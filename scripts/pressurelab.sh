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
#   LAB_JOBS=1 LAB_IO_MAX=40                     # one job per build; hold back over 40% IO PSI
#   LAB_MIX=memhog=1,cpuhog=1,forkbomb=1,go=1 LAB_AGENTS=4 scripts/pressurelab.sh
#                                                # the caps' run: swap, a CPU hog, a fork bomb
#   LAB_DAEMON_REF=main scripts/pressurelab.sh   # the daemon from another commit: a "before"
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
  # The ids that exist here are the map's first column (inside), not its
  # second (the parent's).
  top=$(awk '{ e = $1 + $3; if (e > m) m = e } END { print m }' /proc/self/uid_map)
  for f in /etc/subuid /etc/subgid; do
    sudo awk -F: -v top="$top" '!($2 + $3 > top)' "$f" | sudo tee "$f.lab" >/dev/null
    # Root needs a range of a container's size (Incus maps 65536 ids at
    # least), not just the one id a nested agent's setup gives it.
    awk -F: '$1 == "root" && $3 >= 65536 { ok = 1 } END { exit !ok }' "$f.lab" ||
      echo "root:1000000:$(( top - 1000000 < 1000000000 ? top - 1000000 : 1000000000 ))" | sudo tee -a "$f.lab" >/dev/null
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

# Incus's copies of the base (rsync on a dir pool, ~5 GiB an agent) alone
# take a shared VM's disk to 80% IO pressure: capped, LAB_INCUS_IO_MB a
# second each way (0 for none). On 2026-11-06 an uncapped 30-agent setup
# stalled the real Incus for 11 minutes, and an agent of another project with it.
cap_incus_io() {
  local mb=${LAB_INCUS_IO_MB:-40} src dev
  src=$(findmnt -no SOURCE -T /var/lib/incus | sed 's/\[.*//')
  dev=$(cat "/sys/class/block/$(basename "$src")/dev" 2>/dev/null) || return 0
  local cg=/sys/fs/cgroup/system.slice/incus.service/io.max
  [[ -e $cg ]] || return 0
  if [[ $mb == 0 ]]; then
    echo "$dev rbps=max wbps=max" | sudo tee "$cg" >/dev/null
  else
    echo "$dev rbps=$((mb << 20)) wbps=$((mb << 20))" | sudo tee "$cg" >/dev/null
  fi
}
cap_incus_io

if ! id "$LAB_USER" >/dev/null 2>&1; then
  step "Making the lab's user, $LAB_USER"
  sudo useradd -m -s /bin/bash "$LAB_USER"
  echo "$LAB_USER ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$LAB_USER" >/dev/null
fi

# LAB_DAEMON_REF runs the lab's daemon built from another commit (a "before"
# run), with this checkout's lab.
if [[ -n ${LAB_DAEMON_REF:-} ]]; then
  step "Building agentbox from $LAB_DAEMON_REF"
  src=$(mktemp -d "$HOME/pressurelab-ref.XXXX")
  git worktree add -q --detach "$src" "$LAB_DAEMON_REF"
  (cd "$src" && go build -o "$OLDPWD/bin/agentbox" ./cmd/agentbox)
  git worktree remove --force "$src"
else
  step "Building agentbox from $(git rev-parse --short HEAD)"
  go build -o bin/agentbox ./cmd/agentbox
fi
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
  # Until it answers: a daemon stopped above leaves its socket file behind.
  for _ in $(seq 60); do sudo curl -sf --unix-socket "$socket" http://agentbox/v1/projects >/dev/null && break; sleep 1; done
fi

if ! as_lab "agentbox-lab auth claude list 2>/dev/null | grep -q default"; then
  # Agents start only with a Claude Code login. A placeholder: Claude Code
  # runs and idles as it does in a real agent, and a chat message's turn
  # fails at the API rather than spending anything.
  as_lab "echo sk-ant-oat01-loadlab-placeholder-not-a-token | agentbox-lab auth claude --token-stdin" >/dev/null
fi

# A daemon that has none starts building it by itself as it starts: the lab
# waits for that build rather than failing on a second one.
image_job() { as_lab "agentbox-lab jobs 2>/dev/null" | awk '$2 == "image-build" && $4 == "running" { print $1; exit }'; }
for _ in $(seq 3); do
  sudo incus info agentbox-base >/dev/null 2>&1 && break
  step "Building the base image (minutes)"
  if job=$(image_job) && [[ -n $job ]]; then
    as_lab "agentbox-lab jobs $job" | tail -3 || true
  else
    as_lab "agentbox-lab image build" | tail -3 || true
  fi
done
sudo incus info agentbox-base >/dev/null 2>&1 || { echo "no base image" >&2; exit 1; }

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
if [[ -f $out/report.md ]]; then cat "$out/report.md"; fi
