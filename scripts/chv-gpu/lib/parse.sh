# shellcheck shell=bash
# Parsing for try.sh: what the guest printed on its serial console, and what
# the host's own tools say. Sourced, never run; test/parse_test.sh tests it.
# Every function reads a file (or stdin with "-") once and prints plain text.

# results SERIAL_LOG: the guest's results, from between its two markers, with
# the serial console's carriage returns taken out. Nothing when the guest
# never got to the end marker.
results() {
	[ -r "$1" ] || return 1
	tr -d '\r' < "$1" | awk '
		/^CHVGPU-BEGIN$/ { on = 1; buf = ""; next }
		/^CHVGPU-END$/   { if (on) { printf "%s", buf; done = 1 } on = 0; next }
		on               { buf = buf $0 "\n" }
		END              { exit !done }'
}

# section RESULTS NAME: one command's output, as the guest script framed it
# ("### section NAME" ... "### end NAME rc=N seconds=N").
section() {
	awk -v name="$2" '
		$0 == "### section " name { on = 1; next }
		on && index($0, "### end " name " ") == 1 { exit }
		on { print }' "$1"
}

# section_rc RESULTS NAME: the command's exit status, or "missing".
section_rc() {
	local rc
	rc=$(awk -v name="$2" 'index($0, "### end " name " rc=") == 1 {
		sub(/.* rc=/, ""); sub(/ .*/, ""); print; exit }' "$1")
	echo "${rc:-missing}"
}

# kv FILE KEY: the value after "KEY:" on the first line that starts with it
# (leading blanks ignored), trimmed.
kv() {
	awk -v key="$2" '{
		line = $0; sub(/^[ \t]+/, "", line)
		if (index(line, key ":") == 1) {
			v = substr(line, length(key) + 2); gsub(/^[ \t]+|[ \t]+$/, "", v); print v; exit
		}
	}' "$1"
}

# glxinfo FILE: "renderer|version|accelerated" from `glxinfo -B`. The version
# is the core profile's when there is one (the compatibility one is often
# lower), else the plain "OpenGL version string".
glxinfo_summary() {
	local in renderer version accel
	in=$(cat -- "$1")
	renderer=$(kv - "OpenGL renderer string" <<<"$in")
	version=$(kv - "OpenGL core profile version string" <<<"$in")
	[ -n "$version" ] || version=$(kv - "OpenGL version string" <<<"$in")
	accel=$(kv - "Accelerated" <<<"$in")
	echo "${renderer}|${version}|${accel}"
}

# eglinfo FILE: "renderer|version" of the first OpenGL (or else GLES) profile
# that `eglinfo -B` lists.
eglinfo_summary() {
	local in renderer version
	in=$(cat -- "$1")
	renderer=$(kv - "OpenGL core profile renderer" <<<"$in")
	version=$(kv - "OpenGL core profile version" <<<"$in")
	if [ -z "$renderer" ]; then
		renderer=$(kv - "OpenGL ES profile renderer" <<<"$in")
		version=$(kv - "OpenGL ES profile version" <<<"$in")
	fi
	echo "${renderer}|${version}"
}

# vulkan_devices FILE: one line per device in `vulkaninfo --summary`,
# "GPUn|deviceType|deviceName|driverName|apiVersion|driverInfo", with the
# type shortened (PHYSICAL_DEVICE_TYPE_DISCRETE_GPU -> discrete_gpu).
vulkan_devices() {
	awk '
		function flush() {
			if (gpu != "") print gpu "|" f["deviceType"] "|" f["deviceName"] "|" f["driverName"] "|" f["apiVersion"] "|" f["driverInfo"]
			gpu = ""; split("", f)
		}
		/^GPU[0-9]+:[ \t]*$/ { flush(); gpu = $0; sub(/:.*/, "", gpu); next }
		gpu != "" && /^[ \t]+[A-Za-z]+[ \t]*=/ {
			k = $0; sub(/^[ \t]+/, "", k); sub(/[ \t]*=.*/, "", k)
			v = $0; sub(/^[^=]*=[ \t]*/, "", v); sub(/[ \t]+$/, "", v)
			if (k == "deviceType") { sub(/^PHYSICAL_DEVICE_TYPE_/, "", v); v = tolower(v) }
			f[k] = v; next
		}
		gpu != "" && /^[^ \t]/ { flush() }
		END { flush() }' "$1"
}

# vulkan_best FILE: the first device that isn't the CPU (lavapipe), or the
# first device when that's all there is.
vulkan_best() {
	local devices
	devices=$(vulkan_devices "$1")
	[ -n "$devices" ] || return 0
	{ grep -v '^[^|]*|cpu|' <<<"$devices"; echo "$devices"; } | grep -m1 . || true
}

# score FILE TOOL: the score glmark2 or vkmark printed at the end
# ("glmark2 Score: 4381"), or nothing.
score() {
	sed -n "s/.*$2 Score: *\([0-9][0-9]*\).*/\1/p" "$1" | tail -n1
}

# bench_renderer FILE: what glmark2 (GL_RENDERER) or vkmark (Device Name) ran on.
bench_renderer() {
	local in r
	in=$(cat -- "$1")
	r=$(kv - "GL_RENDERER" <<<"$in")
	[ -n "$r" ] || r=$(kv - "Device Name" <<<"$in")
	echo "$r"
}

# drm_features FILE: the virtio-gpu features the guest's kernel found, from
# its "[drm] features: +virgl +edid ..." lines (it prints them over two).
drm_features() {
	sed -n 's/.*\[drm\] features: *//p' "$1" | tr -s ' \t\n' ' ' | sed 's/^ //; s/ $//'
}

# chv_failure LOG: why Cloud Hypervisor stopped, from its log: the first
# panic, with its message (Rust prints it on the next line), since what
# follows a first panic (a PoisonError, an abort) is its consequence; else the
# chain of errors it exited with, joined; else the log's last lines.
chv_failure() {
	local in
	[ -r "$1" ] || return 0
	in=$(tr -d '\r' < "$1")
	awk '
		/^thread .* panicked at / {
			line = $0
			if (line ~ /:$/ && (getline next_line) > 0) line = line " " next_line
			print line; found = 1; exit
		}
		/^Error: .*exited with/ { chain = 1; out = ""; next }
		chain && /^ *[0-9]+: / { sub(/^ *[0-9]+: /, ""); out = out (out == "" ? "" : " / ") $0; next }
		chain { chain = 0 }
		END { if (!found && out != "") print out }' <<<"$in" | grep . ||
		tail -n 3 <<<"$in" | tr '\n' ' ' | sed 's/ *$//'
}

# host_gpu RENDERER: the host GPU behind a guest's renderer string, as
# "name/codename" in lower case, or nothing for one that isn't virgl or Venus.
# "virgl (AMD Radeon RX 6750 XT (radeonsi, navi22, LLVM 19.1.7, DRM 3.61, ...))"
# and "Virtio-GPU Venus (AMD Radeon RX 6750 XT (RADV NAVI22))" are both
# "amd radeon rx 6750 xt/navi22"; the codename is AMD's (radeonsi, RADV) only.
host_gpu() {
	local inner name code=''
	case "$1" in
	"virgl ("*")") inner=${1#virgl (} ;;
	"Virtio-GPU Venus ("*")") inner=${1#Virtio-GPU Venus (} ;;
	*) return 0 ;;
	esac
	inner=${inner%)}
	name=${inner%% (*}
	name=${name#Mesa }
	case "$inner" in
	*"(radeonsi, "*) code=${inner#*(radeonsi, }; code=${code%%[,)]*} ;;
	*"(RADV "*) code=${inner#*(RADV }; code=${code%%)*} ;;
	esac
	printf '%s/%s\n' "${name,,}" "${code,,}"
}

# same_gpu A B: whether two host_gpu values are the same GPU: the same name,
# and the same codename when both have one.
same_gpu() {
	[ "${1%%/*}" = "${2%%/*}" ] || return 1
	[ -z "${1#*/}" ] || [ -z "${2#*/}" ] || [ "${1#*/}" = "${2#*/}" ]
}

# pci_ids VENDOR DEVICE: "vvvv:dddd", lower case, from hex with or without 0x.
pci_ids() {
	printf '%04x:%04x\n' "0x${1#0x}" "0x${2#0x}" 2>/dev/null
}

# vulkan_ids FILE: "vendor:device" of the GPU a Vulkan run used: vkmark's
# "Vendor ID"/"Device ID", else the first non-CPU device of vulkaninfo
# --summary. Nothing when neither says.
vulkan_ids() {
	local in v d
	in=$(cat -- "$1")
	v=$(kv - "Vendor ID" <<<"$in")
	d=$(kv - "Device ID" <<<"$in")
	if [ -z "$v" ] || [ -z "$d" ]; then
		read -r v d < <(awk '
			/^GPU[0-9]+:/ { if (v != "" && t != "PHYSICAL_DEVICE_TYPE_CPU") { print v, d; done = 1; exit } v = d = t = "" }
			/^[ \t]+vendorID[ \t]*=/ { v = $NF }
			/^[ \t]+deviceID[ \t]*=/ { d = $NF }
			/^[ \t]+deviceType[ \t]*=/ { t = $NF }
			END { if (!done && v != "" && t != "PHYSICAL_DEVICE_TYPE_CPU") print v, d }' <<<"$in")
	fi
	[ -n "$v" ] && [ -n "$d" ] && pci_ids "$v" "$d"
	return 0
}

# node_of IDS NODES: the render node (from NODES, "node|ids" lines) whose GPU
# is IDS, or nothing.
node_of() {
	awk -F'|' -v ids="$1" '$2 == ids { print $1; exit }' <<<"$2"
}

# ratio A B: A/B to one decimal, or "?" when either is missing or B is 0.
ratio() {
	awk -v a="$1" -v b="$2" 'BEGIN {
		if (a !~ /^[0-9]+$/ || b !~ /^[0-9]+$/ || b == 0) { print "?"; exit }
		printf "%.1f\n", a / b }'
}

# is_software RENDERER: whether a GL renderer or Vulkan device is Mesa's
# software one (llvmpipe, softpipe, lavapipe), i.e. no GPU was used.
is_software() {
	case "$1" in
	*llvmpipe* | *softpipe* | *lavapipe* | *"SwiftShader"*) return 0 ;;
	*) return 1 ;;
	esac
}

# mesa_version TEXT: the Mesa version in a glxinfo/vulkaninfo string
# ("4.6 (Core Profile) Mesa 24.0.5-1ubuntu1" -> 24.0.5-1ubuntu1).
mesa_version() {
	sed -n 's/.*Mesa \([0-9][^ )]*\).*/\1/p' <<<"$1" | head -n1
}
