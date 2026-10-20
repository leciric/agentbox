#!/usr/bin/env bash
# Tries the GPU spike on this machine: boots a throwaway Debian 13 guest under
# the patched Cloud Hypervisor with crosvm's GPU device on the host's render
# node, runs glxinfo, vulkaninfo, glmark2 and vkmark in it with the GPU and
# then in software, and prints one summary block to paste back.
#
#   scripts/chv-gpu/try.sh [--tarball FILE | --dir DIR] [options]
#
#   --tarball FILE    the tarball build.sh made (default: the one in out/)
#   --dir DIR         an unpacked tarball instead
#   --render-node DEV the host GPU to use, for GL and Vulkan (default
#                     /dev/dri/renderD128; --check lists them)
#   --cpus N          the guest's CPUs (default 4)
#   --memory MiB      the guest's memory (default 4096)
#   --timeout MIN     give up on the guest after this long (default 30)
#   --keep            keep the work directory (disk, serial log, process logs)
#   --isolate-gpu     also run crosvm where /dev/dri holds only the render node
#                     (an experiment: it broke vrend's winsys on an RX 6750 XT)
#   --check           only check the prerequisites
#
# Runs as your user, with no root and no network setup: passt gives the guest
# its network, the way AgentBox's own VM gets one. Everything it starts runs
# from its own work directory and stops when it exits; downloads (the Debian
# image, the firmware) are cached in ~/.cache/agentbox-chv-gpu.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib/parse.sh
. "$here/lib/parse.sh"
# shellcheck source=versions.env
. "$here/versions.env"

cache=${XDG_CACHE_HOME:-$HOME/.cache}/agentbox-chv-gpu
render_node=/dev/dri/renderD128
cpus=4
memory=4096
timeout_min=30
keep=false
check_only=false
tarball=
dir=
work=
pids=()
gl_nodes=''
isolation=''
isolate_gpu=false
host_nodes=''
declare -A pid
problems=()

say() { printf '\033[1m==>\033[0m %s\n' "$*" >&2; }
die() { printf 'try.sh: %s\n' "$*" >&2; exit 1; }
problem() { problems+=("$*"); }

# --- prerequisites -----------------------------------------------------------

# pkg_version NAME...: the installed version of the first of these packages the
# host's package manager knows, or nothing.
pkg_version() {
	local p v
	for p; do
		v=''
		# Each package manager in turn: some hosts have more than one.
		command -v dpkg-query >/dev/null &&
			v=$(dpkg-query -W -f '${db:Status-Status} ${Version}' "$p" 2>/dev/null | sed -n 's/^installed //p' || true)
		[ -z "$v" ] && command -v pacman >/dev/null &&
			v=$(pacman -Q "$p" 2>/dev/null | awk '{print $2}' || true)
		[ -z "$v" ] && command -v rpm >/dev/null &&
			v=$(rpm -q --qf '%{VERSION}-%{RELEASE}' "$p" 2>/dev/null | grep -v 'not installed' || true)
		[ -n "$v" ] && { echo "$v"; return; }
	done
}

host_mesa() {
	local v
	v=$(pkg_version libgl1-mesa-dri mesa-libgallium mesa-dri-drivers mesa)
	if [ -z "$v" ] && command -v glxinfo >/dev/null && [ -n "${DISPLAY:-}" ]; then
		v=$(mesa_version "$(glxinfo -B 2>/dev/null | grep 'version string' | head -n1)")
	fi
	echo "${v:-unknown}"
}

host_virglrenderer() {
	local v
	v=$(pkg_version libvirglrenderer1 virglrenderer)
	[ -n "$v" ] || v=$(pkg-config --modversion virglrenderer 2>/dev/null || true)
	echo "${v:-not installed}"
}

# render_node_info DEV: "driver|vendor:device" of the GPU behind a render node.
render_node_info() {
	local sys driver='?' ids='?'
	sys=/sys/class/drm/$(basename "$1")/device
	[ -e "$sys/driver" ] && driver=$(basename "$(readlink -f "$sys/driver")")
	[ -r "$sys/vendor" ] && ids="$(sed 's/^0x//' "$sys/vendor"):$(sed 's/^0x//' "$sys/device")"
	echo "$driver|$ids"
}

# host_vulkan: the host's own Vulkan GPU, per its vulkaninfo, if installed.
host_vulkan() {
	command -v vulkaninfo >/dev/null || { echo "vulkaninfo not installed"; return; }
	local f best
	f=$(mktemp)
	vulkaninfo --summary > "$f" 2>/dev/null || true
	best=$(vulkan_best "$f")
	rm -f "$f"
	if [ -n "$best" ]; then
		IFS='|' read -r _ type name driver api _ <<<"$best"
		echo "$name ($driver, $type, Vulkan $api)"
	else
		echo "no device"
	fi
}

check_prereqs() {
	local fatal=0 info
	say "Checking prerequisites"
	[ "$(uname -s)/$(uname -m)" = Linux/x86_64 ] || { echo "  needs x86_64 Linux"; fatal=1; }
	if [ ! -e /dev/kvm ]; then
		echo "  /dev/kvm: missing (is KVM enabled in the firmware, and kvm_intel/kvm_amd loaded?)"; fatal=1
	elif [ ! -r /dev/kvm ] || [ ! -w /dev/kvm ]; then
		echo "  /dev/kvm: not usable by $(id -un): add yourself to its group ($(stat -c %G /dev/kvm))"; fatal=1
	else
		echo "  /dev/kvm: ok"
	fi
	if [ ! -e "$render_node" ]; then
		echo "  $render_node: missing; render nodes here: $(ls /dev/dri/renderD* 2>/dev/null | tr '\n' ' ')"; fatal=1
	elif [ ! -r "$render_node" ] || [ ! -w "$render_node" ]; then
		echo "  $render_node: not usable by $(id -un): add yourself to its group ($(stat -c %G "$render_node"))"; fatal=1
	else
		info=$(render_node_info "$render_node")
		echo "  $render_node: ok (driver ${info%%|*}, PCI ${info#*|})"
		case "${info%%|*}" in
		nvidia) echo "  warning: NVIDIA's proprietary driver: virgl may work, Venus is known not to (README)" ;;
		esac
	fi
	local n
	for n in /dev/dri/renderD*; do
		[ -e "$n" ] || continue
		[ "$(readlink -f "$n")" = "$(readlink -m "$render_node")" ] && continue
		info=$(render_node_info "$n")
		echo "  also: $n (driver ${info%%|*}, PCI ${info#*|}); --render-node $n to use it"
	done
	case "$(gpu_isolation)" in
	none) $isolate_gpu && echo "  GPU isolation: asked for, but no unprivileged user namespaces or bwrap here" ;;
	*) echo "  GPU isolation: $(gpu_isolation) (crosvm sees only $render_node)" ;;
	esac
	echo "  host Mesa: $(host_mesa)"
	echo "  host virglrenderer: $(host_virglrenderer) (try.sh uses the tarball's own $VIRGLRENDERER_VERSION)"
	echo "  host Vulkan: $(host_vulkan)"
	local missing=()
	for c in curl tar xz sha256sum sha512sum; do command -v "$c" >/dev/null || missing+=("$c"); done
	seed_tool >/dev/null || missing+=("one of cloud-localds, genisoimage, mkisofs, xorriso, mkfs.vfat+mcopy")
	if [ ${#missing[@]} -gt 0 ]; then
		echo "  missing: ${missing[*]}"; fatal=1
	fi
	if command -v passt >/dev/null && passt --help 2>&1 | grep -q -- '--vhost-user'; then
		echo "  passt: $(command -v passt)"
	else
		echo "  passt: none with --vhost-user here; try.sh will download passt.top's latest static build"
	fi
	return $fatal
}

# --- the binaries ------------------------------------------------------------

unpack() {
	if [ -n "$dir" ]; then
		bin=$(cd "$dir" && pwd)
	else
		[ -n "$tarball" ] || tarball=$(ls "$here"/out/chv-*.tar.gz 2>/dev/null | head -n1 || true)
		[ -f "$tarball" ] || die "no tarball: run build.sh first, or pass --tarball or --dir"
		mkdir -p "$work/bin"
		tar -xzf "$tarball" -C "$work/bin" --strip-components=1
		bin=$work/bin
	fi
	for f in bin/cloud-hypervisor bin/crosvm libexec/virgl_render_server; do
		[ -x "$bin/$f" ] || die "$bin/$f is missing"
	done
	local missing
	missing=$(ldd "$bin/bin/crosvm" "$bin/libexec/virgl_render_server" 2>/dev/null | awk '/not found/ {print $1}' | sort -u | tr '\n' ' ')
	[ -z "$missing" ] || die "the host lacks libraries crosvm needs: $missing(see HOST-LIBRARIES in the tarball)"
}

# fetch URL SUM FILE: a cached download, checked against its SHA-256 or
# SHA-512 (by the sum's length).
fetch() {
	local url=$1 sum=$2 file=$cache/$3 tool=sha256sum
	[ ${#sum} -eq 128 ] && tool=sha512sum
	if [ -f "$file" ] && echo "$sum  $file" | $tool -c --quiet 2>/dev/null; then
		return
	fi
	say "Downloading $(basename "$url")"
	mkdir -p "$cache"
	curl -fL --retry 3 --progress-bar -o "$file.part" "$url"
	echo "$sum  $file.part" | $tool -c --quiet || die "$url: checksum mismatch"
	mv "$file.part" "$file"
}

# passt_bin: a passt with vhost-user (passt 2025 and later): the host's own,
# or passt.top's latest static build, cached.
passt_bin() {
	if command -v passt >/dev/null && passt --help 2>&1 | grep -q -- '--vhost-user'; then
		command -v passt
		return
	fi
	if [ ! -x "$cache/passt" ]; then
		say "Downloading passt (passt.top publishes only its latest build)"
		mkdir -p "$cache"
		curl -fsSL --retry 3 -o "$cache/passt.part" "$PASST_URL"
		chmod +x "$cache/passt.part" && mv "$cache/passt.part" "$cache/passt"
	fi
	echo "$cache/passt"
}

# --- the guest ---------------------------------------------------------------

# The guest's packages: Mesa's GL drivers (virgl and llvmpipe), its Vulkan
# drivers (Venus is virtio_icd, lavapipe lvp_icd), the tools that report on
# them, and an X server for the GL ones.
guest_packages=(mesa-utils mesa-utils-bin vulkan-tools mesa-vulkan-drivers
	libgl1-mesa-dri libegl-mesa0 glmark2-x11 vkmark xserver-xorg-core pciutils)

# render_ids NODE: a render node's "vendor:device", as Mesa's device-select
# layer takes it, or nothing.
render_ids() {
	local ids
	ids=$(render_node_info "$1")
	ids=${ids#*|}
	[ "$ids" = "?" ] || echo "$ids"
}

# node_ids NODE: render_ids, from host_nodes when it has the node (tests set
# it), else from sysfs.
node_ids() {
	local n ids
	n=$(readlink -m "$1")
	ids=$(awk -F'|' -v n="$n" '$1 == n { print $2; exit }' <<<"${host_nodes:-}")
	[ -n "$ids" ] && { echo "$ids"; return; }
	render_ids "$1"
}

user_data() {
	cat <<EOF
#cloud-config
# Written by AgentBox's scripts/chv-gpu/try.sh: a throwaway guest.
package_update: true
packages: [$(IFS=,; echo "${guest_packages[*]}" | sed 's/,/, /g')]
write_files:
  - path: /etc/chv-gpu.conf
    content: |
      # The host GPU try.sh chose (--render-node), for Venus in the guest.
      VK_DEVICE=$(render_ids "$render_node")
  - path: /usr/local/sbin/chv-gpu-run-tests
    permissions: '0755'
    encoding: b64
    content: $(base64 -w0 < "$here/guest/run-tests.sh")
runcmd:
  - [/usr/local/sbin/chv-gpu-run-tests]
EOF
}

# tool NAME: NAME's path, also looking in the sbin directories a user's PATH
# often lacks (Debian and Fedora keep mkfs.vfat there).
tool() {
	local d
	command -v "$1" 2>/dev/null && return
	for d in /usr/sbin /sbin /usr/local/sbin; do
		[ -x "$d/$1" ] && { echo "$d/$1"; return; }
	done
	return 1
}

# seed_tool: the first tool here that can write a NoCloud seed, or the one
# CHVGPU_SEED_TOOL names (test/seed_test.sh tries each).
seed_tool() {
	local t
	for t in ${CHVGPU_SEED_TOOL:-cloud-localds genisoimage mkisofs xorriso mkfs.vfat}; do
		case "$t" in
		mkfs.vfat) tool mkfs.vfat >/dev/null && tool mcopy >/dev/null && { echo "$t"; return 0; } ;;
		*) tool "$t" >/dev/null && { echo "$t"; return 0; } ;;
		esac
	done
	return 1
}

# make_seed DIR OUT: the NoCloud seed (volume "cidata") from DIR's user-data
# and meta-data.
make_seed() {
	local d=$1 out=$2 t
	t=$(seed_tool) || die "no tool to write the cloud-init seed with"
	case "$t" in
	cloud-localds) "$(tool cloud-localds)" "$out" "$d/user-data" "$d/meta-data" ;;
	genisoimage | mkisofs)
		"$(tool "$t")" -quiet -output "$out" -volid cidata -joliet -rock "$d/user-data" "$d/meta-data" ;;
	xorriso)
		"$(tool xorriso)" -as mkisofs -quiet -output "$out" -volid cidata -joliet -rock "$d/user-data" "$d/meta-data" ;;
	mkfs.vfat)
		rm -f "$out"
		truncate -s 2M "$out"
		"$(tool mkfs.vfat)" -n CIDATA "$out" >/dev/null
		"$(tool mcopy)" -i "$out" "$d/user-data" "$d/meta-data" ::
		;;
	esac
}

prepare_guest() {
	fetch "$FIRMWARE_URL" "$FIRMWARE_SHA256" CLOUDHV.fd
	fetch "$DEBIAN_IMAGE_URL" "$DEBIAN_IMAGE_SHA512" "$(basename "$DEBIAN_IMAGE_URL")"
	say "Preparing the guest's disk"
	tar -xJf "$cache/$(basename "$DEBIAN_IMAGE_URL")" -C "$work" disk.raw
	truncate -s 12G "$work/disk.raw"
	mkdir -p "$work/seed"
	user_data > "$work/seed/user-data"
	printf 'instance-id: chv-gpu-%s\nlocal-hostname: chv-gpu\n' "$$" > "$work/seed/meta-data"
	make_seed "$work/seed" "$work/seed.img"
}

# --- processes ---------------------------------------------------------------

# start NAME COMMAND...: runs a process in its own session, logging to
# $work/NAME.log, so cleanup can stop it and anything it started.
start() {
	local name=$1
	shift
	setsid "$@" > "$work/$name.log" 2>&1 < /dev/null &
	pids+=("$!")
	pid[$name]=$!
}

alive() { kill -0 "$1" 2>/dev/null; }

wait_for_socket() {
	local sock=$1 p=$2 name=$3
	for _ in $(seq 100); do
		[ -S "$sock" ] && return
		alive "$p" || break
		sleep 0.1
	done
	tail -n 20 "$work/$name.log" >&2 || true
	die "$name didn't start"
}

cleanup() {
	local rc=$? p
	for p in "${pids[@]}"; do kill -TERM -- "-$p" 2>/dev/null || true; done
	for p in "${pids[@]}"; do
		for _ in $(seq 50); do alive "$p" || break; sleep 0.1; done
		kill -KILL -- "-$p" 2>/dev/null || true
	done
	if [ -n "$work" ] && [ -d "$work" ]; then
		if $keep; then
			echo "Kept $work" >&2
		else
			rm -rf "$work"
		fi
	fi
	exit "$rc"
}

# gpu_isolation: whether, and how, crosvm runs where /dev/dri holds only the
# render node: "none" (the default: render-node does it), or with
# --isolate-gpu "unshare" (lib/gpu-ns.sh) or "bwrap", whichever works here.
# CHVGPU_ISOLATE overrides.
gpu_isolation() {
	if [ -n "${CHVGPU_ISOLATE:-}" ]; then
		echo "$CHVGPU_ISOLATE"
	elif ! $isolate_gpu; then
		echo none
	elif unshare --user --map-root-user --mount true 2>/dev/null; then
		echo unshare
	elif command -v bwrap >/dev/null && bwrap --dev-bind / / true 2>/dev/null; then
		echo bwrap
	else
		echo none
	fi
}

# crosvm_command: crosvm's GPU device command line, into the array crosvm,
# and how it's kept to the render node, into isolation.
crosvm_command() {
	local params node dri=${CHVGPU_DRI_DIR:-/dev/dri}
	node=$(readlink -m "$render_node")
	# Which GPU: render-node (our crosvm patch) hands rutabaga the node as a
	# GPU path, and rutabaga (patched: 0.1.75 rejected every render node, its
	# Path::starts_with comparing whole components) gives virglrenderer its
	# fd, for GL's GBM device. crosvm.log says "virglrenderer: using GPU path".
	# Venus's Vulkan, in virgl_render_server, lists every host GPU; the guest
	# puts this one first (run-tests.sh). Not Mesa's device-select layer here
	# nor DRI_PRIME: in virgl_render_server the layer breaks Venus
	# (vkEnumeratePhysicalDeviceGroups returns devices vkr never saw,
	# ERROR_INITIALIZATION_FAILED), and virglrenderer's own node choice
	# ignores DRI_PRIME. --isolate-gpu also hides the other render nodes from
	# crosvm (gpu_isolation), but on a user's RX 6750 XT that made vrend's
	# winsys fail and both GL and Venus fall back to software.
	# surfaceless off: EGL from the GBM device on that node, not Mesa's
	# default device.
	# external-blob: every blob the guest maps must reach Cloud Hypervisor as
	# an fd (SHMEM_MAP). Without it crosvm falls back, for GL buffers, to
	# EXTERNAL_MAP (a pointer into its own memory), which Spectrum's patchset
	# doesn't implement: Cloud Hypervisor drops the device (InvalidMessage)
	# and crosvm panics. With it, virglrenderer allocates those buffers with
	# GBM (built with minigbm_allocation) and exports them as dma-bufs.
	params=$(printf '{"context-types":"virgl:virgl2:venus","egl":true,"surfaceless":false,"vulkan":true,"implicit-render-server":true,"external-blob":true,"render-node":"%s"}' \
		"$dri/$(basename "$node")")
	# Headless: without DISPLAY and WAYLAND_DISPLAY crosvm's display is its
	# stub, which still gives the guest a scanout for its X server.
	local base=(env -u DISPLAY -u WAYLAND_DISPLAY
		RENDER_SERVER_EXEC_PATH="$bin/libexec/virgl_render_server"
		"$bin/bin/crosvm" --no-syslog device gpu --socket-path "$work/gpu.sock" --params "$params")
	isolation=$(gpu_isolation)
	case "$isolation" in
	unshare)
		crosvm=(unshare --user --map-root-user --mount
			"$here/lib/gpu-ns.sh" "$node" "$dri" "$(id -u)" "$(id -g)" "${base[@]}") ;;
	bwrap)
		# shellcheck disable=SC2016 # expanded by the sh inside bwrap
		crosvm=(bwrap --dev-bind / / --tmpfs "$dri" --dev-bind "$node" "$dri/$(basename "$node")" --
			sh -c 'echo "chv-gpu: $0 in crosvm'"'"'s namespace: $(ls "$0" | tr "\n" " ")" >&2; exec "$@"' "$dri" "${base[@]}") ;;
	*)
		crosvm=("${base[@]}") ;;
	esac
}

# open_render_nodes PID [PROC]: the render nodes a process has open, one per
# line, as this host names them: for crosvm, the one GL renders on (Venus's
# are virgl_render_server's). Matched by device number, since crosvm sees
# them through its own /dev/dri; a node that doesn't stat is given as named.
open_render_nodes() {
	local f t dev n
	for f in "${2:-/proc}/$1"/fd/*; do
		t=$(readlink "$f" 2>/dev/null) || continue
		case "$t" in /dev/dri/renderD*) ;; *) continue ;; esac
		dev=$(stat -L -c '%t:%T' "$f" 2>/dev/null) || { echo "$t"; continue; }
		for n in /dev/dri/renderD*; do
			[ "$(stat -c '%t:%T' "$n" 2>/dev/null)" = "$dev" ] && { echo "$n"; continue 2; }
		done
		echo "$t"
	done | sort -u
}

# render_nodes: every render node here, "node|vendor:device" per line.
render_nodes() {
	local n info
	for n in /dev/dri/renderD*; do
		[ -e "$n" ] || continue
		info=$(render_node_info "$n")
		echo "$n|${info#*|}"
	done
}

# chv_command: Cloud Hypervisor's command line, into the array chv: the
# tarball's v53.0 with the patchset, the one AgentBox's VM runs.
# test/chv_args_test.sh runs the tarball's binary on exactly this.
chv_command() {
	# shared=on: vhost-user devices (passt, the GPU) map the guest's memory.
	chv=("$bin/bin/cloud-hypervisor"
		--api-socket "path=$work/api.sock"
		--firmware "$cache/CLOUDHV.fd"
		--cpus "boot=$cpus"
		--memory "size=${memory}M,shared=on"
		--disk "path=$work/disk.raw" "path=$work/seed.img,readonly=on"
		--net "vhost_user=true,socket=$work/passt.sock,vhost_mode=client"
		--gpu "socket=$work/gpu.sock"
		--serial "file=$work/serial.log" --console off)
}

run_guest() {
	local passt
	passt=$(passt_bin)
	say "Starting passt, crosvm's GPU device and Cloud Hypervisor"
	start passt "$passt" --vhost-user --socket "$work/passt.sock" --foreground \
		--tcp-ports none --udp-ports none
	wait_for_socket "$work/passt.sock" "${pid[passt]}" passt
	crosvm_command
	start crosvm "${crosvm[@]}"
	wait_for_socket "$work/gpu.sock" "${pid[crosvm]}" crosvm
	chv_command
	# A panic's backtrace goes to its log, for whoever reads it with --keep.
	start cloud-hypervisor env RUST_BACKTRACE=1 "${chv[@]}"

	say "Booting; the guest installs Mesa and the benchmarks, then runs them (a few minutes)"
	local deadline=$((SECONDS + timeout_min * 60)) shown='' crosvm_gone=''
	while [ $SECONDS -lt $deadline ]; do
		# What crosvm has open is what GL renders on; it opens it as the guest
		# first uses the GPU, so the last look before the end counts.
		local nodes
		nodes=$(open_render_nodes "${pid[crosvm]}")
		[ -n "$nodes" ] && gl_nodes=$nodes
		if grep -q '^CHVGPU-END' "$work/serial.log" 2>/dev/null; then
			return 0
		fi
		if [ -z "$crosvm_gone" ] && ! alive "${pid[crosvm]}"; then
			crosvm_gone=1
			problem "crosvm exited during the run: $(chv_failure "$work/crosvm.log")"
		fi
		if ! alive "${pid[cloud-hypervisor]}"; then
			grep -q '^CHVGPU-END' "$work/serial.log" 2>/dev/null && return 0
			problem "Cloud Hypervisor exited before the guest finished: $(chv_failure "$work/cloud-hypervisor.log")"
			return 1
		fi
		local stage
		stage=$(progress "$work/serial.log")
		if [ -n "$stage" ] && [ "$stage" != "$shown" ]; then
			echo "    $stage" >&2
			shown=$stage
		fi
		sleep 3
	done
	problem "the guest didn't finish within $timeout_min minutes"
	return 1
}

# progress SERIAL_LOG: a word on how far the guest has got.
progress() {
	local s
	s=$(tr -d '\r' 2>/dev/null < "$1" | grep -aoE 'Cloud-init v\. [^ ]+ (running|finished)[^.]*|Reached target [A-Za-z -]+|login:' | tail -n1 || true)
	echo "$s"
}

# --- the summary -------------------------------------------------------------

# summary SERIAL_LOG: the block to paste back. Reads the host facts from
# variables check_prereqs' helpers fill in, so it can be tested on a log.
summary() {
	local serial=$1 tmp gl_gpu gl_sw egl vk_gpu vk_sw g_gpu g_sw v_gpu v_sw feats
	tmp=$(mktemp)
	if ! results "$serial" > "$tmp"; then
		problem "no results from the guest (see the serial log with --keep)"
	fi
	echo "----- chv-gpu try.sh summary (paste this back) -----"
	echo "date:        $(date -u +%Y-%m-%dT%H:%MZ)"
	echo "host:        ${host_os:-?}, kernel ${host_kernel:-?}, ${host_cpu:-?}"
	echo "render node: ${render_node} (driver ${host_gpu_driver:-?}, PCI ${host_gpu_ids:-?})"
	echo "host Mesa:   ${host_mesa_v:-?}; host Vulkan: ${host_vulkan_v:-?}"
	echo "virglrenderer: host ${host_virgl_v:-?}; used: bundled $VIRGLRENDERER_VERSION"
	echo "binaries:    ${binaries:-?}"
	if [ -s "$tmp" ]; then
		echo "guest:       $(kv "$tmp" os), kernel $(kv "$tmp" kernel), Mesa $(kv "$tmp" mesa)"
	else
		echo "guest:       no results"
	fi
	feats=$(section "$tmp" guest | drm_features -)
	echo "virtio-gpu:  ${feats:-no [drm] features line}"

	gl_gpu=$(section "$tmp" glxinfo-gpu | glxinfo_summary -)
	gl_sw=$(section "$tmp" glxinfo-sw | glxinfo_summary -)
	egl=$(section "$tmp" eglinfo-gpu | eglinfo_summary -)
	vk_gpu=$(section "$tmp" vulkaninfo-gpu | vulkan_best -)
	vk_sw=$(section "$tmp" vulkaninfo-sw | vulkan_best -)
	g_gpu=$(section "$tmp" glmark2-gpu | score - glmark2)
	g_sw=$(section "$tmp" glmark2-sw | score - glmark2)
	v_gpu=$(section "$tmp" vkmark-gpu | score - vkmark)
	v_sw=$(section "$tmp" vkmark-sw | score - vkmark)

	gl_line() { # LABEL "renderer|version|accelerated"
		local renderer version accel
		IFS='|' read -r renderer version accel <<<"$2"
		if [ -z "$renderer" ]; then
			echo "$1 failed"
		else
			echo "$1 $renderer; $version${accel:+; accelerated: $accel}"
		fi
	}
	vk_line() { # LABEL "GPUn|type|name|driver|api|info"
		local name driver api type
		IFS='|' read -r _ type name driver api _ <<<"$2"
		if [ -z "$name" ]; then
			echo "$1 failed"
		else
			echo "$1 $name; $driver ($type), Vulkan $api"
		fi
	}
	gl_line "GL (GPU):        " "$gl_gpu"
	gl_line "GL (software):   " "$gl_sw"
	echo "EGL surfaceless: $(if [ -n "${egl%%|*}" ]; then echo "${egl%%|*}; ${egl#*|}"; else echo failed; fi)"
	vk_line "Vulkan (GPU):    " "$vk_gpu"
	vk_line "Vulkan (software):" "$vk_sw"
	echo "glmark2:         GPU ${g_gpu:-failed}, software ${g_sw:-failed} (x$(ratio "$g_gpu" "$g_sw"))"
	echo "vkmark:          GPU ${v_gpu:-failed}, software ${v_sw:-failed} (x$(ratio "$v_gpu" "$v_sw"))"
	# Where GL and Vulkan ran, as lines of their own (computed below).
	local where_gl where_vk vk_ids_shown
	where_gl=$(head -n1 <<<"${gl_nodes:-}")
	[ -n "$where_gl" ] && where_gl="$where_gl ($(node_ids "$where_gl"))"
	vk_ids_shown=$(section "$tmp" vkmark-gpu | vulkan_ids -)
	[ -n "$vk_ids_shown" ] || vk_ids_shown=$(section "$tmp" vulkaninfo-gpu | vulkan_ids -)
	where_vk=${vk_ids_shown:+$vk_ids_shown}
	[ -n "$where_vk" ] && where_vk="$where_vk ($(node_of "$vk_ids_shown" "${host_nodes:-}" | grep . || echo "no render node here"))"
	echo "GL node asked:   $(grep -m1 -oE "virglrenderer: (using GPU path .*|no GPU path.*|GPU path .* isn't a render node.*)" "$work/crosvm.log" 2>/dev/null || echo "crosvm didn't say")"
	[ "${isolation:-none}" = none ] ||
		echo "GPU isolation:   $isolation$(grep -m1 -o "in crosvm's namespace: .*" "$work/crosvm.log" 2>/dev/null | sed 's/^/ (/; s/$/)/')"
	echo "GL ran on:       ${where_gl:-unknown (crosvm had no render node open)}"
	echo "Vulkan ran on:   ${where_vk:-unknown}"

	# What went wrong, from what the guest printed.
	local name rc
	for name in glxinfo-gpu eglinfo-gpu vulkaninfo-gpu glmark2-gpu vkmark-gpu glxinfo-sw vulkaninfo-sw glmark2-sw vkmark-sw; do
		rc=$(section_rc "$tmp" "$name")
		case "$rc" in
		0) ;;
		missing) [ -s "$tmp" ] && problem "$name: didn't run" ;;
		*) problem "$name: exit $rc: $(section "$tmp" "$name" | grep -v '^[[:space:]]*$' | tail -n 1 | cut -c1-160)" ;;
		esac
	done
	# Where each ran, against the render node asked for: GL's node is what
	# crosvm had open, Vulkan's the GPU (vendor:device) vkmark reported.
	local want_ids gl_node vk_ids vk_node gl_host vk_host
	want_ids=$(node_ids "$render_node")
	gl_node=$(head -n1 <<<"${gl_nodes:-}")
	vk_ids=$(section "$tmp" vkmark-gpu | vulkan_ids -)
	[ -n "$vk_ids" ] || vk_ids=$(section "$tmp" vulkaninfo-gpu | vulkan_ids -)
	vk_node=$(node_of "$vk_ids" "${host_nodes:-}")
	if [ -n "$gl_node" ] && [ "$gl_node" != "$(readlink -m "$render_node")" ]; then
		problem "GL ran on $gl_node, not $render_node"
	fi
	if [ -n "$vk_ids" ] && [ -n "$want_ids" ] && [ "$vk_ids" != "$want_ids" ]; then
		problem "Vulkan ran on $vk_ids${vk_node:+ ($vk_node)}, not $render_node's $want_ids"
	fi
	# Without crosvm's node (a log read later), the GPUs' names tell.
	gl_host=$(host_gpu "${gl_gpu%%|*}")
	IFS='|' read -r _ _ vk_host _ <<<"$vk_gpu"
	vk_host=$(host_gpu "$vk_host")
	if [ -z "$gl_node" ] && [ -n "$gl_host" ] && [ -n "$vk_host" ] && ! same_gpu "$gl_host" "$vk_host"; then
		problem "GL and Vulkan ran on different host GPUs: GL on ${gl_host%/}, Vulkan on ${vk_host%/}"
	fi
	is_software "${gl_gpu%%|*}" && problem "GL with the GPU fell back to software (${gl_gpu%%|*})"
	[[ ${vk_gpu} == *"|cpu|"* ]] && problem "Vulkan with the GPU is the CPU (${vk_gpu})"
	[ -n "$(section "$tmp" xorg)" ] && problem "Xorg didn't start: $(section "$tmp" xorg | grep -E '\(EE\)' | head -n 2 | tr '\n' ' ')"
	blob_problems "$work/crosvm.log"
	if [ ${#problems[@]} -gt 0 ]; then
		echo "problems:"
		printf '  - %s\n' "${problems[@]}"
	else
		echo "problems:        none"
	fi
	local tail
	tail=$(section "$tmp" dmesg-tail | tail -n 8)
	[ -z "$tail" ] || { echo "guest kernel (last lines):"; sed 's/^/  /' <<<"$tail"; }
	echo "-----"
	rm -f "$tmp"
}

# blob_problems CROSVM_LOG: a problem for each way crosvm failed to hand a
# blob to Cloud Hypervisor, with what it means.
blob_problems() {
	[ -r "$1" ] || return 0
	local n
	n=$(grep -c "create external mapping" "$1" || true)
	[ "$n" -gt 0 ] && problem "crosvm sent $n blob(s) as EXTERNAL_MAP, which Spectrum's patchset doesn't implement (external-blob off?)"
	n=$(grep -c "can't fallback to external mapping" "$1" || true)
	[ "$n" -gt 0 ] && problem "$n blob(s) couldn't be exported as an fd to map into the guest (opaque Vulkan memory, or GL without GL_EXT_memory_object_fd on the host)"
	return 0
}

host_facts() {
	local info
	host_os=$( (. /etc/os-release && echo "$PRETTY_NAME") 2>/dev/null || echo "?")
	host_kernel=$(uname -r)
	host_cpu=$(sed -n 's/^model name[[:space:]]*: //p' /proc/cpuinfo | head -n1)
	info=$(render_node_info "$render_node")
	host_gpu_driver=${info%%|*}
	host_gpu_ids=${info#*|}
	host_mesa_v=$(host_mesa)
	host_vulkan_v=$(host_vulkan)
	host_virgl_v=$(host_virglrenderer)
	host_nodes=$(render_nodes)
	binaries=$(head -n1 "$bin/VERSIONS" 2>/dev/null || echo "?")
	[ -n "$tarball" ] && binaries="$binaries; $(basename "$tarball") $(sha256sum "$tarball" | cut -c1-16)"
}

main() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--tarball) tarball=$2; shift ;;
		--dir) dir=$2; shift ;;
		--render-node) render_node=$2; shift ;;
		--cpus) cpus=$2; shift ;;
		--memory) memory=$2; shift ;;
		--timeout) timeout_min=$2; shift ;;
		--keep) keep=true ;;
		--isolate-gpu) isolate_gpu=true ;;
		--check) check_only=true ;;
		-h | --help) sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown option $1 (see --help)" ;;
		esac
		shift
	done

	check_prereqs || die "prerequisites missing (above)"
	$check_only && exit 0

	mkdir -p "$cache"
	work=$(mktemp -d "$cache/run.XXXXXX")
	trap cleanup EXIT
	trap 'exit 130' INT TERM
	unpack
	prepare_guest
	run_guest || true
	host_facts
	summary "$work/serial.log"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
	main "$@"
fi
