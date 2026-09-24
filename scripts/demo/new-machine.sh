#!/usr/bin/env bash
# Checks docs/setup.md on a new machine: a fresh Debian 13 VM (Incus), with a
# user who has never used AgentBox. It follows the guide's terminal path, with
# the command-line tool from the AppImage, up to an agent with its browser.
# The Claude Code login is left out: it needs your own account.
#
#   npm --prefix desktop run dist
#   scripts/demo/new-machine.sh > .demo-runs/new-machine/demo.log 2>&1
set -uo pipefail
exec </dev/null

root=$(cd "$(dirname "$0")/../.." && pwd)
version=$(node -p "require('$root/desktop/package.json').version")
appimage=AgentBox-$version-x86_64.AppImage
vm=abx-new-machine
work=$(mktemp -d)
results=()
started=$SECONDS

step() { printf '\n######## [%4ss] %s\n' "$((SECONDS - started))" "$*"; }
check() { # check <name> <command that succeeds when it holds>
  if eval "$2" >/dev/null 2>&1; then results+=("PASS  $1"); echo "PASS $1"; else results+=("FAIL  $1"); echo "FAIL $1"; fi
}
# as_dev runs a command in a new login shell of the user, as a terminal would
# after logging in, and prints it.
as_dev() {
  printf '\n$ %s\n' "$*"
  incus exec "$vm" -- su - dev -c "$*" 2>&1 | tee "$work/last"
  return "${PIPESTATUS[0]}"
}

cp "$root/desktop/dist/$appimage" "$work/"
cp -r "$root/testdata/fixtures/hello-stack" "$work/hello-stack"

step "A fresh Debian 13 VM: 4 CPUs, 8 GiB of memory"
incus delete --force "$vm" >/dev/null 2>&1
incus launch images:debian/13 "$vm" --vm -c limits.cpu=4 -c limits.memory=8GiB -d root,size=80GiB
for _ in $(seq 120); do incus exec "$vm" -- getent hosts deb.debian.org >/dev/null 2>&1 && break; sleep 2; done
incus exec "$vm" -- sh -c '. /etc/os-release; echo "$PRETTY_NAME, kernel $(uname -r), $(nproc) CPUs, $(free -h | awk "/Mem/{print \$2}") of memory"; command -v incus || echo "no incus installed"'

step "A user, dev, who can use sudo (like a new laptop's first account)"
incus exec "$vm" -- sh -c 'apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sudo git ca-certificates >/dev/null &&
  useradd -m -u 1000 -s /bin/bash -G sudo dev && echo "dev ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/dev && id dev'
incus file push "$work/$appimage" "$vm/home/dev/Downloads/$appimage" --create-dirs --uid 1000 --gid 1000 --mode 0644
incus file push -r "$work/hello-stack" "$vm/home/dev/" --uid 1000 --gid 1000
incus exec "$vm" -- chown -R dev:dev /home/dev/hello-stack /home/dev/Downloads

step "Guide step 1: install the command-line tool from the AppImage"
as_dev "chmod +x ~/Downloads/$appimage && cd /tmp && ~/Downloads/$appimage --appimage-extract resources/bin/agentbox >/dev/null && install -Dm755 squashfs-root/resources/bin/agentbox ~/.local/bin/agentbox && rm -rf squashfs-root"
as_dev "command -v agentbox && agentbox --version"
check "a new login shell finds agentbox $version in ~/.local/bin" "grep -q 'agentbox version $version' $work/last"
as_dev "agentbox host check"

step "Guide step 2: host setup (installs Incus; needs sudo)"
as_dev 'sudo "$(command -v agentbox)" host setup'
check "host setup finishes" "grep -q 'Done. dev ' $work/last"
check "it says no logout is needed" "grep -q 'no need to log out' $work/last"

step "Guide step 3: the same session can use Incus, without logging out"
# The group would need a new login; the ACL on the socket is by UID, so it
# applies to this session and to processes already running.
printf '\n$ getfacl /var/lib/incus/unix.socket\n'
incus exec "$vm" -- sh -c 'getfacl -p /var/lib/incus/unix.socket 2>&1 | grep -vE "^$"' 2>&1 | tee "$work/last"
check "the socket has an ACL for dev" "grep -q '^user:dev:rw' $work/last"
# --user 1000 is a session with dev's own group only: no incus-admin, like the
# terminal that ran host setup a moment ago.
printf '\n$ (a session from before host setup) incus version; agentbox host check\n'
incus exec "$vm" --user 1000 --group 1000 --cwd /home/dev --env HOME=/home/dev --env USER=dev \
  -- sh -lc 'id -nG; incus version; PATH=$HOME/.local/bin:$PATH agentbox host check' 2>&1 | tee "$work/last"
check "dev is not in incus-admin in that session" "! grep -qw incus-admin $work/last"
check "and can use Incus anyway, through the ACL" "grep -q 'Client version' $work/last"
check "Incus and the user mapping are ready, with no new login" "grep -q '✓  Incus' $work/last && grep -q '✓  User mapping' $work/last"

step "The ACL survives an Incus restart (the systemd drop-ins put it back)"
printf '\n$ systemctl restart incus.socket incus.service; getfacl /var/lib/incus/unix.socket\n'
incus exec "$vm" -- sh -c 'systemctl restart incus.socket incus.service; sleep 2; getfacl -p /var/lib/incus/unix.socket 2>&1 | grep -vE "^$"' 2>&1 | tee "$work/last"
check "the ACL is back after restarting incus" "grep -q '^user:dev:rw' $work/last"

step "A new login has the group as well"
as_dev "id -nG | tr ' ' '\n' | grep -x incus-admin && incus version >/dev/null && echo 'incus works here too'"
check "after logging in again, dev is in incus-admin" "grep -qx incus-admin $work/last"

step "Guide step 4: build the base image (the app's Setup page has a button for this)"
as_dev "time agentbox image build 2>&1 | grep -E '^==>|ready in|error'"
as_dev "agentbox host check"
check "the base image is ready" "grep -q '✓  Base image' $work/last"

step "Guide step 5: add a project and create an agent"
as_dev "cd ~/hello-stack && git init -q -b main && git add -A && git -c user.name=dev -c user.email=dev@example.com commit -qm 'first commit' && agentbox add ~/hello-stack"
as_dev "agentbox create hello-stack --ai none --title 'First agent' 2>&1 | tail -n 3"
as_dev "agentbox list"
check "the agent is running" "grep -q 'agent-01.*running' $work/last"
as_dev "agentbox exec hello-stack/agent-01 -- 'hostname; git branch --show-current; node --version; docker --version'"
check "commands run inside the agent, on its branch" "grep -q 'ab-hello-stack-agent-01' $work/last && grep -q 'agentbox/agent-01' $work/last"
as_dev "agentbox browser open hello-stack/agent-01 about:blank && agentbox media screenshot hello-stack/agent-01 --name first-look && agentbox media list hello-stack/agent-01"
check "its browser runs, and a screenshot lands in its media" "grep -q 'first-look' $work/last"

step "Clean up"
as_dev "agentbox destroy hello-stack/agent-01 --force --delete-branch && agentbox daemon stop"
incus delete --force "$vm"
rm -rf "$work"

printf '\n######## Summary (%ss)\n' "$((SECONDS - started))"
printf '%s\n' "${results[@]}"
passed=$(printf '%s\n' "${results[@]}" | grep -c '^PASS')
echo "$passed/${#results[@]} checks passed"
[ "$passed" -eq "${#results[@]}" ]
