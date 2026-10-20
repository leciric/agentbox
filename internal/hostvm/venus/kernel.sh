#!/bin/bash
# The kernel half of GPU for agents in a krunkit VM on a Mac (venus.sh), run
# as root. Debian's cloud kernel has no DRM, so no virtio-gpu driver; the
# generic kernel has it. A kernel that doesn't boot leaves a VM with nothing
# to boot, so the cloud kernel stays until the generic one has booted once:
#
#   kernel.sh try   (venus.sh) installs the generic kernel, keeps GRUB's
#                   default on the running cloud kernel (GRUB_DEFAULT=saved)
#                   and boots the generic one next time only (grub-reboot).
#                   Leaves $state saying "trying".
#   kernel.sh boot  (agentbox-gpu-kernel.service, at every boot while $state
#                   is there) on the generic kernel, makes it the default and
#                   purges the cloud kernel; back on the cloud kernel, GRUB
#                   fell back to it, so it writes "failed" and nothing is
#                   tried again until AGENTBOX_VENUS_RETRY=1 (turning "GPU for
#                   agents" off and on).
#
# AGENTBOX_VENUS_ROOT puts every file it reads or writes under another
# directory, for tests, which also stand in for the commands it runs.
set -eu -o pipefail
export DEBIAN_FRONTEND=noninteractive

root=${AGENTBOX_VENUS_ROOT:-}
state=$root/var/lib/agentbox/gpu-kernel
grubcfg=$root/etc/default/grub.d/agentbox-gpu.cfg
grubmenu=$root/boot/grub/grub.cfg
unit=$root/etc/systemd/system/agentbox-gpu-kernel.service
restart=$root/run/agentbox-gpu-restart
log=$root/var/log/agentbox-venus.log
here=$(cd "$(dirname "$0")" && pwd)
arch=$(dpkg --print-architecture)
running=$(uname -r)
apt=(apt-get -y -q -o DPkg::Lock::Timeout=300)

say() { echo "==> $*"; }

# has_driver says whether kernel version $1 has the virtio-gpu driver.
has_driver() { grep -qs '^CONFIG_DRM_VIRTIO_GPU=[ym]' "$root/boot/config-$1"; }

# entry is GRUB's menu entry id for kernel version $1, "" when it has none.
# GRUB_DISABLE_SUBMENU puts every kernel's entry at the top level, where
# grub-reboot and grub-set-default take its id alone.
entry() {
  awk -v id="'gnulinux-$1-advanced-" '{
    i = index($0, id); if (!i) next
    s = substr($0, i + 1); print substr(s, 1, index(s, "\047") - 1); exit
  }' "$grubmenu" 2>/dev/null
}

# cloud_kernels are the cloud kernel's packages that are installed.
cloud_kernels() {
  dpkg-query -W -f '${db:Status-Status} ${Package}\n' "linux-image-cloud-$arch" "linux-image-*-cloud-$arch" 2>/dev/null |
    awk '$1 == "installed" { print $2 }'
}

try() {
  if has_driver "$running"; then
    return 0
  fi
  case $(cat "$state" 2>/dev/null) in
  trying*)
    say "GPU: the VM boots the kernel with the GPU driver when it restarts"
    touch "$restart"
    return 0
    ;;
  failed*)
    if [[ ${AGENTBOX_VENUS_RETRY:-} != 1 ]]; then
      echo "GPU: Debian's generic kernel ($(cut -d' ' -f2 "$state")) didn't boot, so the VM went back to its cloud kernel; turn \"GPU for agents\" off and on to try again" >&2
      exit 3
    fi
    rm -f "$state"
    ;;
  esac
  if ! command -v grub-reboot >/dev/null || [[ ! -e $grubmenu ]]; then
    echo "GPU: this VM doesn't boot through GRUB, so its kernel can't be tried once with a way back: it keeps the kernel it has, which has no GPU driver" >&2
    exit 1
  fi
  say "GPU: Debian's generic kernel, which has the virtio-gpu driver the cloud kernel lacks"
  # The running kernel is pinned as GRUB's default before another is
  # installed: a newer generic kernel would otherwise come first.
  mkdir -p "$(dirname "$grubcfg")"
  printf '# agentbox: the cloud kernel stays the default until the generic one has booted (kernel.sh)\nGRUB_DEFAULT=saved\nGRUB_DISABLE_SUBMENU=y\n' >"$grubcfg"
  update-grub >>"$log" 2>&1
  local cloud generic
  cloud=$(entry "$running")
  if [[ -z $cloud ]]; then
    rm -f "$grubcfg"
    update-grub >>"$log" 2>&1
    echo "GPU: GRUB has no entry for the running kernel ($running), so it can't be kept as the way back: the VM keeps it, with no GPU driver" >&2
    exit 1
  fi
  grub-set-default "$cloud"
  "${apt[@]}" update >>"$log"
  "${apt[@]}" install "linux-image-$arch" >>"$log"
  generic=$(for k in "$root"/boot/vmlinuz-*-"$arch"; do
    if [[ -e $k && $k != *-cloud-"$arch" ]]; then echo "${k##*/vmlinuz-}"; fi
  done | sort -V | tail -n1)
  if [[ -z $generic ]] || ! has_driver "$generic" || [[ -z $(entry "$generic") ]]; then
    echo "GPU: Debian's generic kernel ($generic) installed, but GRUB has no entry for it with the virtio-gpu driver: the VM keeps booting $running" >&2
    exit 1
  fi
  install -m 0644 /dev/stdin "$unit" <<EOF
[Unit]
Description=AgentBox: keep the kernel with the GPU driver once it has booted
ConditionPathExists=/var/lib/agentbox/gpu-kernel
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/bin/bash $here/kernel.sh boot

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable agentbox-gpu-kernel.service >>"$log" 2>&1
  # The state comes last: without it the unit does nothing, and the next
  # boot that isn't the generic kernel's is the cloud kernel's again.
  grub-reboot "$(entry "$generic")"
  mkdir -p "$(dirname "$state")"
  echo "trying $generic" >"$state"
  touch "$restart"
}

boot() {
  local was
  was=$(cat "$state" 2>/dev/null) || return 0
  if ! has_driver "$running"; then
    case $was in
    trying*)
      # GRUB clears next_entry as it boots it: still set, this boot
      # wasn't GRUB's (or it didn't get that far), and the try is still to come.
      if grub-editenv - list 2>/dev/null | grep -q '^next_entry=.'; then
        return 0
      fi
      echo "failed ${was#trying }" >"$state"
      echo "GPU: Debian's generic kernel didn't boot, so the VM went back to its cloud kernel" >&2
      ;;
    esac
    return 0
  fi
  # The generic kernel booted: it's the default now, and the cloud kernel,
  # which would come first again otherwise, goes.
  grub-set-default "$(entry "$running")"
  echo "booted $running" >"$state"
  mapfile -t cloud < <(cloud_kernels)
  if ((${#cloud[@]})); then
    "${apt[@]}" purge "${cloud[@]}" >>"$log"
  fi
  rm -f "$grubcfg"
  update-grub >>"$log" 2>&1
  grub-editenv - unset saved_entry next_entry
  rm -f "$state"
}

case ${1:-} in
try) try ;;
boot) boot ;;
*)
  echo "usage: kernel.sh try|boot" >&2
  exit 2
  ;;
esac
