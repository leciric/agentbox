#!/usr/bin/env bash
# Benchmarks AgentBox's agents as today's Incus containers against AgentBox in
# one Cloud Hypervisor VM, on this host, and writes one markdown table and the
# raw JSON. README.md next to this says what it needs and what it does.
#
#   scripts/bench/run.sh                                  # containers, then the Cloud Hypervisor VM (agentbox vm)
#   scripts/bench/run.sh --modes containers,vm,chproto    # and the prototype that drives Cloud Hypervisor directly
#   scripts/bench/run.sh --quick                          # a short run, to check it works here
#   scripts/bench/run.sh --cleanup --modes containers,vm
#
# Ctrl-C stops it and removes what it made; a second Ctrl-C abandons that
# cleanup, which --cleanup then finishes.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
export BENCH_REPO="${BENCH_REPO:-$(git -C "$here" rev-parse --show-toplevel)}"

if [[ $(id -u) == 0 ]]; then
  echo "run.sh: run it as the user who runs AgentBox, not root: it asks for sudo itself when it needs it" >&2
  exit 2
fi
if ! command -v go >/dev/null; then
  echo "run.sh: needs Go to build the harness: mise install in the repository, or install Go" >&2
  exit 2
fi

# The prototype VM's tap is the one thing that needs root: ask for sudo now,
# before anything starts, and keep it fresh until the harness has cleaned up.
modes="containers,vm" help="" cleanup=""
for ((i = 1; i <= $#; i++)); do
  case "${!i}" in
    --modes=*|-modes=*) modes="${!i#*=}" ;;
    --modes|-modes) j=$((i + 1)); modes="${!j:-}" ;;
    -h|-help|--help) help=1 ;;
    --cleanup|-cleanup) cleanup=1 ;;
  esac
done
tap=""
ip link show abbench0 >/dev/null 2>&1 && tap=1
need_sudo=""
if [[ -z $help && ,$modes, == *,chproto,* ]]; then
  # A run makes the tap; a cleanup deletes it.
  if [[ -z $cleanup && -z $tap ]] || [[ -n $cleanup && -n $tap ]]; then
    need_sudo=1
  fi
fi
keepalive=""
if [[ -n $need_sudo ]]; then
  echo "The prototype VM's network (a tap on the Incus bridge) needs sudo, once:"
  sudo -v
  (while sleep 50; do sudo -n -v 2>/dev/null || exit; done) &
  keepalive=$!
fi
trap '[[ -n $keepalive ]] && kill "$keepalive" 2>/dev/null; true' EXIT

bin="${XDG_CACHE_HOME:-$HOME/.cache}/agentbox-bench/bench"
mkdir -p "$(dirname "$bin")"
(cd "$here" && go build -o "$bin" .)
# Not exec: Ctrl-C reaches the harness, which cleans up, and this shell waits
# for it before its trap lets sudo go.
status=0
"$bin" "$@" || status=$?
exit "$status"
