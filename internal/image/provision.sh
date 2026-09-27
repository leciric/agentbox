#!/usr/bin/env bash
# Provisions the AgentBox base image. Runs as root inside the base instance.
#
# The user it creates is a placeholder that belongs to no machine (image.go's
# Placeholder), so one image fits every host: personalise.sh renames it to the
# host's own user when the image is made ready.
#
# Usage: provision.sh <user> <uid> <gid>
#
# tools.sh and tools.list, the agent tools to install, must be next to it in
# /root: Build puts them there.
#
# Some parts of the image are optional, and off unless internal/image asks for
# them (image.Options). Each is a download most agents never need, so a plain
# build stays small:
#   AGENTBOX_WITH_ANDROID=1  scrcpy, which mirrors an Android emulator's screen
#   AGENTBOX_WITH_CODEX=1    the Codex CLI and its ACP adapter
#   AGENTBOX_WITH_OPENCODE=1 the OpenCode CLI, which is its own ACP adapter
#   AGENTBOX_WITH_DEV_CACHES=1 the Go, npm and Electron caches of AgentBox's own
#                            repository, for agents that work on AgentBox itself
#   AGENTBOX_WITH_INCUS=1    Incus itself, so an agent can run a real Incus
#                            daemon of its own (a project's "nesting" setting)
#
# AGENTBOX_DEBIAN_MIRROR is a Debian mirror to download Debian's packages from
# instead of deb.debian.org, for a connection on which deb.debian.org is slow.
# The image keeps it: the agents copied from it are on the same connection.
set -euo pipefail
USER_NAME=$1 USER_UID=$2 USER_GID=$3
WITH_ANDROID=${AGENTBOX_WITH_ANDROID:-0} WITH_CODEX=${AGENTBOX_WITH_CODEX:-0}
WITH_OPENCODE=${AGENTBOX_WITH_OPENCODE:-0} WITH_DEV_CACHES=${AGENTBOX_WITH_DEV_CACHES:-0}
WITH_INCUS=${AGENTBOX_WITH_INCUS:-0}
DEBIAN_MIRROR=${AGENTBOX_DEBIAN_MIRROR:-}
export DEBIAN_FRONTEND=noninteractive

step() { printf '\n==> %s\n' "$*"; }
skip() { printf '\n==> Skipping %s (%s)\n' "$1" "$2"; }
as_user() { runuser -l "$USER_NAME" -c "$*"; }

# debian_from_mirror points the sources file it's given at the mirror, for
# Debian's own archive only: the security archive isn't on every mirror, and is
# small. The mirror has to be a web address and nothing else, since it goes
# into a file apt reads and into sed's replacement.
debian_from_mirror() {
  local url='^https?://[A-Za-z0-9._~:/%-]+$' sources
  [[ $1 =~ $url ]] || { echo "AGENTBOX_DEBIAN_MIRROR isn't a web address: $1" >&2; return 1; }
  sources=$(sed "s|http://deb.debian.org/debian |${1%/} |" "$2")
  printf '%s\n' "$sources" >"$2"
}

step "Waiting for the network"
for _ in $(seq 1 60); do getent hosts deb.debian.org >/dev/null && break; sleep 1; done

if [[ -n $DEBIAN_MIRROR ]]; then
  step "Downloading Debian's packages from $DEBIAN_MIRROR"
  debian_from_mirror "$DEBIAN_MIRROR" /etc/apt/sources.list
fi

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

if [[ $WITH_INCUS == 1 ]]; then
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

step "User $USER_NAME ($USER_UID:$USER_GID)"
existing=$(getent passwd "$USER_UID" | cut -d: -f1 || true)
if [[ -n "$existing" && "$existing" != "$USER_NAME" ]]; then
  userdel -r "$existing"
fi
getent group "$USER_GID" >/dev/null || groupadd -g "$USER_GID" "$USER_NAME"
id "$USER_NAME" >/dev/null 2>&1 || useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash "$USER_NAME"
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
echo 'tmpfs /t tmpfs mode=1777,nosuid,nodev 0 0' >>/etc/fstab

# The agent tools are pinned in tools.txt, and installed by tools.sh from the
# list internal/image wrote next to this script for the image's components:
# they move on without a rebuild (image.UpdateTools).
step "Go, Node.js, pnpm, Claude Code, its ACP adapter, the GitHub CLI and the Playwright MCP server for $USER_NAME"
[[ $WITH_CODEX == 1 ]] || skip "the Codex CLI and its ACP adapter" "Codex is off"
[[ $WITH_OPENCODE == 1 ]] || skip "the OpenCode CLI" "OpenCode is off"
/root/tools.sh install "$USER_NAME" /root/tools.list
# AgentBox writes into these later, so they must belong to the user rather than root.
as_user 'mkdir -p ~/.claude ~/.config/agentbox'
as_user 'echo "{\"skipDangerousModePermissionPrompt\": true}" > ~/.claude/settings.json'
if [[ $WITH_CODEX == 1 ]]; then
  as_user 'mkdir -p ~/.codex'
fi
# AgentBox writes OpenCode's login and its MCP servers into these (configure in
# internal/agent/agent.go), at the XDG paths OpenCode reads them from.
if [[ $WITH_OPENCODE == 1 ]]; then
  as_user 'mkdir -p ~/.local/share/opencode ~/.config/opencode'
fi

DEV_CACHES_REPO=https://github.com/leciric/agentbox
# The Go module cache sits outside the home directory, at a path that doesn't
# name the user, because personalise.sh moves the home directory: Go's build
# cache keys a dependency's compiled package on the directory it was built
# from, and a moved module cache would miss every entry this step makes.
DEV_GOMODCACHE=/var/cache/agentbox/go-mod
if [[ $WITH_DEV_CACHES == 1 ]]; then
  step "AgentBox's development caches: Go's modules and build cache, npm's cache and Electron"
  install -d -o "$USER_UID" -g "$USER_GID" "$DEV_GOMODCACHE"
  as_user "go env -w GOMODCACHE=$DEV_GOMODCACHE"
  # A cache is worth having rather than necessary: if GitHub or a registry
  # fails here, the image is still good, and agents download what they need.
  # The clone's mise.toml goes, so the pinned tools above do the work rather
  # than the versions it asks mise to install. `go test -run '^$'` compiles
  # every test binary, and so their dependencies and the standard library,
  # without running a test. npm ci fills ~/.npm; Electron has no postinstall,
  # and downloads itself the first time it runs, so its install.js is run to
  # put that download in ~/.cache/electron.
  if ! as_user "set -e; dir=\$(mktemp -d); trap 'rm -rf \$dir' EXIT
      git clone -q --depth 1 $DEV_CACHES_REPO \$dir; cd \$dir; rm -f mise.toml
      go mod download; go test -count=1 -run '^\$' ./... >/dev/null || echo 'go test failed: the build cache is only partly filled'
      npm --prefix desktop ci --no-audit --no-fund --loglevel=error
      node desktop/node_modules/electron/install.js"; then
    echo "==> Warning: AgentBox's development caches are incomplete; agents will download what's missing" >&2
  fi
else
  skip "AgentBox's development caches" "they are off"
fi

step "Versions"
as_user 'git --version; docker --version; docker compose version'
chromium --version
/root/tools.sh verify "$USER_NAME" /root/tools.list
if [[ $WITH_ANDROID == 1 ]]; then
  /opt/scrcpy/scrcpy --version | head -n 1
fi
if [[ $WITH_INCUS == 1 ]]; then
  dpkg-query -W -f='incus ${Version}\n' incus
fi
