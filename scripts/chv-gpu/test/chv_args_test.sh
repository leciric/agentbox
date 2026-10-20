#!/usr/bin/env bash
# Runs the tarball's own cloud-hypervisor on try.sh's exact command line
# (chv_command) and on AgentBox's VM's (chArgs, with the GPU and without), far
# enough to prove it parses and validates them, on a machine with no KVM: it
# opens /dev/kvm and asks KVM_GET_API_VERSION before it reads its options, so
# the run happens in a user and mount namespace whose /dev/kvm is an empty
# file, under fake-kvm, which answers that one ioctl. The first KVM ioctl
# after it (KVM_CHECK_EXTENSION, as the VM is created) fails, and means the
# options were parsed and validated. Nothing else on the machine sees the fake
# /dev/kvm, a real one included. AgentBox's line without the GPU also goes to
# upstream's v53.0 (downloaded, pinned), which must say exactly what ours does.
#
#   scripts/chv-gpu/test/chv_args_test.sh [TARBALL]   (default: out/*.tar.gz)
#
# Needs gcc, go, curl, and unshare with unprivileged user namespaces.
# shellcheck disable=SC2034,SC2154 # try.sh's globals, and chv from chv_command
set -uo pipefail

tdir=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=../try.sh
. "$tdir/../try.sh"
set +e

tarball=${1:-$(ls "$tdir"/../out/chv-*.tar.gz 2>/dev/null | head -n1)}
[ -f "$tarball" ] || { echo "no tarball: run build.sh first, or pass one"; exit 1; }
for c in gcc unshare; do command -v "$c" >/dev/null || { echo "needs $c"; exit 1; }; done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
gcc -O2 -Wall -o "$tmp/fake-kvm" "$tdir/fake-kvm.c" || exit 1
mkdir -p "$tmp/bin" "$tmp/work" "$tmp/cache"
tar -xzf "$tarball" -C "$tmp/bin" --strip-components=1

# try.sh's globals, as run_guest has them; the files only need to exist.
bin=$tmp/bin work=$tmp/work cache=$tmp/cache
cp /dev/null "$cache/CLOUDHV.fd"
truncate -s 12G "$work/disk.raw"
truncate -s 2M "$work/seed.img"

# fake_kvm_run COMMAND...: COMMAND with the fake /dev/kvm, output combined.
fake_kvm_run() {
	rm -f "$work/api.sock" # each run makes its own
	# shellcheck disable=SC2016 # expanded inside the namespace
	timeout 60 unshare --user --map-root-user --mount sh -c '
		set -e
		d=$(mktemp -d)
		# -n: util-linux 2.39 (Ubuntu 24.04) mounts but fails writing
		# /run/mount/utab in a user namespace, and exits non-zero.
		mount -n -t tmpfs tmpfs "$d"
		for n in null zero random urandom full; do
			touch "$d/$n"; mount -n --bind "/dev/$n" "$d/$n"
		done
		touch "$d/kvm"
		mount -n --move "$d" /dev
		exec "$@"' sh "$tmp/fake-kvm" "$@" 2>&1
}

failed=0
check() { # NAME OUTPUT WANT-PARSED(yes|no) [NEEDLE]
	local parsed=no
	grep -q 'fake-kvm: KVM ioctl' <<<"$2" && ! grep -q 'Error parsing config' <<<"$2" && parsed=yes
	if [ "$parsed" = "$3" ] && { [ -z "${4:-}" ] || grep -qF -- "$4" <<<"$2"; }; then
		echo "ok   $1"
	else
		echo "FAIL $1 (parsed: $parsed, want $3${4:+, and \"$4\"}):"
		sed 's/^/     /' <<<"$2" | tail -n 8
		failed=1
	fi
}

chv_command
echo "try.sh runs: ${chv[*]//"$tmp"\//}"
check "try.sh's command line parses and validates" "$(fake_kvm_run "${chv[@]}")" yes \
	"KVM ioctl 0xae03"

# The harness catches what it's there to catch: an option it doesn't know,
# and a config it parses but refuses (vhost-user needs shared memory).
bad=("${chv[@]}")
for i in "${!bad[@]}"; do
	case "${bad[$i]}" in path="$work/disk.raw") bad[i]+=",image_typo=raw" ;; esac
done
check "an unknown option is refused" "$(fake_kvm_run "${bad[@]}")" no \
	"unknown option: image_typo"
bad=("${chv[@]}")
for i in "${!bad[@]}"; do
	case "${bad[$i]}" in size=*M,shared=on) bad[i]=${bad[i]%,shared=on} ;; esac
done
check "vhost-user without shared memory is refused" "$(fake_kvm_run "${bad[@]}")" no \
	"Using vhost-user requires using shared memory"

# AgentBox's VM's own command line (internal/hostvm/chv chArgs), as
# TestWriteCHArgs writes it: with the GPU it parses; without, ours and
# upstream's v53.0 say the same, but for where they were built.
upstream_url=https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v53.0/cloud-hypervisor-static
upstream_sha256=448af3d4e59b22c2987f7df94c213ad40fb53a10d437e42b5ee6c4fce7c29ecc
root=$(cd "$tdir/../../.." && pwd)
args=$tmp/agentbox
mkdir -p "$args"
if ! (cd "$root" && CHV_ARGS_OUT=$args go test -count=1 -run '^TestWriteCHArgs$' ./internal/hostvm/chv >/dev/null); then
	echo "FAIL TestWriteCHArgs"; failed=1
else
	mapfile -t with_gpu <"$args/gpu.args"
	mapfile -t without <"$args/nogpu.args"
	check "AgentBox's VM's line, with the GPU" "$(fake_kvm_run "$bin/bin/cloud-hypervisor" "${with_gpu[@]}")" yes "KVM ioctl 0xae03"
	ours=$(fake_kvm_run "$bin/bin/cloud-hypervisor" "${without[@]}")
	check "AgentBox's VM's line, without the GPU" "$ours" yes "KVM ioctl 0xae03"
	if curl -fsSL --retry 3 -o "$tmp/upstream" "$upstream_url" && echo "$upstream_sha256  $tmp/upstream" | sha256sum -c --quiet; then
		chmod +x "$tmp/upstream"
		theirs=$(fake_kvm_run "$tmp/upstream" "${without[@]}")
		norm() { sed -E 's/[0-9]+\.[0-9]+s:/T:/; s#ERROR:[^ ]*cloud-hypervisor/src/#ERROR:src/#; s/vm\.rs:[0-9]+:[0-9]+/vm.rs:N/'; }
		if [ "$(norm <<<"$ours")" = "$(norm <<<"$theirs")" ]; then
			echo "ok   upstream's v53.0 says the same of it"
		else
			echo "FAIL upstream's v53.0 says otherwise:"; diff <(norm <<<"$theirs") <(norm <<<"$ours") | sed 's/^/     /'; failed=1
		fi
		check "upstream's v53.0 has no --gpu" "$(fake_kvm_run "$tmp/upstream" "${with_gpu[@]}")" no "unexpected argument '--gpu'"
	else
		echo "skip upstream's v53.0 (couldn't fetch it)"
	fi
fi

# crosvm on try.sh's line (crosvm_command): it binds its socket only once
# --params parsed, and refuses keys it doesn't know, so this also proves the
# render-node key (our patch) is there. Nothing connects, so it never opens
# the render node, which needn't exist.
crosvm_bound() { # SOCKET COMMAND...: "bound", or crosvm's last line
	local sock=$1 p
	shift
	rm -f "$sock"
	"$@" > "$tmp/crosvm.log" 2>&1 &
	p=$!
	for _ in $(seq 50); do
		[ -S "$sock" ] && break
		kill -0 "$p" 2>/dev/null || break
		sleep 0.1
	done
	if [ -S "$sock" ]; then echo bound; else tail -n 1 "$tmp/crosvm.log"; fi
	kill "$p" 2>/dev/null
	wait "$p" 2>/dev/null
}
if ldd "$bin/bin/crosvm" | grep -q 'not found'; then
	echo "skip crosvm (this machine lacks its libraries: $(ldd "$bin/bin/crosvm" | awk '/not found/ {print $1}' | tr '\n' ' '))"
else
	# The params alone first: no namespace, the node needn't exist.
	render_node=/dev/dri/renderD128 CHVGPU_ISOLATE=none
	crosvm_command
	echo "try.sh runs: ${crosvm[*]//"$tmp"\//}"
	got=$(crosvm_bound "$work/gpu.sock" "${crosvm[@]}")
	[ "$got" = bound ] && echo "ok   crosvm takes try.sh's --params (render-node included)" ||
		{ echo "FAIL crosvm on try.sh's line: $got"; failed=1; }
	if [ -f "$args/crosvm.args" ]; then
		mapfile -t agentbox_crosvm <"$args/crosvm.args"
		got=$(crosvm_bound "$args/vm/run/gpu.sock" "$bin/bin/crosvm" "${agentbox_crosvm[@]}")
		[ "$got" = bound ] && echo "ok   crosvm takes AgentBox's line (crosvmArgs)" ||
			{ echo "FAIL crosvm on AgentBox's line: $got"; failed=1; }
	fi
	bad=("${crosvm[@]}")
	bad[-1]=${bad[-1]//render-node/render-nodee}
	got=$(crosvm_bound "$work/gpu.sock" "${bad[@]}")
	[ "$got" != bound ] && echo "ok   crosvm refuses an unknown key ($got)" ||
		{ echo "FAIL crosvm took an unknown --params key"; failed=1; }

	# The same crosvm kept to one render node (lib/gpu-ns.sh): /dev/null
	# stands in for the node and a directory of ours for /dev/dri, which this
	# machine may not have.
	if unshare --user --map-root-user --mount true 2>/dev/null; then
		mkdir -p "$tmp/dri"
		render_node=/dev/null CHVGPU_DRI_DIR=$tmp/dri CHVGPU_ISOLATE=unshare
		crosvm_command
		got=$(crosvm_bound "$work/gpu.sock" "${crosvm[@]}")
		[ "$got" = bound ] && echo "ok   crosvm runs in its own /dev/dri" ||
			{ echo "FAIL crosvm in its namespace: $got"; failed=1; }
		has_line=$(grep -c "chv-gpu: $tmp/dri in crosvm's namespace: null (device 1:3)" "$tmp/crosvm.log")
		[ "$has_line" = 1 ] && echo "ok   it says which node it sees" ||
			{ echo "FAIL no namespace line in crosvm.log:"; sed 's/^/     /' "$tmp/crosvm.log" | head -5; failed=1; }
		[ -z "$(ls -A "$tmp/dri")" ] && echo "ok   the host's directory is untouched" ||
			{ echo "FAIL the namespace leaked into the host: $(ls "$tmp/dri")"; failed=1; }
		[ "$(find "${TMPDIR:-/tmp}" -maxdepth 1 -newer "$tmp/dri" -type d -empty -name 'tmp.*' | wc -l)" = 0 ] &&
			echo "ok   no directory left behind" || echo "note: an empty tmp.* directory appeared (another process?)"
		unset CHVGPU_DRI_DIR CHVGPU_ISOLATE
	else
		echo "skip crosvm in a namespace (no unprivileged user namespaces here)"
	fi
fi

if [ $failed -ne 0 ]; then
	echo FAILED
	exit 1
fi
echo PASS
