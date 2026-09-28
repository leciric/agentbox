#!/usr/bin/env bash
# The system half of an AgentBox machine: Debian's packages, Docker, the
# browser and media tools, Mesa, the optional components' software, mise, the
# user's groups and sudo, and the machine-wide settings every agent relies on.
# Runs as root. provision.sh runs it on a new build, once the user exists, and
# a project base's refresh runs it on a machine copied from that base
# (image.CatchUp), so a base saved before a change here gets it too.
#
# So everything here has to be safe to run again, over a machine an agent has
# already used for weeks: installing a package that is there is a no-op, and a
# file is written whole or only added to once. What isn't safe to run twice —
# making the user, the agent tools' own config, filling caches — belongs in
# provision.sh instead. A change here still means bumping image.Version, and
# saying what changed in image.Changes: that is what a base's card shows.
#
# Usage: system.sh <user>
#
# It reads the same AGENTBOX_WITH_* variables provision.sh does.
set -euo pipefail
USER_NAME=$1
WITH_ANDROID=${AGENTBOX_WITH_ANDROID:-0} WITH_INCUS=${AGENTBOX_WITH_INCUS:-0}
export DEBIAN_FRONTEND=noninteractive

step() { printf '\n==> %s\n' "$*"; }
skip() { printf '\n==> Skipping %s (%s)\n' "$1" "$2"; }

step "Waiting for the network"
for _ in $(seq 1 60); do getent hosts deb.debian.org >/dev/null && break; sleep 1; done

step "Base packages"
apt-get update -q
apt-get install -y -q --no-install-recommends \
  ca-certificates curl wget gnupg git tmux sudo build-essential jq ripgrep unzip xz-utils \
  procps iproute2 iputils-ping less nano python3 python3-venv openssh-client \
  libarchive-tools

step "Docker"
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  >/etc/apt/sources.list.d/docker.list
apt-get update -q
apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin docker-buildx-plugin

if [[ $WITH_INCUS == 1 ]] && dpkg-query -W -f='${Status}' incus 2>/dev/null | grep -q 'ok installed'; then
  skip "Incus" "it is already installed, and may be running for a project with nesting on"
elif [[ $WITH_INCUS == 1 ]]; then
  step "Incus, for an agent whose project turns nesting on"
  apt-get install -y -q incus
  # incus's postinst gives root a subuid/subgid range sized for a normal host
  # (root:1000000:1000000000), which is bigger than this container's own
  # idmap actually covers: writing a nested container's uid_map with it fails
  # with EPERM, since the kernel refuses to delegate ids this container
  # doesn't itself have. A small range, well inside what every agent's idmap
  # covers regardless of host (only the agent user's own uid, 1000, is
  # pinned outside the automatic range — see image.IDMap), replaces it.
  sed -i 's/^root:.*/root:100000:65536/' /etc/subuid /etc/subgid
  # host-setup.sh reads this to tell a nested agent from a real host: a real
  # host's own small range still gets the billion-ID one added (that's what
  # a real host's own idmap can actually back), and a real host's own failed
  # btrfs pool is a real problem to see, not one to paper over with a
  # silent, slower directory pool. Neither is true here.
  touch /etc/agentbox-nested-incus
  # Off until a project turns nesting on: `incus admin init` runs then
  # (agent.EnsureNesting), not at boot, so an agent without it spends nothing.
  systemctl disable --now incus.socket incus >/dev/null 2>&1 || true
else
  skip "Incus" "nesting is off"
fi

step "Browser and media: a display with a VNC server, a small desktop, Chromium and ffmpeg"
apt-get install -y -q --no-install-recommends tigervnc-standalone-server chromium \
  openbox tint2 pcmanfm xfce4-terminal xdotool x11-utils x11-xserver-utils \
  xwallpaper adwaita-icon-theme librsvg2-common fonts-liberation fonts-dejavu-core fonts-noto-color-emoji ffmpeg

# Mesa's DRI drivers, VA-API and Vulkan: in every image, whether or not
# "GPU for agents" is ever turned on for this installation, because that
# setting only decides whether an agent's container gets a GPU device
# (internal/agent/gpu.go) — the userspace that renders and encodes on one has
# to already be here, or turning it on would do nothing. mesa-va-drivers
# covers AMD and Intel; NVIDIA agents render and encode through the
# proprietary driver's own libraries, which Incus's gpu device brings in with
# nvidia.runtime=true rather than anything installed here. vainfo lets an
# agent check chrome://gpu-style whether VA-API actually found the device.
apt-get install -y -q --no-install-recommends \
  mesa-va-drivers mesa-vulkan-drivers libgl1-mesa-dri libegl-mesa0 vainfo vulkan-tools

SCRCPY_VERSION=v4.1
SCRCPY_SHA256=ad56ae8bfeedf41e824945c11dbf55fcb092b3e615b9b486f48a50e30d389635
if [[ $WITH_ANDROID == 1 ]]; then
  step "scrcpy $SCRCPY_VERSION, which shows Android emulators"
  curl -fsSLo /tmp/scrcpy.tar.gz "https://github.com/Genymobile/scrcpy/releases/download/$SCRCPY_VERSION/scrcpy-linux-x86_64-$SCRCPY_VERSION.tar.gz"
  echo "$SCRCPY_SHA256  /tmp/scrcpy.tar.gz" | sha256sum -c -
  rm -rf /opt/scrcpy && mkdir -p /opt/scrcpy
  tar -xzf /tmp/scrcpy.tar.gz -C /opt/scrcpy --strip-components=1
  rm /tmp/scrcpy.tar.gz
else
  skip "scrcpy" "the Android tools are off"
fi

step "mise"
curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh

step "The groups and sudo of $USER_NAME"
# kvm: agents with Android get /dev/kvm, which Debian gives to this group at boot.
getent group kvm >/dev/null || groupadd -r kvm
usermod -aG sudo,docker,kvm "$USER_NAME"
if [[ $WITH_INCUS == 1 ]]; then
  usermod -aG incus-admin "$USER_NAME"
fi
echo "$USER_NAME ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/90-agentbox
chmod 0440 /etc/sudoers.d/90-agentbox

cat >/etc/profile.d/agentbox.sh <<'EOF'
export LANG=C.UTF-8
export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:$PATH"
# Temporary files, and so every test's t.TempDir(), go to the tmpfs on /t.
export TMPDIR=/t
# Credentials AgentBox writes when it creates the agent.
if [ -f "$HOME/.config/agentbox/env" ]; then . "$HOME/.config/agentbox/env"; fi
EOF

# Debian mounts a tmpfs on /tmp at boot, which would hide worktrees and
# repositories that Incus mounts under /tmp.
systemctl mask tmp.mount
# Temporary files go to a tmpfs of their own instead, which makes tests that
# write many small files faster. Its path is short on purpose: a unix socket's
# path can't be longer than 107 bytes, and tests make sockets in t.TempDir().
# The directory is there without the mount too, so TMPDIR works before the
# first boot mounts it, while this script runs.
install -d -m 1777 /t
grep -q '^tmpfs /t ' /etc/fstab || echo 'tmpfs /t tmpfs mode=1777,nosuid,nodev 0 0' >>/etc/fstab
