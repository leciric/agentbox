#!/bin/bash
# GPU for agents in a krunkit VM on a Mac (gpu.go), run as root by the daemon
# in the VM when "GPU for agents" is on (agent.ReadyVenus), with kernel.sh and
# the patches beside it; safe to run again, and quick when there is nothing
# to do.
#
# krunkit always gives the VM a virtio-gpu that speaks Venus, Vulkan forwarded
# to the Mac and run there on Metal by MoltenVK. Two things in the VM stand
# between that and an agent:
#
#   - The kernel. Lima's Debian image boots Debian's cloud kernel, which is
#     built without DRM at all, so it has no virtio-gpu driver and no render
#     node. Debian's generic kernel has it as a module: kernel.sh installs it
#     and boots it once, keeping the cloud kernel to fall back to.
#   - Mesa's Venus driver as Debian builds it: macOS pages are 16 KiB, and
#     Venus maps what the guest allocates with the guest's 4 KiB alignment,
#     which the Mac can't map. libkrun's maintainer carries the fix as a patch
#     on Fedora's Mesa (copr slp/mesa-libkrun-vulkan); it isn't upstream.
#     This builds that driver alone, from the same Mesa as Debian 13's, with
#     the same patches, and agents get it in place of their own (package
#     agent, vgpudriver.go). Our own patch (0004) lets it present and run
#     ANGLE without dma-buf, which the Mac doesn't have: that's what gives
#     Chromium's WebGL the GPU (package agent's venus-chromium.sh), and 0005
#     keeps WebGL's timer queries from hanging it. Mesa's Zink runs OpenGL on it.
#
set -eu -o pipefail
export DEBIAN_FRONTEND=noninteractive

patches=$(cd "$(dirname "$0")" && pwd)
mesa_version=25.0.7
mesa_sha256=592272df3cf01e85e7db300c449df5061092574d099da275d19e97ef0510f8a6
# Where the driver goes, which agents mount theirs from: AGENTBOX_VENUS_DIR in
# tests, which build it in a container.
dest=${AGENTBOX_VENUS_DIR:-/usr/local/lib/agentbox/venus}
log=/var/log/agentbox-venus.log
# Debian's multiarch directory, aarch64-linux-gnu on a Mac.
multiarch=$(uname -m)-linux-gnu

say() { echo "==> $*"; }

# One at a time: a daemon that stopped leaves its run to finish, which the
# next one's waits for.
exec 9>/run/agentbox-venus.lock
flock 9

# The kernel comes first: kernel.sh says when it fails, and leaves
# /run/agentbox-gpu-restart when the VM has to restart for it.
if [[ ${AGENTBOX_VENUS_KERNEL:-1} == 1 ]]; then
  bash "$patches/kernel.sh" try
fi
modprobe virtio_gpu 2>/dev/null || true

stamp="$mesa_version $(cat "$patches"/*.patch | sha256sum | cut -d' ' -f1)"
if [[ $(cat "$dest/stamp" 2>/dev/null) != "$stamp" ]]; then
  say "GPU: building Mesa $mesa_version's Venus driver with the patches macOS needs (once, a few minutes; log in $log)"
  build=(curl ca-certificates xz-utils patch gcc g++ pkg-config meson ninja-build python3-mako python3-yaml
    python3-packaging glslang-tools bison flex libdrm-dev libexpat1-dev zlib1g-dev libzstd-dev
    libx11-dev libx11-xcb-dev libxext-dev libxfixes-dev libxrandr-dev libxxf86vm-dev libxshmfence-dev
    libxcb1-dev libxcb-dri3-dev libxcb-present-dev libxcb-randr0-dev libxcb-shm0-dev libxcb-sync-dev
    libxcb-xfixes0-dev)
  # What's here already stays: only what this installs comes off again.
  mapfile -t added < <(for p in "${build[@]}"; do dpkg-query -W -f '${db:Status-Status}\n' "$p" 2>/dev/null | grep -qx installed || echo "$p"; done)
  apt-get update -q >>"$log"
  apt-get install -y -q --no-install-recommends "${build[@]}" libvulkan1 vulkan-tools mesa-utils >>"$log"
  # Mesa links xcb-keysyms when it finds it, and agents don't have it, so
  # their Vulkan loader would drop the driver: a failed build of a version
  # that installed it left it behind.
  apt-get purge -y -q libxcb-keysyms1-dev >>"$log" 2>&1 || true
  src=$(mktemp -d /var/tmp/agentbox-venus.XXXXXX)
  trap 'rm -rf "$src"' EXIT
  curl -fsSL --retry 3 -o "$src/mesa.tar.xz" "https://archive.mesa3d.org/mesa-$mesa_version.tar.xz"
  echo "$mesa_sha256  $src/mesa.tar.xz" | sha256sum -c --quiet
  tar -xJf "$src/mesa.tar.xz" -C "$src"
  for p in "$patches"/*.patch; do
    patch -d "$src/mesa-$mesa_version" -p1 --forward --quiet <"$p"
  done
  if ! meson setup "$src/build" "$src/mesa-$mesa_version" --buildtype=release -Db_ndebug=true \
    -Dvulkan-drivers=virtio -Dgallium-drivers= -Dplatforms=x11 -Dopengl=false -Dglx=disabled -Degl=disabled \
    -Dgbm=disabled -Dgles1=disabled -Dgles2=disabled -Dllvm=disabled -Dvalgrind=disabled -Dlibunwind=disabled \
    -Dlmsensors=disabled -Dbuild-tests=false >>"$log" 2>&1 || ! ninja -C "$src/build" >>"$log" 2>&1; then
    tail -n 30 "$log" >&2
    echo "GPU: building the Venus driver failed: all of it is in $log" >&2
    exit 1
  fi
  mkdir -p "$dest"
  install -m 0644 "$src/build/src/virtio/vulkan/libvulkan_virtio.so" "$dest/libvulkan_virtio.so.new"
  mv "$dest/libvulkan_virtio.so.new" "$dest/libvulkan_virtio.so"
  # The VM's own Vulkan finds it here, for vulkaninfo; agents have their own
  # ICD file, Mesa's, which names the path theirs is mounted at.
  mkdir -p /etc/vulkan/icd.d
  sed -E "s|\"library_path\": *\"[^\"]*\"|\"library_path\": \"$dest/libvulkan_virtio.so\"|" \
    "$src"/build/src/virtio/vulkan/virtio_icd.*.json >/etc/vulkan/icd.d/agentbox_venus_icd.json
  # The libraries it loads are kept when the build's packages go.
  mapfile -t keep < <(ldd "$dest/libvulkan_virtio.so" | awk '$3 ~ /^\// { print $3 }' | xargs -r realpath |
    xargs -r dpkg -S 2>/dev/null | cut -d: -f1 | sort -u)
  ((${#keep[@]})) && apt-mark manual "${keep[@]}" >/dev/null
  ((${#added[@]})) && apt-get purge -y -q --autoremove "${added[@]}" >>"$log"
  echo "$stamp" >"$dest/stamp"
  echo "$multiarch" >"$dest/multiarch"
fi
