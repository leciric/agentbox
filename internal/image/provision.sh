#!/usr/bin/env bash
# Provisions the AgentBox base image. Runs as root inside the base instance.
#
# The user it creates is a placeholder that belongs to no machine (image.go's
# Placeholder), so one image fits every host: personalise.sh renames it to the
# host's own user when the image is made ready.
#
# Usage: provision.sh <user> <uid> <gid>
#
# Two parts of the image are optional, and off unless internal/image asks for
# them (image.Options). Each is a download most agents never need, so a plain
# build stays small:
#   AGENTBOX_WITH_ANDROID=1  scrcpy, which mirrors an Android emulator's screen
#   AGENTBOX_WITH_CODEX=1    the Codex CLI and its ACP adapter
#   AGENTBOX_WITH_OPENCODE=1 the OpenCode CLI, which is its own ACP adapter
#
# AGENTBOX_DEBIAN_MIRROR is a Debian mirror to download Debian's packages from
# instead of deb.debian.org, for a connection on which deb.debian.org is slow.
# The image keeps it: the agents copied from it are on the same connection.
set -euo pipefail
USER_NAME=$1 USER_UID=$2 USER_GID=$3
WITH_ANDROID=${AGENTBOX_WITH_ANDROID:-0} WITH_CODEX=${AGENTBOX_WITH_CODEX:-0}
WITH_OPENCODE=${AGENTBOX_WITH_OPENCODE:-0}
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
  procps iproute2 iputils-ping less nano python3 python3-venv openssh-client

step "Docker"
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  >/etc/apt/sources.list.d/docker.list
apt-get update -q
apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin docker-buildx-plugin

step "Browser and media: a display with a VNC server, a small desktop, Chromium and ffmpeg"
apt-get install -y -q --no-install-recommends tigervnc-standalone-server chromium \
  openbox tint2 pcmanfm xfce4-terminal xdotool screenkey x11-utils x11-xserver-utils \
  xwallpaper adwaita-icon-theme librsvg2-common fonts-liberation fonts-noto-color-emoji ffmpeg

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
echo "$USER_NAME ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/90-agentbox
chmod 0440 /etc/sudoers.d/90-agentbox

cat >/etc/profile.d/agentbox.sh <<'EOF'
export LANG=C.UTF-8
export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:$PATH"
# Credentials AgentBox writes when it creates the agent.
if [ -f "$HOME/.config/agentbox/env" ]; then . "$HOME/.config/agentbox/env"; fi
EOF

# Debian mounts a tmpfs on /tmp at boot, which would hide worktrees and
# repositories that Incus mounts under /tmp.
systemctl mask tmp.mount

# Every tool is pinned: two builds of the same image version then install the
# same thing, and a release that breaks agents can't arrive on its own. These
# were the current stable versions on 2026-09-18, except Claude Code, moved to
# 2.1.280 on 2026-09-22 for Opus 5.5; docs/implementation/
# packaging-and-setup.md says how to move them on.
GO_VERSION=1.27.1        # go.dev/dl
NODE_VERSION=24.21.0     # Node.js 24 "Krypton", the active LTS line
PNPM_VERSION=12.4.2      # npm: pnpm
CLAUDE_VERSION=2.1.280   # npm: @anthropic-ai/claude-code
GH_VERSION=2.101.0       # github.com/cli/cli releases
CODEX_VERSION=0.155.0    # npm: @openai/codex, only with AGENTBOX_WITH_CODEX
OPENCODE_VERSION=1.18.31 # npm: opencode-ai, only with AGENTBOX_WITH_OPENCODE
# @playwright/mcp is pinned to the newest release published with provenance
# from its GitHub workflow: later releases have no trust evidence (D21).
# The ACP adapters run Claude Code and Codex for the app's chat. They are pinned
# to the versions in internal/agent/chat.go, which installs them in older agents.
# Once a tool has a shim here, mise auto-installs whatever version a project
# pins for it (this repo's own mise.toml, say) the first time it runs, with no
# `mise activate` or other shell setup. A tool with no shim at all, like a bare
# `go` before this line existed, just isn't found.
TOOLS="go@$GO_VERSION node@$NODE_VERSION pnpm@$PNPM_VERSION claude@$CLAUDE_VERSION gh@$GH_VERSION"
TOOLS="$TOOLS npm:@playwright/mcp@0.0.79 npm:@agentclientprotocol/claude-agent-acp@0.81.0"
if [[ $WITH_CODEX == 1 ]]; then
  TOOLS="$TOOLS codex@$CODEX_VERSION npm:@agentclientprotocol/codex-acp@1.11.0"
fi
# OpenCode needs no separate adapter: `opencode acp` is one, and it is the same
# version pinned in internal/agent/chat.go.
if [[ $WITH_OPENCODE == 1 ]]; then
  TOOLS="$TOOLS npm:opencode-ai@$OPENCODE_VERSION"
fi

step "Go, Node.js, pnpm, Claude Code, its ACP adapter, the GitHub CLI and the Playwright MCP server for $USER_NAME"
[[ $WITH_CODEX == 1 ]] || skip "the Codex CLI and its ACP adapter" "Codex is off"
[[ $WITH_OPENCODE == 1 ]] || skip "the OpenCode CLI" "OpenCode is off"
as_user "export MISE_YES=1 && mise use -g $TOOLS"
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

step "Versions"
as_user 'git --version; go version; node --version; pnpm --version; claude --version; gh --version; playwright-mcp --version; docker --version; docker compose version'
as_user 'playwright-mcp --help | grep -q -- --cdp-endpoint' || { echo "playwright-mcp has no --cdp-endpoint option" >&2; exit 1; }
as_user 'command -v claude-agent-acp'
chromium --version
if [[ $WITH_CODEX == 1 ]]; then
  as_user 'codex --version; command -v codex-acp'
fi
if [[ $WITH_OPENCODE == 1 ]]; then
  as_user 'opencode --version'
  as_user 'opencode acp --help | grep -q "ACP"' || { echo "opencode has no acp subcommand" >&2; exit 1; }
fi
if [[ $WITH_ANDROID == 1 ]]; then
  /opt/scrcpy/scrcpy --version | head -n 1
fi
