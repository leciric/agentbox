#!/usr/bin/env bash
# Step 0 spike (throwaway): runs as root inside the base container.
# Usage: provision-base.sh <user> <uid> <gid>
set -euo pipefail
USER_NAME=$1 USER_UID=$2 USER_GID=$3
export DEBIAN_FRONTEND=noninteractive

echo "== waiting for network"
for _ in $(seq 1 60); do getent hosts deb.debian.org >/dev/null && break; sleep 1; done

echo "== apt packages"
apt-get update -q
apt-get install -y -q --no-install-recommends \
  ca-certificates curl wget gnupg git tmux sudo build-essential jq ripgrep unzip xz-utils \
  procps iproute2 iputils-ping less nano python3 python3-venv openssh-client

echo "== docker"
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  >/etc/apt/sources.list.d/docker.list
apt-get update -q
apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin docker-buildx-plugin

echo "== mise"
curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh

echo "== user $USER_NAME ($USER_UID:$USER_GID)"
existing=$(getent passwd "$USER_UID" | cut -d: -f1 || true)
if [[ -n "$existing" && "$existing" != "$USER_NAME" ]]; then
  userdel -r "$existing"
fi
getent group "$USER_GID" >/dev/null || groupadd -g "$USER_GID" "$USER_NAME"
id "$USER_NAME" >/dev/null 2>&1 || useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash "$USER_NAME"
usermod -aG sudo,docker "$USER_NAME"
echo "$USER_NAME ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/90-agentbox
chmod 0440 /etc/sudoers.d/90-agentbox

cat >/etc/profile.d/agentbox.sh <<'EOF'
export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:$PATH"
if [ -f "$HOME/.config/agentbox/env" ]; then . "$HOME/.config/agentbox/env"; fi
EOF

echo "== user tools"
runuser -l "$USER_NAME" -c 'export MISE_YES=1; mise use -g node@lts pnpm@latest'
runuser -l "$USER_NAME" -c 'export MISE_YES=1; mise use -g claude@latest' ||
  runuser -l "$USER_NAME" -c 'curl -fsSL https://claude.ai/install.sh | bash'
runuser -l "$USER_NAME" -c 'export MISE_YES=1; mise use -g codex@latest' ||
  runuser -l "$USER_NAME" -c 'npm install -g @openai/codex'
runuser -l "$USER_NAME" -c 'mkdir -p ~/.config/agentbox && echo "{\"hasCompletedOnboarding\": true}" > ~/.claude.json'

echo "== versions"
runuser -l "$USER_NAME" -c 'git --version; node --version; pnpm --version; claude --version; codex --version; docker --version; docker compose version'
