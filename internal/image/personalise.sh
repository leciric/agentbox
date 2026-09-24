#!/usr/bin/env bash
# Makes the base image this machine's. Runs as root inside the instance
# provision.sh built, once, before it is snapshotted as ready.
#
# provision.sh built the image around a placeholder user that belongs to no
# machine. This renames it to the host's own user, with the host's UID and GID,
# so files agents write in their mounted worktrees are the host user's
# (raw.idmap, D4) and /home/<user> is where AgentBox expects it.
#
# Usage: personalise.sh <placeholder> <user> <uid> <gid>
set -euo pipefail
PLACEHOLDER=$1 USER_NAME=$2 USER_UID=$3 USER_GID=$4

step() { printf '\n==> %s\n' "$*"; }
as_user() { runuser -l "$USER_NAME" -c "$*"; }

if [[ $USER_UID -eq 0 || $USER_GID -eq 0 ]]; then
  echo "refusing to give the agents' user root's UID or GID: run AgentBox as yourself, not as root" >&2
  exit 1
fi

if [[ "$USER_NAME" == "$PLACEHOLDER" && $USER_UID -eq 1000 && $USER_GID -eq 1000 ]]; then
  step "The image's user is already $USER_NAME ($USER_UID:$USER_GID)"
else
  step "Renaming $PLACEHOLDER to $USER_NAME ($USER_UID:$USER_GID)"
  # Anything already holding the wanted UID or name would block the rename.
  # Debian 13 ships no account above UID 999 except nobody, so this only fires
  # for a host user with an unusual ID — and a system account of the image's is
  # refused rather than deleted, because deleting it breaks the image quietly.
  taken=$(getent passwd "$USER_UID" | cut -d: -f1 || true)
  if [[ -n "$taken" && "$taken" != "$PLACEHOLDER" ]]; then
    if [[ $USER_UID -lt 1000 ]]; then
      echo "UID $USER_UID is the system account $taken inside the image: agents need a regular user account" >&2
      exit 1
    fi
    userdel -r "$taken"
  fi
  if [[ "$USER_NAME" != "$PLACEHOLDER" ]] && id "$USER_NAME" >/dev/null 2>&1; then userdel -r "$USER_NAME"; fi

  # The group has to end up with the host's GID. A group of the image's own that
  # already holds it is reused as it is (Debian's "users" is 100, and a host
  # user's primary group is often a system one); otherwise the placeholder's
  # group takes that GID, under the host group's name when the image has no
  # group by that name already.
  group=$(getent group "$USER_GID" | cut -d: -f1 || true)
  if [[ -z "$group" || "$group" == "$PLACEHOLDER" ]]; then
    group=$PLACEHOLDER
    if [[ "$USER_NAME" != "$PLACEHOLDER" ]] && ! getent group "$USER_NAME" >/dev/null; then
      groupmod -n "$USER_NAME" "$PLACEHOLDER"
      group=$USER_NAME
    fi
    if [[ "$(getent group "$group" | cut -d: -f3)" != "$USER_GID" ]]; then
      groupmod -g "$USER_GID" "$group"
    fi
  fi

  # usermod -l keeps the user's other group memberships (sudo, docker, kvm) and
  # its subordinate ID ranges, -m moves the home directory, and -u rewrites the
  # ownership inside it.
  args=(-u "$USER_UID" -g "$USER_GID")
  if [[ "$USER_NAME" != "$PLACEHOLDER" ]]; then args+=(-l "$USER_NAME" -d "/home/$USER_NAME" -m); fi
  usermod "${args[@]}" "$PLACEHOLDER"
  # Files the image left owned by the placeholder's group, and anything usermod
  # skipped because it sits outside the home directory it moved.
  chown -R "$USER_UID:$USER_GID" "/home/$USER_NAME"
  # Older usermod leaves these behind; rootless Docker inside the agent reads them.
  for f in /etc/subuid /etc/subgid; do
    if [[ -f "$f" ]]; then sed -i "s/^$PLACEHOLDER:/$USER_NAME:/" "$f"; fi
  done
  if [[ "$group" != "$PLACEHOLDER" ]]; then groupdel "$PLACEHOLDER" 2>/dev/null || true; fi

  echo "$USER_NAME ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/90-agentbox
  chmod 0440 /etc/sudoers.d/90-agentbox
fi

step "Checking the image still works as $USER_NAME"
# A tool that kept the placeholder's home directory in an absolute path would
# only break inside an agent, hours later. Fail the build here instead.
test "$(getent passwd "$USER_NAME" | cut -d: -f6)" = "/home/$USER_NAME"
as_user 'git --version; go version; node --version; pnpm --version; claude --version; codex --version; gh --version; playwright-mcp --version'
as_user 'test -f ~/.claude/settings.json && test -d ~/.config/agentbox'
as_user 'sudo -n true'
