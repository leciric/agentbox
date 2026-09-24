#!/usr/bin/env bash
# Step 0 spike (throwaway). Proves the isolation model by hand; results are in
# docs/implementation/step-0-spike.md.
#
#   agent.sh base                      build the base container + "ready" snapshot
#   agent.sh create <repo> <agent>     worktree + container copy + mounts + tmux
#   agent.sh run <agent> <command>     run a login-shell command in the agent's worktree
#   agent.sh shell <agent>             attach to the agent's tmux session
#   agent.sh ip <agent>
#   agent.sh snapshot <agent> <name>   container snapshot + worktree snapshot ref
#   agent.sh restore <agent> <name>
#   agent.sh destroy <agent>
#
# IDMAP=raw (default) maps only your UID/GID 1:1 into the container.
# IDMAP=shift uses idmapped disk devices (shift=true) instead.
# CLAUDE_CODE_OAUTH_TOKEN, if set, is written to the agent's ~/.config/agentbox/env.
set -euo pipefail

SPIKE_HOME="${SPIKE_HOME:-$HOME/.local/share/agentbox-spike}"
IDMAP="${IDMAP:-raw}"
BASE=ab-spike-base
PROFILE=ab-spike
HERE="$(cd "$(dirname "$0")" && pwd)"
USER_NAME="$(id -un)" USER_UID="$(id -u)" USER_GID="$(id -g)"

die() { echo "error: $*" >&2; exit 1; }
inst_of() { echo "ab-spike-$1"; }
meta() { cat "$SPIKE_HOME/agents/$1/$2"; }
ip_of() { incus query "/1.0/instances/$1/state" | jq -r '.network.eth0.addresses[]? | select(.family=="inet") | .address' | head -1; }
as_user() { local inst=$1; shift; incus exec "$inst" -- runuser -l "$USER_NAME" -c "$*"; }

ensure_profile() {
  incus profile show "$PROFILE" >/dev/null 2>&1 && return
  local mods=() m
  for m in overlay br_netfilter ip_tables ip6_tables iptable_nat nf_nat nf_tables xt_conntrack xt_MASQUERADE xt_addrtype; do
    modinfo -n "$m" >/dev/null 2>&1 && mods+=("$m")
  done
  incus profile create "$PROFILE"
  incus profile set "$PROFILE" \
    security.nesting=true \
    security.syscalls.intercept.mknod=true \
    security.syscalls.intercept.setxattr=true \
    linux.kernel_modules="$(IFS=,; echo "${mods[*]}")"
}

set_idmap() {
  if [[ $IDMAP == raw ]]; then
    incus config set "$1" raw.idmap "$(printf 'uid %s %s\ngid %s %s' "$USER_UID" "$USER_UID" "$USER_GID" "$USER_GID")"
  else
    incus config unset "$1" raw.idmap
  fi
}

wait_ready() {
  incus exec "$1" -- systemctl is-system-running --wait >/dev/null 2>&1 || true
  for _ in $(seq 1 60); do
    [[ -n "$(ip_of "$1")" ]] && return 0
    sleep 1
  done
  die "$1 got no IPv4 address"
}

push_env() {
  local inst=$1 tmp
  tmp=$(mktemp)
  chmod 600 "$tmp"
  if [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
    printf 'export CLAUDE_CODE_OAUTH_TOKEN=%q\n' "$CLAUDE_CODE_OAUTH_TOKEN" >>"$tmp"
  fi
  as_user "$inst" 'mkdir -p ~/.config/agentbox'
  incus file push --uid "$USER_UID" --gid "$USER_GID" --mode 0600 "$tmp" "$inst/home/$USER_NAME/.config/agentbox/env"
  rm -f "$tmp"
}

cmd_base() {
  ensure_profile
  if ! incus info "$BASE" >/dev/null 2>&1; then
    incus init images:debian/13 "$BASE" -p default -p "$PROFILE"
    set_idmap "$BASE"
    incus start "$BASE"
  fi
  wait_ready "$BASE"
  incus file push "$HERE/provision-base.sh" "$BASE/root/provision-base.sh"
  incus exec "$BASE" -- bash /root/provision-base.sh "$USER_NAME" "$USER_UID" "$USER_GID"
  # Copies must boot with a fresh machine-id, or DHCP hands them all the same IP.
  incus exec "$BASE" -- sh -c 'truncate -s0 /etc/machine-id && rm -f /var/lib/dbus/machine-id'
  incus stop "$BASE"
  incus snapshot delete "$BASE" ready 2>/dev/null || true
  incus snapshot create "$BASE" ready
  echo "base ready: $BASE/ready"
}

cmd_create() {
  [[ $# -eq 2 ]] || die "usage: create <repo> <agent>"
  local repo agent=$2 inst wt gitdir shift=false
  repo=$(git -C "$1" rev-parse --show-toplevel)
  gitdir=$(git -C "$repo" rev-parse --absolute-git-dir)
  inst=$(inst_of "$agent") wt="$SPIKE_HOME/worktrees/$agent"
  [[ $IDMAP == shift ]] && shift=true
  mkdir -p "$SPIKE_HOME/agents/$agent" "$SPIKE_HOME/worktrees"
  echo "$repo" >"$SPIKE_HOME/agents/$agent/repo"
  echo "$wt" >"$SPIKE_HOME/agents/$agent/worktree"

  git -C "$repo" worktree add -q -b "agentbox/$agent" "$wt" HEAD
  incus copy "$BASE/ready" "$inst"
  set_idmap "$inst"
  # Identical absolute paths inside the agent, so the worktree's .git pointer resolves.
  incus config device add "$inst" worktree disk source="$wt" path="$wt" shift="$shift" >/dev/null
  incus config device add "$inst" gitdir disk source="$gitdir" path="$gitdir" shift="$shift" >/dev/null
  incus start "$inst"
  wait_ready "$inst"

  as_user "$inst" "git config --global user.name '$(git config --global user.name)' && git config --global user.email '$(git config --global user.email)'"
  push_env "$inst"
  as_user "$inst" "tmux new -d -s main -c '$wt'"
  echo "$agent ready: ip=$(ip_of "$inst") worktree=$wt branch=agentbox/$agent"
}

cmd_run() {
  local agent=$1
  shift
  as_user "$(inst_of "$agent")" "cd '$(meta "$agent" worktree)' && $*"
}

cmd_shell() {
  incus exec "$(inst_of "$1")" -t -- runuser -l "$USER_NAME" -c "tmux new -A -s main -c '$(meta "$1" worktree)'"
}

cmd_ip() { ip_of "$(inst_of "$1")"; }

# Records tracked + untracked (not ignored) files as a commit on a hidden ref,
# using a temporary index so the agent's own index and branch are untouched.
worktree_snapshot() {
  local wt=$1 ref=$2 tmp head tree commit
  tmp=$(mktemp -d)
  head=$(git -C "$wt" rev-parse HEAD)
  GIT_INDEX_FILE="$tmp/index" git -C "$wt" read-tree HEAD
  GIT_INDEX_FILE="$tmp/index" git -C "$wt" add -A
  tree=$(GIT_INDEX_FILE="$tmp/index" git -C "$wt" write-tree)
  commit=$(git -C "$wt" commit-tree "$tree" -p "$head" -m "agentbox snapshot")
  git -C "$wt" update-ref "$ref" "$commit"
  rm -rf "$tmp"
}

cmd_snapshot() {
  local agent=$1 name=$2
  incus snapshot create "$(inst_of "$agent")" "$name"
  worktree_snapshot "$(meta "$agent" worktree)" "refs/agentbox/snapshots/$agent/$name"
  echo "snapshot $agent@$name"
}

cmd_restore() {
  local agent=$1 name=$2 inst wt commit head
  inst=$(inst_of "$agent") wt=$(meta "$agent" worktree)
  commit=$(git -C "$wt" rev-parse --verify -q "refs/agentbox/snapshots/$agent/$name") || die "no snapshot $name"
  head=$(git -C "$wt" rev-parse "$commit^")
  worktree_snapshot "$wt" "refs/agentbox/pre-restore/$agent/$(date +%s)"
  incus snapshot restore "$inst" "$name"
  incus start "$inst" 2>/dev/null || true
  git -C "$wt" reset -q --hard "$head"
  git -C "$wt" clean -fdq
  git -C "$wt" read-tree -m -u HEAD "$commit"
  git -C "$wt" reset -q
  wait_ready "$inst"
  as_user "$inst" "tmux has-session -t main 2>/dev/null || tmux new -d -s main -c '$wt'"
  echo "restored $agent@$name"
}

cmd_destroy() {
  local agent=$1 inst repo wt
  inst=$(inst_of "$agent") repo=$(meta "$agent" repo) wt=$(meta "$agent" worktree)
  incus delete -f "$inst" 2>/dev/null || true
  git -C "$repo" worktree remove --force "$wt" 2>/dev/null || rm -rf "$wt"
  git -C "$repo" worktree prune
  git -C "$repo" branch -q -D "agentbox/$agent" 2>/dev/null || true
  git -C "$repo" for-each-ref --format='%(refname)' "refs/agentbox/snapshots/$agent" "refs/agentbox/pre-restore/$agent" |
    while read -r ref; do git -C "$repo" update-ref -d "$ref"; done
  rm -rf "$SPIKE_HOME/agents/$agent"
  echo "destroyed $agent"
}

case "${1:-}" in
  base) shift; cmd_base "$@" ;;
  create) shift; cmd_create "$@" ;;
  run) shift; cmd_run "$@" ;;
  shell) shift; cmd_shell "$@" ;;
  ip) shift; cmd_ip "$@" ;;
  snapshot) shift; cmd_snapshot "$@" ;;
  restore) shift; cmd_restore "$@" ;;
  destroy) shift; cmd_destroy "$@" ;;
  *) sed -n '2,18p' "$0"; exit 1 ;;
esac
