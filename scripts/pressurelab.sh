#!/usr/bin/env bash
# The load lab (TestLoadLab, internal/agent/loadlab_internal_test.go): N real
# agents of a daemon of its own, all working at once, measured from outside.
# Run it from a checkout, in an agent of a project with nesting on, or on any
# Linux machine where Incus may run containers (it installs Incus when
# missing, the way a nested agent's is set up: agent.EnsureNesting).
#
#   scripts/pressurelab.sh                       # 30 agents, 10 minutes
#   LAB_AGENTS=12 LAB_DURATION=5m scripts/pressurelab.sh
#   LAB_GATE=none scripts/pressurelab.sh         # as non-Claude agents run
#   scripts/pressurelab.sh --restart             # restart the lab's daemon first
#   scripts/pressurelab.sh --down                # stop the lab's daemon
#
# It sets up, once: a user of its own (LAB_USER, "lab"), so the lab's daemon,
# state and worktrees never touch yours; that daemon, built from this
# checkout and restarted when the build changed; a placeholder Claude Code
# token (agents need one to start; nothing it sends can reach a model); the
# base image; and the project, a shallow clone of this checkout. Then it runs
# the lab, which writes report.md and report.json to LAB_OUT
# (./loadlab-<time>). Every LAB_* variable the lab reads passes through.
set -euo pipefail

cd "$(dirname "$0")/.."
LAB_USER=${LAB_USER:-lab}
bin=/usr/local/lib/agentbox-lab
labhome=/home/$LAB_USER
socket=$labhome/.local/share/agentbox/run/agentbox.sock
export AGENTBOX_FRONT_END=host

as_lab() { sudo -u "$LAB_USER" -H env AGENTBOX_FRONT_END=host bash -lc "$*"; }
step() { printf '==> %s\n' "$*"; }

stop_daemon() { sudo pkill -u "$LAB_USER" -x agentbox-lab 2>/dev/null || true; sleep 1; }
if [[ ${1:-} == --restart ]]; then
  stop_daemon
  shift
fi
if [[ ${1:-} == --down ]]; then
  stop_daemon
  exit 0
fi

# Incus, as nesting sets it up: a dir pool (btrfs needs privileges a nested
# container lacks), a bridge that can't clash with the host's, and ids the
# container's own idmap covers.
if ! command -v incus >/dev/null; then
  step "Installing Incus"
  sudo apt-get update -qq && sudo apt-get install -y -qq incus >/dev/null
fi
trim_subids() {
  # A container's own uid map often ends near 1e9: a subordinate range past it
  # makes newuidmap fail with EPERM when a nested container starts.
  local top
  top=$(awk '{ e = $2 + $3; if (e > m) m = e } END { print m }' /proc/self/uid_map)
  for f in /etc/subuid /etc/subgid; do
    sudo awk -F: -v top="$top" '!($2 + $3 > top)' "$f" | sudo tee "$f.lab" >/dev/null
    grep -q '^root:' "$f.lab" || echo "root:100000:65536" | sudo tee -a "$f.lab" >/dev/null
    if ! sudo cmp -s "$f" "$f.lab"; then
      sudo mv "$f.lab" "$f"
      sudo systemctl restart incus
    else
      sudo rm -f "$f.lab"
    fi
  done
}
# On btrfs, the lab deduplicates each agent's copy of the base (TestLoadLab):
# a dir pool otherwise costs ~5 GiB of a shared disk per agent.
if [[ $(findmnt -no FSTYPE -T /var/lib) == btrfs ]] && ! command -v duperemove >/dev/null; then
  sudo apt-get install -y -qq duperemove >/dev/null
fi
sudo systemctl enable --now incus.socket incus >/dev/null 2>&1 || true
trim_subids

if ! id "$LAB_USER" >/dev/null 2>&1; then
  step "Making the lab's user, $LAB_USER"
  sudo useradd -m -s /bin/bash "$LAB_USER"
  echo "$LAB_USER ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$LAB_USER" >/dev/null
fi

step "Building agentbox from $(git rev-parse --short HEAD)"
go build -o bin/agentbox ./cmd/agentbox
if ! sudo cmp -s bin/agentbox "$bin"; then
  sudo install -m755 bin/agentbox "$bin"
  sudo ln -sf "$bin" /usr/local/bin/agentbox-lab
  stop_daemon
fi

if ! sudo incus storage show default >/dev/null 2>&1; then
  step "Setting Incus up for $LAB_USER"
  sudo incus storage create default dir >/dev/null
fi
if ! as_lab "incus list -f csv >/dev/null 2>&1"; then
  as_lab "sudo AGENTBOX_FRONT_END=host $bin host setup" >/dev/null
  trim_subids
fi

if ! pgrep -u "$LAB_USER" -x agentbox-lab >/dev/null; then
  step "Starting the lab's daemon"
  # As AgentBox's VM runs it (hostos.InVM): agents' memory.max, io.weight
  # and heavy runs' own cgroups are written through sudo, zram and the
  # daemon's own protection are set up. A daemon on a machine of its own
  # (host mode) does none of that, and its gate can't pause anything.
  as_lab "cd ~ && AGENTBOX_HOST_OS=${LAB_HOST_OS:-linux} setsid nohup $bin daemon >> ~/daemon.out 2>&1 < /dev/null &"
  for _ in $(seq 30); do sudo test -S "$socket" && break; sleep 1; done
fi

if ! as_lab "agentbox-lab auth claude list 2>/dev/null | grep -q default"; then
  # Agents start only with a Claude Code login. A placeholder: Claude Code
  # runs and idles as it does in a real agent, and a chat message's turn
  # fails at the API rather than spending anything.
  as_lab "echo sk-ant-oat01-loadlab-placeholder-not-a-token | agentbox-lab auth claude --token-stdin" >/dev/null
fi

if ! sudo incus info agentbox-base >/dev/null 2>&1; then
  step "Building the base image (minutes)"
  as_lab "agentbox-lab image build" | tail -3
fi

repo=$labhome/loadlab-repo
if ! sudo test -d "$repo/.git"; then
  step "Cloning this checkout for the lab's project"
  tmp=$(mktemp -d "$HOME/loadlab-repo.XXXX")
  git clone -q --depth 1 "file://$PWD" "$tmp/repo"
  sudo mv "$tmp/repo" "$repo" && rmdir "$tmp"
  sudo chown -R "$LAB_USER:$LAB_USER" "$repo"
fi

out=${LAB_OUT:-$PWD/loadlab-$(date +%Y%m%d-%H%M%S)}
step "Running the lab: ${LAB_AGENTS:-30} agents for ${LAB_DURATION:-10m}, report in $out"
run=(sudo -E env "PATH=$PATH" "HOME=$HOME" "LAB_SOCKET=$socket" "LAB_REPO=$repo" "LAB_OUT=$out"
  "LAB_DAEMON_LOG=$labhome/daemon.out"
  go test -tags pressurelab -run '^TestLoadLab$' -count=1 -v -timeout 6h ./internal/agent)
# Inside an agent, the lab is itself heavy: it asks this agent's daemon too.
if [[ -S /run/agentbox.sock ]] && command -v agentbox >/dev/null && [[ ${LAB_OUTER_HEAVY:-1} == 1 ]]; then
  run=(agentbox heavy -- "${run[@]}")
fi
"${run[@]}" 2>&1 | tee "${out}.log" | grep -E '^(=== RUN|--- |    loadlab_internal_test.go:[0-9]+: (made|prepared|making|stopping|target)|FAIL|ok )' || true
[[ -f $out/report.md ]] && cat "$out/report.md"
