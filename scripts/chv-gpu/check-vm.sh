#!/usr/bin/env bash
# Checks, on a Linux machine with KVM and a GPU, that AgentBox's own VM gets
# the GPU through "GPU for agents", end to end, with this branch's AgentBox:
#
#   scripts/chv-gpu/check-vm.sh [TARBALL]
#
# TARBALL is the Cloud Hypervisor build (build.sh's, or the Cloud Hypervisor
# workflow's artifact: by default out/chv-*.tar.gz, or else the last run's
# artifact on this branch, through gh). The agentbox built here pins it
# (pin.sh, on a copy of tools.go put back after) and installs it from the
# file (AGENTBOX_CHV_TARBALL), published or not. It turns "GPU for agents" on,
# restarts the VM (twice when it needs Debian's generic kernel: the first
# start installs it, the second boots it), and prints what the VM, and an
# agent if you name one (AGENT=project/agent), render on. Restarting the VM
# stops every agent, and the VM runs this branch's agentbox afterwards, as
# any build you run does.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
tarball=${1:-$(ls "$here"/out/chv-*.tar.gz 2>/dev/null | head -n1 || true)}
if [ -z "$tarball" ]; then
	echo "==> Fetching the Cloud Hypervisor build from this branch's last workflow run"
	branch=$(git -C "$root" rev-parse --abbrev-ref HEAD)
	run=$(gh run list -w chv.yml -b "$branch" -s success -L 1 --json databaseId -q '.[0].databaseId')
	[ -n "$run" ] || { echo "no successful Cloud Hypervisor run on $branch: run build.sh, or pass a tarball" >&2; exit 1; }
	gh run download "$run" -n chv -D "$here/out"
	tarball=$(ls "$here"/out/chv-*.tar.gz | head -n1)
fi
tarball=$(cd "$(dirname "$tarball")" && pwd)/$(basename "$tarball")

echo "==> Building agentbox, pinning this tarball for the build"
cp "$root/internal/hostvm/chv/tools.go" "$root/internal/hostvm/chv/tools.go.check-vm"
trap 'mv -f "$root/internal/hostvm/chv/tools.go.check-vm" "$root/internal/hostvm/chv/tools.go"' EXIT
"$here/pin.sh" "$tarball"
(cd "$root" && go build -o bin/agentbox ./cmd/agentbox)
mv -f "$root/internal/hostvm/chv/tools.go.check-vm" "$root/internal/hostvm/chv/tools.go"
trap - EXIT
ab=$root/bin/agentbox
data=${XDG_DATA_HOME:-$HOME/.local/share}/agentbox
sock=${AGENTBOX_SOCKET:-$data/run/agentbox.sock}
gpujson=$data/vm/${AGENTBOX_VM:-agentbox}/gpu.json

read -r -p "This restarts AgentBox's VM, once or twice, which stops every agent. Go on? [y/N] " yes
[ "$yes" = y ] || [ "$yes" = Y ] || exit 1

export AGENTBOX_CHV_TARBALL=$tarball
restart() {
	echo "==> Restarting the VM"
	"$ab" vm stop
	"$ab" vm start
}
field() { sed -n "s/.*\"$1\": *\"\\{0,1\\}\\([^\",]*\\)\"\\{0,1\\}.*/\\1/p" "$gpujson" 2>/dev/null; }

"$ab" vm start
echo "==> Turning on \"GPU for agents\""
curl -fsS --unix-socket "$sock" -X PATCH -H 'Content-Type: application/json' -d '{"agentGPU":true}' http://agentbox/v1/settings >/dev/null
echo "==> Waiting for the VM's supervisor to see it (it asks every 15 s)"
for _ in $(seq 60); do [ "$(field want)" = true ] && break; sleep 2; done
# The first restart gives the VM the GPU, or (on the cloud kernel) installs
# the generic kernel while it runs; the second boots that kernel.
for pass in 1 2; do
	restart
	echo "==> Waiting for the supervisor to say what the VM has (up to 20 minutes: a new kernel installs first)"
	for _ in $(seq 240); do
		st=$(field state)
		[ "$st" = on ] || [ "$st" = none ] && break
		[ "$st" = restart ] && ! grep -q installing "$gpujson" && break
		sleep 5
	done
	[ "$(field state)" = restart ] || break
done
echo
cat "$gpujson"
echo
"$ab" vm gpu ${AGENT:+--agent "$AGENT"}
echo
echo "The VM's log, for the GPU: grep -E 'GPU|crosvm|kernel' $(dirname "$gpujson")/vm.log"
[ "$(field state)" = on ]
