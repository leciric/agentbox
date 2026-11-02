#!/bin/sh
# Installs AgentBox on Linux:
#
#   curl -fsSL https://downloads.agentbox.linting.dev/install.sh | sh
#
# It checks this machine first and downloads nothing until every check
# passes: x86_64, /dev/kvm, 8 GB of memory, 30 GB free in your home folder,
# and FUSE when it would install the AppImage. Then it downloads the latest
# release, checks it against the release's SHA256SUMS, and installs the .deb
# (Debian, Ubuntu and their kin, with apt-get), the .pacman (Arch, with
# pacman), or else the AppImage, in ~/.local/bin. Only the .deb and the
# .pacman need sudo, and it says so before asking.
#
#   sh install.sh --check      only the checks
#   sh install.sh --appimage   the AppImage, whatever the distribution
#
# AGENTBOX_CHANNEL=nightly installs the newest nightly instead of the newest
# release, and DOWNLOADS_URL points it at another copy of the downloads.
#
# All of it is one function, called on the last line: piped into sh, a
# download cut off half-way runs nothing.
set -eu

main() {

downloads=${DOWNLOADS_URL:-https://downloads.agentbox.linting.dev}
downloads=${downloads%/}
channel=${AGENTBOX_CHANNEL:-stable}
# Where /proc, /dev and /etc are: only the tests change it.
root=${AGENTBOX_INSTALL_ROOT:-}

check_only=no
force_appimage=no
for arg in "$@"; do
  case $arg in
    --check) check_only=yes ;;
    --appimage) force_appimage=yes ;;
    -h | --help)
      sed -n '2,18p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "install.sh: unknown option $arg (try --check or --appimage)" >&2
      exit 2
      ;;
  esac
done

say() { printf '%s\n' "$*"; }
problems=""
notes=""
problem() { problems="$problems
  - $*"; }
note() { notes="$notes
  - $*"; }

# --- What to install --------------------------------------------------------

os=$(uname -s)
if [ "$os" = Darwin ]; then
  say "This script installs AgentBox on Linux. On a Mac, download the app (the .dmg) from"
  say "  $downloads/index.html"
  exit 1
fi
if [ "$os" != Linux ]; then
  say "This script installs AgentBox on Linux, and this is $os. Downloads for every system:"
  say "  $downloads/index.html"
  exit 1
fi

arch=$(uname -m)
case $arch in
  x86_64 | amd64) ;;
  aarch64 | arm64)
    problem "This is an ARM (arm64) machine. AgentBox's app and its VM run only on x86_64 Linux for now."
    ;;
  *)
    problem "This machine's processor is $arch. AgentBox runs only on x86_64 Linux."
    ;;
esac

# The distribution, from os-release: ID and ID_LIKE.
distro=""
if [ -r "$root/etc/os-release" ]; then
  distro=$(
    # shellcheck disable=SC1091
    . "$root/etc/os-release" >/dev/null 2>&1 || true
    printf '%s %s' "${ID:-}" "${ID_LIKE:-}"
  )
fi
kind=appimage
if [ "$force_appimage" = no ]; then
  case " $distro " in
    *" debian "* | *" ubuntu "*) command -v apt-get >/dev/null 2>&1 && kind=deb ;;
    *" arch "*) command -v pacman >/dev/null 2>&1 && kind=pacman ;;
  esac
fi

# --- The checks -------------------------------------------------------------

# KVM: AgentBox runs its agents in a VM, which needs the processor's
# virtualization. A /dev/kvm this user can't open is fine: the app's Setup
# gives access with one password prompt.
# No /dev/kvm on a processor that shows vmx (VT-x) or svm (AMD-V) is KVM's
# module not loaded, which Setup does too; without either, the firmware has
# it off (Linux 5.6 and later hide vmx then).
if [ ! -e "$root/dev/kvm" ] && grep -qw -e vmx -e svm "$root/proc/cpuinfo" 2>/dev/null; then
  note "There's no /dev/kvm yet: KVM's module isn't loaded. That's fine: AgentBox's Setup loads it, and gives you access, with one password prompt."
elif [ ! -e "$root/dev/kvm" ]; then
  problem "There is no /dev/kvm: this machine's virtualization is off. Turn on Intel VT-x or AMD-V (often called SVM or Virtualization Technology) in the firmware (BIOS/UEFI) settings; in a virtual machine, turn on nested virtualization."
elif [ ! -r "$root/dev/kvm" ] || [ ! -w "$root/dev/kvm" ]; then
  note "You can't open /dev/kvm yet. That's fine: AgentBox's Setup gives you access with one password prompt, with no need to log in again."
fi

# Memory: 8 GB, a little under, since 8 GB machines report less.
mem_kb=$(awk '/^MemTotal:/ { print $2; exit }' "$root/proc/meminfo" 2>/dev/null || true)
if [ -n "$mem_kb" ] && [ "$mem_kb" -lt 7500000 ]; then
  problem "This machine has $((mem_kb / 1024 / 1024)) GB of memory, and AgentBox needs at least 8 GB: its VM, and the agents in it, take their memory from it."
fi

# Disk: 30 GB free in the home folder, where the VM's disk and the agents'
# machines go.
home=${HOME:-/}
free_kb=$(df -Pk "$home" 2>/dev/null | awk 'NR == 2 { print $4 }' || true)
if [ -n "$free_kb" ] && [ "$free_kb" -lt $((30 * 1000 * 1000)) ]; then
  problem "Your home folder ($home) has $((free_kb / 1000 / 1000)) GB free, and AgentBox needs at least 30 GB: its VM's disk, and every agent's machine, go there."
fi

# FUSE: the AppImage mounts itself with it, through libfuse2.
if [ "$kind" = appimage ]; then
  if [ ! -e "$root/dev/fuse" ]; then
    problem "There is no /dev/fuse, which the AppImage needs to run. Load the fuse module (sudo modprobe fuse) or install your distribution's fuse package."
  else
    libfuse=no
    for f in "$root"/usr/lib*/libfuse.so.2* "$root"/usr/lib*/*/libfuse.so.2* "$root"/lib*/libfuse.so.2* "$root"/lib*/*/libfuse.so.2*; do
      [ -e "$f" ] && libfuse=yes && break
    done
    [ "$libfuse" = yes ] || problem "libfuse2 isn't installed, and the AppImage needs it to run: install it with your package manager (fuse-libs on Fedora, fuse2 on Arch, libfuse2 or libfuse2t64 on Debian and Ubuntu)."
  fi
fi

if [ -n "$notes" ]; then
  say "Note:$notes"
fi
if [ -n "$problems" ]; then
  say "AgentBox can't be installed on this machine yet:$problems"
  exit 1
fi
case $kind in
  deb) what="the .deb, with apt-get" ;;
  pacman) what="the .pacman, with pacman" ;;
  *) what="the AppImage, in ~/.local/bin" ;;
esac
say "This machine can run AgentBox: it would install $what."
if [ "$check_only" = yes ]; then
  exit 0
fi

# --- Download and check -----------------------------------------------------

fetch() { # url file
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    say "Neither curl nor wget is installed: install one, then run this again." >&2
    exit 1
  fi
}

tmp=$(mktemp -d)
# apt-get reads the .deb as its own unprivileged user.
chmod 755 "$tmp"
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch "$downloads/releases.json" "$tmp/releases.json"
# releases.json is newest first, in GitHub's shape, one field a line. A
# nightly is a prerelease whose tag says nightly.
tag=$(awk -v want="$channel" '
  /"tag_name"/ { gsub(/.*"tag_name": *"|".*/, ""); t = $0 }
  /"prerelease"/ {
    pre = ($0 ~ /true/)
    nightly = pre && t ~ /-nightly\./
    if (t != "" && (want == "nightly" ? nightly : !pre)) { print t; exit }
    t = ""
  }' "$tmp/releases.json")
if [ -z "$tag" ]; then
  say "Couldn't find a $channel release in $downloads/releases.json." >&2
  exit 1
fi
version=${tag#v}
case $kind in
  deb) asset="AgentBox-$version-amd64.deb" ;;
  pacman) asset="AgentBox-$version-x64.pacman" ;;
  *) asset="AgentBox-$version-x86_64.AppImage" ;;
esac

say "==> Downloading AgentBox $version ($asset)"
fetch "$downloads/releases/$tag/SHA256SUMS" "$tmp/SHA256SUMS"
fetch "$downloads/releases/$tag/$asset" "$tmp/$asset"
want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  say "The download doesn't match the release's SHA256SUMS (want ${want:-no line for $asset}, got $got): nothing was installed." >&2
  exit 1
fi
say "==> Checked against the release's SHA256SUMS"

# --- Install ----------------------------------------------------------------

as_root() {
  if [ "$(id -u)" = 0 ]; then
    "$@"
  else
    sudo "$@"
  fi
}

case $kind in
  deb)
    say "==> Installing it with apt-get, which needs your password for sudo"
    as_root apt-get install -y "$tmp/$asset"
    ;;
  pacman)
    say "==> Installing it with pacman, which needs your password for sudo"
    as_root pacman -U --noconfirm "$tmp/$asset"
    ;;
  *)
    bin="$home/.local/bin"
    mkdir -p "$bin"
    mv "$tmp/$asset" "$bin/AgentBox.AppImage"
    chmod +x "$bin/AgentBox.AppImage"
    apps="${XDG_DATA_HOME:-$home/.local/share}/applications"
    mkdir -p "$apps"
    cat >"$apps/agentbox.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=AgentBox
Comment=Run AI coding agents, each in a machine of its own
Exec=$bin/AgentBox.AppImage %U
Terminal=false
Categories=Development;
EOF
    say "==> Installed $bin/AgentBox.AppImage, with AgentBox in your applications menu"
    ;;
esac

say "AgentBox $version is installed. Open AgentBox: its Setup makes the VM your agents run in."
}

main "$@"
