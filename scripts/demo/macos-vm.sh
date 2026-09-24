#!/usr/bin/env bash
# AgentBox's VM front end (internal/hostvm, D92) against a real Lima VM, end to
# end. On a Mac it is the real thing: vz, Apple's virtiofs, the macOS agentbox.
# On Linux it stands in for a Mac: the front end is turned on with
# AGENTBOX_FRONT_END=vm, and Lima runs the VM on QEMU. Either way everything is
# kept apart from this machine's own AgentBox: its own VM, state, config,
# socket and preview port. The VM is deleted at the end unless KEEP=1.
#
# Needs Lima 2.0+ (limactl on PATH, or AGENTBOX_LIMACTL), Go, jq and curl, and
# KVM on Linux. Takes about ten minutes: Lima downloads Debian, host setup
# installs Incus in the VM, and the base image is built.
#
#   scripts/demo/macos-vm.sh
#
# Written for macOS's own bash 3.2 as well as bash 5.
set -uo pipefail
cd "$(dirname "$0")/../.."

# Under the home directory: it's the only one the VM has.
mkdir -p "$HOME/.cache"
work=$(mktemp -d "$HOME/.cache/agentbox-vm-demo.XXXXXX")
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data"
export AGENTBOX_VM=agentbox-demo AGENTBOX_PREVIEW_ADDR=127.0.0.1:17777
if [[ "$(uname -s)" == Linux ]]; then
  export AGENTBOX_FRONT_END=vm AGENTBOX_VM_TYPE=qemu
fi
AGENTBOX_LIMACTL=${AGENTBOX_LIMACTL:-$(command -v limactl || true)}
export AGENTBOX_LIMACTL
[[ -x "$AGENTBOX_LIMACTL" ]] || { echo "no limactl: install Lima 2.0+ (brew install lima), or set AGENTBOX_LIMACTL" >&2; exit 1; }
for tool in go jq curl python3; do
  command -v "$tool" >/dev/null || { echo "this needs $tool" >&2; exit 1; }
done
# The front end for this OS, and the Linux agentbox it installs in the VM,
# for this machine's architecture (the VM runs the host's own).
arch=$(go env GOARCH)
go build -o "$work/agentbox" ./cmd/agentbox || exit 1
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -o "$work/agentbox-linux" ./cmd/agentbox || exit 1
AB=$work/agentbox
source scripts/demo/lib.sh

# Portable versions of what lib.sh and the checks lean on, for macOS: bash 3.2
# has no EPOCHREALTIME, and BSD stat and script take other flags.
seconds() {
  local start end
  start=$(python3 -c 'import time; print(time.time())')
  "$@" >/dev/null 2>&1
  end=$(python3 -c 'import time; print(time.time())')
  awk -v s="$start" -v e="$end" 'BEGIN { printf "%.1f", e - s }'
}
owner() { stat -c %u "$1" 2>/dev/null || stat -f %u "$1"; }
in_terminal() {
  if [[ "$(uname -s)" == Darwin ]]; then script -q /dev/null sh -c "$1"; else script -qec "$1" /dev/null; fi
}
finish() {
  [[ -n ${KEEP:-} ]] && { echo "KEEP=1: the VM $AGENTBOX_VM and $work stay"; return; }
  "$AB" vm delete --yes >/dev/null 2>&1
  rm -rf "$work"
}
trap finish EXIT
sock=$work/data/agentbox/run/agentbox.sock

section "Before the VM exists"
ab list
expect_match "a command before vm init says what to run" "$(q list 2>&1; "$AB" list 2>&1)" "isn't set up: run agentbox vm init"
expect_match "vm status --json says there's no VM" "$(q vm status --json)" '"exists":false'
expect "--version answers on the host" "$(q --version)" "agentbox version dev"

section "agentbox vm init"
init_s=$(seconds "$AB" vm init --cpus 4 --memory 6GiB --disk 60GiB)
echo "vm init: ${init_s}s"
expect_match "vm init made the VM and it runs" "$(q vm status)" "^agentbox-demo: Running"
expect_match "the daemon's socket answers on the host" "$(curl -s --unix-socket "$sock" http://agentbox/v1/version)" '"version":"dev"'
setup=$(curl -s --unix-socket "$sock" http://agentbox/v1/setup)
expect_match "Incus is set up inside the VM" "$(jq -r '.checks[] | select(.id=="incus") | .status' <<<"$setup")" "^ok$"
expect_match "so is the user mapping" "$(jq -r '.checks[] | select(.id=="host") | .status' <<<"$setup")" "^ok$"
expect_match "Android is off, with no fix offered" "$(jq -r '.checks[] | select(.id=="android") | "\(.detail)|\(.fix // "")"' <<<"$setup")" "runs in a VM on a Mac[|]$"

section "The base image, from the host"
image_s=$(seconds "$AB" image build)
echo "image build: ${image_s}s"
expect_match "the base image is ready" "$(curl -s --unix-socket "$sock" http://agentbox/v1/setup | jq -r '.checks[] | select(.id=="image") | .status')" "^ok$"

section "A project and an agent, from the project's own folder"
fixture_repo hello-stack "$work/projects/hello"
(cd "$work/projects/hello" && "$AB" add . >/dev/null 2>&1)
expect_match "agentbox add . resolved the folder inside the VM" "$(q projects 2>/dev/null || q project list 2>/dev/null)" "hello"
create_s=$(seconds "$AB" create hello --ai none)
echo "create: ${create_s}s"
ab list
expect_match "the agent runs" "$(q list)" "hello/agent-01.*running"
wt=$(q path hello/agent-01)
expect "its worktree is on the host, under the host's data directory" "$wt" "$work/data/agentbox/worktrees/hello/agent-01"
q exec hello/agent-01 -- 'echo from-the-agent > written-in-agent.txt'
expect "a file the agent wrote is on the host" "$(cat "$wt/written-in-agent.txt" 2>/dev/null)" "from-the-agent"
expect "and it's the host user's" "$(owner "$wt/written-in-agent.txt")" "$(id -u)"
echo "from-the-host" >"$wt/written-on-host.txt"
expect "a file written on the host is in the agent" "$(q exec hello/agent-01 -- cat written-on-host.txt)" "from-the-host"
"$AB" exec hello/agent-01 -- 'exit 7' >/dev/null 2>&1
expect "an exit status comes back through the VM" "$?" "7"
# The terminal Lima opens in the VM may echo a control character (^@) first.
expect_match "a terminal reaches the VM as a terminal" "$(in_terminal "$AB vm shell -- tty" | tr -d '\r')" "/dev/pts/[0-9]+"

# What only a Mac can answer: whether root in
# an agent can write in its worktree, on the share. Linux's virtiofsd refuses;
# Apple's may not. Either answer is a finding, so it's reported, not checked.
if q exec hello/agent-01 -- 'sudo sh -c "echo root > written-by-root.txt"' >/dev/null 2>&1 && [[ -f "$wt/written-by-root.txt" ]]; then
  root_write="yes, and the file is uid $(owner "$wt/written-by-root.txt") on the host"
else
  root_write="no: the share refuses it"
fi
echo "Root in the agent writing in its worktree: $root_write"

section "What the agent can reach"
python3 -m http.server 17788 --bind 127.0.0.1 --directory "$work" >/dev/null 2>&1 &
http=$!
sleep 1
expect "the VM reaches the host's loopback through Lima" "$(q vm shell -- curl -s -o /dev/null -w '%{http_code}' -m 5 http://192.168.5.2:17788/)" "200"
expect "an agent doesn't" "$(q exec hello/agent-01 -- "curl -s -o /dev/null -w '%{http_code}' -m 5 http://192.168.5.2:17788/ || true")" "000"
kill $http

section "Preview URLs, on the host"
q exec hello/agent-01 -- 'nohup python3 -m http.server 3000 >/dev/null 2>&1 &' >/dev/null
for _ in $(seq 1 20); do curl -sf -m 2 -o /dev/null "http://3000.agent-01.hello.localhost:17777/" && break; sleep 1; done
expect "an agent's port is at <port>.<agent>.<project>.localhost on the host" "$(curl -s -o /dev/null -w '%{http_code}' -m 5 http://3000.agent-01.hello.localhost:17777/)" "200"

section "Upgrading: a new Linux agentbox reaches the VM by itself"
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -ldflags "-X agentbox/internal/cli.version=demo-2" -o "$work/agentbox-linux" ./cmd/agentbox
q list >/dev/null
expect "the next command installed it" "$(q vm shell -- agentbox --version)" "agentbox version demo-2"

section "Stopping the VM, and the next command starting it"
"$AB" vm stop >/dev/null 2>&1
expect_match "vm stop stopped it" "$(q vm status)" "Stopped"
start_s=$(seconds "$AB" list)
echo "a command on a stopped VM, starting it: ${start_s}s"
expect_match "a command started it again" "$(q vm status)" "Running"
expect_match "and the agent is there" "$(q list)" "hello/agent-01"
q start hello/agent-01 >/dev/null
expect_match "and starts" "$(q list)" "hello/agent-01.*running"

section "Destroy"
q destroy --force hello/agent-01 >/dev/null
expect "no agents left" "$(q list | grep -c agent-01)" "0"

echo
echo "Root in the agent writing in its worktree: $root_write"
summary
