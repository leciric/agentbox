#!/usr/bin/env bash
# Checks internal/image/personalise.sh against a real Debian 13 and a real
# usermod, without Incus (D43): it renames the base image's placeholder user to
# a host user, and getting that wrong would only show up inside an agent.
#
#   sudo scripts/check-personalise.sh
#
# It takes a Debian 13 root filesystem from Docker, sets it up the way
# provision.sh leaves the image (the placeholder user, its home, sudoers,
# subordinate IDs, stubs for the tools personalise.sh re-checks), and runs the
# script in a chroot once per case. The real thing is the base-image workflow.
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=${TMPDIR:-/tmp}/agentbox-personalise
[ "$EUID" -eq 0 ] || { echo "run it as root: sudo $0" >&2; exit 1; }

cleanup() { rm -rf "$work"; }
trap cleanup EXIT

echo "==> Taking a Debian 13 root filesystem from Docker"
rm -rf "$work" && mkdir -p "$work/image"
docker pull -q debian:13
id=$(docker create debian:13 true)
docker export "$id" | tar -x -C "$work/image"
docker rm "$id" >/dev/null
chmod 1777 "$work/image/tmp"
cp /etc/resolv.conf "$work/image/etc/resolv.conf"

echo "==> Setting it up the way provision.sh leaves the image"
cat >"$work/image/prepare.sh" <<'PREPARE'
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends sudo passwd >/dev/null
groupadd -g 1000 agent
useradd -m -u 1000 -g 1000 -s /bin/bash agent
getent group kvm >/dev/null || groupadd -r kvm
getent group docker >/dev/null || groupadd -r docker
usermod -aG sudo,docker,kvm agent
echo "agent ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/90-agentbox
chmod 0440 /etc/sudoers.d/90-agentbox
printf 'agent:100000:65536\n' >/etc/subuid
printf 'agent:100000:65536\n' >/etc/subgid
mkdir -p /home/agent/.claude /home/agent/.codex /home/agent/.config/agentbox
echo '{"skipDangerousModePermissionPrompt": true}' >/home/agent/.claude/settings.json
chown -R 1000:1000 /home/agent
# personalise.sh runs these as the renamed user; here they only have to exist.
for t in git go node pnpm claude codex gh playwright-mcp; do
  printf '#!/bin/sh\necho "%s (stub)"\n' "$t" >"/usr/local/bin/$t"
  chmod +x "/usr/local/bin/$t"
done
PREPARE
chroot "$work/image" bash /prepare.sh || exit 1
rm -f "$work/image/prepare.sh"

failures=0
# name uid gid want-exit what-it-covers
cases=(
  "dev 1000 1000 0 the usual host user: a rename only"
  "sam 1001 1001 0 a different UID and GID, both free in the image"
  "runner 1001 100 0 a GID the image already has (Debian's users): reused, not renamed"
  "agent 1000 1000 0 a host user that is already the placeholder: nothing to do"
  "staff 1002 1002 0 a name the image already has a group for: the GID still moves"
  "clash 33 33 1 a UID the image gives a system account: refused, not deleted"
)
for c in "${cases[@]}"; do
  set -- $c
  user=$1 uid=$2 gid=$3 want=$4
  shift 4
  case_root=$work/case
  rm -rf "$case_root" && cp -a "$work/image" "$case_root"
  install -Dm755 "$root/internal/image/personalise.sh" "$case_root/scripts/personalise.sh"

  echo
  echo "==> $user ($uid:$gid): $*"
  chroot "$case_root" /scripts/personalise.sh agent "$user" "$uid" "$gid" 2>&1 | sed 's/^/    /'
  got=${PIPESTATUS[0]}
  if [ "$got" -ne "$want" ]; then
    echo "    FAILED: exit $got, wanted $want"
    failures=$((failures + 1))
    continue
  fi
  [ "$want" -eq 0 ] || { echo "    refused, as it should be"; continue; }

  # What has to be true for an agent made from this image to work: the user is
  # there with the host's IDs, its home is where AgentBox writes, and nothing
  # in the home directory is left behind under the placeholder's ownership.
  ids=$(chroot "$case_root" sh -c "getent passwd '$user' | cut -d: -f3,4,6")
  stray=$(chroot "$case_root" find "/home/$user" ! -uid "$uid" -o ! -gid "$gid" | head -1)
  subordinate=$(chroot "$case_root" grep -c "^$user:" /etc/subuid)
  want_ids="$uid:$gid:/home/$user"
  if [ "$ids" != "$want_ids" ]; then
    echo "    FAILED: the user is ${ids:-missing}, wanted $want_ids"
    failures=$((failures + 1))
  elif [ -n "$stray" ]; then
    echo "    FAILED: $stray isn't $user's"
    failures=$((failures + 1))
  elif ! chroot "$case_root" grep -q "^$user " /etc/sudoers.d/90-agentbox || [ "$subordinate" != "1" ]; then
    echo "    FAILED: sudoers or the subordinate IDs still name the placeholder"
    failures=$((failures + 1))
  else
    echo "    ok: $want_ids, its home, its sudo and its subordinate IDs are $user's"
  fi
done

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures of ${#cases[@]} cases failed"
  exit 1
fi
echo "${#cases[@]} of ${#cases[@]} cases passed"
