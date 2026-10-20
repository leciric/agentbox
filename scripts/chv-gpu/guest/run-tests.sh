#!/bin/bash
# Runs in the guest, as root, from cloud-init (try.sh puts it there): what
# OpenGL and Vulkan run on with the virtio GPU, then the same in software,
# framed into sections for lib/parse.sh and printed on the serial console
# between CHVGPU-BEGIN and CHVGPU-END. Then it powers the guest off.
set -u
out=/var/log/chv-gpu-results.txt
VK_DEVICE=''
# shellcheck disable=SC1091 # written by try.sh's cloud-init
. /etc/chv-gpu.conf 2>/dev/null
: > "$out"

# run NAME SECONDS COMMAND...: one framed section of the results.
run() {
	local name=$1 limit=$2 start rc
	shift 2
	echo "### section $name" >> "$out"
	start=$(date +%s)
	if declare -F "$1" >/dev/null; then
		"$@" >> "$out" 2>&1
	else
		timeout -k 10 "$limit" "$@" >> "$out" 2>&1
	fi
	rc=$?
	echo "### end $name rc=$rc seconds=$(($(date +%s) - start))" >> "$out"
}

guest_info() {
	. /etc/os-release
	echo "os: $PRETTY_NAME"
	echo "kernel: $(uname -r)"
	echo "mesa: $(dpkg-query -W -f '${Version}' libgl1-mesa-dri 2>/dev/null)"
	echo "cpus: $(nproc)"
	echo "vulkan device asked for: ${VK_DEVICE:-none}"
	echo "devices:"
	lspci -nn | grep -iE 'vga|display|3d' | sed 's/^/  /'
	ls /dev/dri | sed 's/^/  \/dev\/dri\//'
	echo "kernel log:"
	dmesg | grep -iE 'virtio[_-]gpu|\[drm\]' | sed 's/^/  /'
}
run guest 30 guest_info

# glxinfo and glmark2 need an X server: Xorg's modesetting driver on the
# virtio GPU's scanout (crosvm's display stub), with glamor on virgl.
mkdir -p /run/user/0 && chmod 700 /run/user/0
export XDG_RUNTIME_DIR=/run/user/0 DISPLAY=:0
Xorg :0 -nolisten tcp -noreset vt7 > /var/log/chv-gpu-xorg.out 2>&1 &
for _ in $(seq 40); do [ -S /tmp/.X11-unix/X0 ] && break; sleep 0.5; done
[ -S /tmp/.X11-unix/X0 ] || run xorg 5 tail -n 30 /var/log/Xorg.0.log

glmark=(--off-screen -s 1280x720
	-b build:use-vbo=true:duration=3 -b texture:texture-filter=linear:duration=3
	-b shading:shading=phong:duration=3 -b bump:bump-render=normals:duration=3
	-b effect2d:duration=3 -b refract:duration=3 -b terrain:duration=3)
vkmark=(--winsys headless -s 1280x720
	-b vertex:duration=3 -b texture:duration=3 -b shading:duration=3
	-b cube:duration=3 -b effect2d:duration=3 -b desktop:duration=3)
icd=/usr/share/vulkan/icd.d

# The GPU: Mesa's virgl for GL and only Venus (virtio_icd) for Vulkan, so a
# Venus that doesn't work fails rather than quietly falling back to lavapipe.
(
	export VK_DRIVER_FILES=$icd/virtio_icd.json VK_ICD_FILENAMES=$icd/virtio_icd.json
	# Venus offers every host GPU: the one try.sh chose goes first, which is
	# the one vkmark and vulkaninfo's first device are (Mesa's device-select
	# layer, here in the guest; it only reorders).
	[ -n "$VK_DEVICE" ] && export MESA_VK_DEVICE_SELECT=$VK_DEVICE
	run glxinfo-gpu 60 glxinfo -B
	run eglinfo-gpu 60 eglinfo -B -p surfaceless
	run vulkaninfo-gpu 60 vulkaninfo --summary
	run glmark2-gpu 300 glmark2 "${glmark[@]}"
	run vkmark-gpu 300 vkmark "${vkmark[@]}"
)
# Software: llvmpipe and lavapipe on the guest's CPUs.
(
	export LIBGL_ALWAYS_SOFTWARE=1 VK_DRIVER_FILES=$icd/lvp_icd.json VK_ICD_FILENAMES=$icd/lvp_icd.json
	run glxinfo-sw 60 glxinfo -B
	run vulkaninfo-sw 60 vulkaninfo --summary
	run glmark2-sw 300 glmark2 "${glmark[@]}"
	run vkmark-sw 300 vkmark "${vkmark[@]}"
)
run dmesg-tail 10 sh -c "dmesg | grep -iE 'virtio|drm|gpu|venus|fault' | tail -n 30"

# Quiet the kernel on the console so nothing lands inside the results.
dmesg -n 1
{
	echo CHVGPU-BEGIN
	cat "$out"
	echo CHVGPU-END
} > /dev/ttyS0
sync
systemctl poweroff
