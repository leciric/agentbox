#!/usr/bin/env bash
# AgentBox host setup. Run once, as root:
#
#   sudo "$(command -v agentbox)" host setup
#   sudo bash scripts/host-setup.sh            (from a checkout)
#
# The app's Setup page runs it through pkexec, which asks for the password in
# the desktop's own dialog. sudo says who ran it in SUDO_USER, pkexec in
# PKEXEC_UID, and --user names that user when neither is set.
#
# Safe to run again. It installs Incus (Arch, Debian/Ubuntu or Fedora), gives
# your user access to it — in the session running now, not only the next login
# — creates a btrfs storage pool and a bridge network, and stops ufw and
# Docker's firewall policy from blocking the Incus bridge.
set -euo pipefail

# Whether this is an agent whose base image was built with Incus in it
# (agentbox image build --incus, nesting.go), rather than a real host: its own
# idmap only covers so much, and a slow storage pool is what it needs rather
# than a warning nobody but this container's own agent would see. provision.sh
# writes the marker when it installs Incus for exactly this reason.
nested=no
[[ -e /etc/agentbox-nested-incus ]] && nested=yes

# Where the Incus daemon listens for local clients, and what the incus command
# connects to when INCUS_SOCKET isn't set. It is Incus' own var path rather than
# a packaging choice: Debian's incus.socket unit is
# `ListenStream=/var/lib/incus/unix.socket`, with `SocketGroup=incus-admin` and
# `SocketMode=0660` — which is the permission check, since Incus trusts whoever
# can connect to this socket.
socket=/var/lib/incus/unix.socket

usage() {
  cat >&2 <<'EOF'
usage: host-setup.sh [--user <name>] [--bridge-subnet <address>/<prefix>]

Sets this machine up for the user who ran sudo (SUDO_USER) or pkexec
(PKEXEC_UID). --user names that user when neither of those says. Run it as root.
--bridge-subnet gives Incus's bridge that address and subnet, when it is made,
instead of letting Incus pick one.
EOF
}

flag_user=""
flag_bridge_subnet=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --user)
      [[ $# -ge 2 ]] || { echo "--user needs a user name" >&2; exit 2; }
      flag_user="$2"
      shift 2
      ;;
    --user=*)
      flag_user="${1#--user=}"
      shift
      ;;
    --bridge-subnet)
      [[ $# -ge 2 ]] || { echo "--bridge-subnet needs an address/prefix" >&2; exit 2; }
      flag_bridge_subnet="$2"
      shift 2
      ;;
    --bridge-subnet=*)
      flag_bridge_subnet="${1#--bridge-subnet=}"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done
if [[ -n $flag_bridge_subnet && ! $flag_bridge_subnet =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$ ]]; then
  echo "--bridge-subnet wants an IPv4 address and prefix, like 10.87.0.1/24: $flag_bridge_subnet" >&2
  exit 2
fi

# Who ran this: sudo and pkexec each say, in their own way. Work it out before
# checking for root, so a run from a terminal reports the wrong user before it
# reports the missing sudo.
caller=""
if [[ -n "${SUDO_USER:-}" ]]; then
  caller="$SUDO_USER"
elif [[ -n "${PKEXEC_UID:-}" ]]; then
  [[ "$PKEXEC_UID" =~ ^[0-9]+$ ]] || { echo "PKEXEC_UID=$PKEXEC_UID is not a UID" >&2; exit 1; }
  caller_entry=$(getent passwd "$PKEXEC_UID") || { echo "PKEXEC_UID=$PKEXEC_UID is not a user on this machine" >&2; exit 1; }
  caller=$(cut -d: -f1 <<<"$caller_entry")
fi
if [[ -n "$caller" && -n "$flag_user" && "$caller" != "$flag_user" ]]; then
  echo "--user $flag_user is not who ran this ($caller): leave --user out to set up for $caller" >&2
  exit 1
fi

TARGET_USER="${caller:-$flag_user}"
if [[ -z "$TARGET_USER" ]]; then
  echo 'who is this for? run it with sudo: sudo "$(command -v agentbox)" host setup' >&2
  echo "       or name the user: host setup --user <name>" >&2
  exit 1
fi
# Everything below puts this name in /etc/subuid, a systemd unit and an ACL, so
# it has to be a plain user name and an account that exists.
[[ "$TARGET_USER" =~ ^[a-z_][a-z0-9_-]*\$?$ ]] || { echo "not a user name: $TARGET_USER" >&2; exit 1; }
passwd_entry=$(getent passwd "$TARGET_USER") || { echo "no such user: $TARGET_USER" >&2; exit 1; }
uid=$(cut -d: -f3 <<<"$passwd_entry")
gid=$(cut -d: -f4 <<<"$passwd_entry")
if [[ "$uid" -eq 0 ]]; then
  echo "host setup is for the user who runs AgentBox, not for root" >&2
  exit 1
fi

if [[ $EUID -ne 0 ]]; then
  echo "host setup is for $TARGET_USER, but it has to run as root:" >&2
  echo '  sudo "$(command -v agentbox)" host setup' >&2
  exit 1
fi

step() { printf '\n==> %s\n' "$*"; }

# add_zabbly_repository adds the repository the Incus project points Debian and
# Ubuntu users at, for releases whose own incus is too old or missing.
add_zabbly_repository() {
  local codename arch
  codename=$(. /etc/os-release && echo "${VERSION_CODENAME:-}")
  arch=$(dpkg --print-architecture)
  if [[ -z "$codename" ]]; then
    echo "no VERSION_CODENAME in /etc/os-release, so there's no Zabbly suite to pick" >&2
    return 1
  fi
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q curl ca-certificates
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://pkgs.zabbly.com/key.asc -o /etc/apt/keyrings/zabbly.asc
  chmod 0644 /etc/apt/keyrings/zabbly.asc
  cat >/etc/apt/sources.list.d/zabbly-incus-stable.sources <<EOF
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: $codename
Components: main
Architectures: $arch
Signed-By: /etc/apt/keyrings/zabbly.asc
EOF
}

# give_root_a_range gives root the billion IDs Incus maps containers into, in
# the /etc/subuid or /etc/subgid it's given, unless root has that many already.
# Debian's incus package gives root a range when it's installed, after the
# highest range it finds, so it's only 1000000 when nothing ends past that; and
# Incus maps each large range of root's to the container's ID 0, so a second
# one overlaps the first and no container starts.
#
# A nested agent (provision.sh's --incus, nesting.go) is different: its own
# idmap doesn't cover a real host's billion IDs, so it starts with a smaller
# range of its own, sized to what its own container actually has room for.
# Adding the usual billion-ID range on top would be the same two-ranges
# problem the size check above already guards against on a real host, so
# there $nested only relaxes the threshold to "any range at all" — not the
# range itself, which stays whatever provision.sh gave it.
give_root_a_range() {
  local min=1000000000
  [[ $nested == yes ]] && min=2
  awk -F: -v min="$min" '$1 == "root" && $3 >= min { found = 1 } END { exit !found }' "$1" ||
    echo 'root:1000000:1000000000' >>"$1"
}

step "Installing incus"
# acl comes with them: the socket ACL below is what lets the session running
# now use Incus, without logging in again.
if command -v pacman >/dev/null; then
  pacman -S --needed --noconfirm incus btrfs-progs acl
elif command -v apt-get >/dev/null; then
  DEBIAN_FRONTEND=noninteractive apt-get update -q
  # Debian 13 has incus 6.0 and Ubuntu 24.04 has 6.0 in universe. Debian 12 has
  # none (only backports) and Ubuntu 22.04 has none at all; 6.0 is also the
  # oldest with the `incus snapshot create` form AgentBox uses. Anything older
  # or missing comes from Zabbly instead.
  candidate=$(apt-cache policy incus 2>/dev/null | awk '/Candidate:/ { print $2 }')
  if [[ -z "$candidate" || "$candidate" == "(none)" ]] || ! dpkg --compare-versions "$candidate" ge 6.0; then
    echo "this release offers incus ${candidate:-none}, older than 6.0: adding the Zabbly repository"
    if add_zabbly_repository; then
      DEBIAN_FRONTEND=noninteractive apt-get update -q
    else
      echo "warning: could not add the Zabbly repository, trying this release's own incus" >&2
    fi
  fi
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q incus btrfs-progs acl
elif command -v dnf >/dev/null; then
  dnf install -y incus btrfs-progs acl
else
  echo "no pacman, apt-get or dnf here: install incus, btrfs-progs and acl yourself, then run this again" >&2
  command -v incus >/dev/null || exit 1
fi

step "Subordinate UID/GID ranges for root (required by Incus)"
for f in /etc/subuid /etc/subgid; do
  touch "$f"
  give_root_a_range "$f"
done
# Lets agents map your UID/GID 1:1 (raw.idmap), so files they write in the
# worktree are owned by you while container root stays unprivileged on the host.
grep -qx "root:$uid:1" /etc/subuid || echo "root:$uid:1" >>/etc/subuid
grep -qx "root:$gid:1" /etc/subgid || echo "root:$gid:1" >>/etc/subgid

step "Starting incus"
systemctl enable --now incus.socket
systemctl restart incus.service

step "Granting $TARGET_USER access to incus"
usermod -aG incus-admin "$TARGET_USER"
# Android emulators in agents need /dev/kvm, which most distributions give to this group.
if getent group kvm >/dev/null; then
  usermod -aG kvm "$TARGET_USER"
fi

# A group only applies to new logins, so on its own it would leave the session
# that ran this — the desktop you are looking at — unable to use Incus until
# you logged out. An ACL on the socket is by UID, so it applies at once, to
# processes already running. Incus recreates the socket when it restarts, and
# the new one has only the package's own permissions, so a systemd drop-in puts
# the ACL back: on incus.socket, which creates it, and on incus.service, in
# case the daemon is started on its own.
write_acl_dropin() { # write_acl_dropin <unit> <section>
  install -d -m 0755 "/etc/systemd/system/$1.d"
  cat >"/etc/systemd/system/$1.d/10-agentbox-$TARGET_USER.conf" <<EOF
# AgentBox: let $TARGET_USER use the Incus socket without logging in again.
# The socket is recreated with the package's own permissions when Incus
# restarts, so the ACL goes back on every start. Remove this file to undo it.
[$2]
ExecStartPost=-$setfacl -m u:$TARGET_USER:rw $socket
EOF
}

acl=no
setfacl=$(command -v setfacl || true)
if [[ -n "$setfacl" ]]; then
  write_acl_dropin incus.socket Socket
  write_acl_dropin incus.service Service
  systemctl daemon-reload
  if [[ -S "$socket" ]] && "$setfacl" -m "u:$TARGET_USER:rw" "$socket"; then
    acl=yes
  else
    echo "warning: could not put an ACL on $socket, so $TARGET_USER needs a new login" >&2
  fi
else
  echo "warning: no setfacl on this machine, so $TARGET_USER needs a new login" >&2
fi

step "Storage pool 'default' (btrfs)"
# WSL2's kernel builds btrfs as a module, on the modules disk WSL mounts at
# /lib/modules, so it has to be loaded, and loaded again at every boot. A WSL
# too old to carry that disk has no btrfs at all, and there the pool is a
# plain directory: agents work, but a fork or a base copies every file.
wsl=no
grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null && wsl=yes
if [[ $wsl == yes ]] && ! incus storage show default >/dev/null 2>&1; then
  modprobe btrfs 2>/dev/null && echo btrfs >/etc/modules-load.d/agentbox-btrfs.conf || true
  if ! grep -qw btrfs /proc/filesystems; then
    echo "warning: this WSL kernel has no btrfs: the pool is a directory, and forks and bases copy every file (wsl --update brings the modules disk)" >&2
    incus storage create default dir
  fi
fi
if ! incus storage show default >/dev/null 2>&1; then
  if [[ "$(findmnt -no FSTYPE -T /var/lib)" == btrfs ]] && command -v btrfs >/dev/null; then
    # A subvolume on the existing btrfs filesystem: no fixed size, instant CoW snapshots.
    [[ -d /var/lib/incus-pool ]] || btrfs subvolume create /var/lib/incus-pool
    if [[ $nested == yes ]]; then
      incus storage create default btrfs source=/var/lib/incus-pool ||
        incus storage create default btrfs size=60GiB ||
        incus storage create default dir
    else
      incus storage create default btrfs source=/var/lib/incus-pool ||
        incus storage create default btrfs size=60GiB
    fi
  elif [[ $nested == yes ]]; then
    # A loopback-backed btrfs image needs its own block device (losetup),
    # which a nested agent (provision.sh's --incus) has no access to: the
    # pool is a directory here, silently, the way it already is on a real
    # host whose btrfs fails outright — a real host's own failed loop device
    # is worth seeing, not papering over with a slower pool it never asked for.
    incus storage create default btrfs size=60GiB ||
      incus storage create default dir
  else
    incus storage create default btrfs size=60GiB
  fi
fi

step "Network bridge 'incusbr0'"
# Incus picks the bridge's subnet itself unless --bridge-subnet names one. It
# rules a subnet out when it's routed here or when an address in it answers a
# ping, which is what keeps it off a LAN or VPN range reached through the
# default route. Lima's user-mode network, which AgentBox's VM on a Mac runs on,
# answers a ping to anywhere, so there Incus finds nothing and the VM names one.
incus network show incusbr0 >/dev/null 2>&1 ||
  incus network create incusbr0 "ipv4.address=${flag_bridge_subnet:-auto}" ipv4.nat=true ipv6.address=none

step "Default profile devices"
incus profile device show default | grep -q '^root:' ||
  incus profile device add default root disk path=/ pool=default
incus profile device show default | grep -q '^eth0:' ||
  incus profile device add default eth0 nic network=incusbr0 name=eth0

step "ufw: allow the Incus bridge"
# With ufw active, containers get no DHCP/DNS from Incus and no forwarded traffic.
if command -v ufw >/dev/null && ufw status | grep -q 'Status: active'; then
  ufw allow in on incusbr0 comment 'incus bridge (agentbox)'
  ufw route allow in on incusbr0 comment 'incus bridge (agentbox)'
  ufw route allow out on incusbr0 comment 'incus bridge (agentbox)'
else
  echo "ufw not active, skipping"
fi

step "Docker coexistence"
# Docker sets the iptables FORWARD policy to DROP, which silently blocks traffic
# from Incus containers. Allow the Incus bridge every time Docker starts.
allow_rules=(
  "-i incusbr0 -j ACCEPT"
  "-o incusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT"
)
dropin=/etc/systemd/system/docker.service.d/10-incus-bridge.conf
mkdir -p "$(dirname "$dropin")"
{
  echo "[Service]"
  for rule in "${allow_rules[@]}"; do
    echo "ExecStartPost=-/bin/sh -c 'iptables -C DOCKER-USER $rule 2>/dev/null || iptables -I DOCKER-USER $rule'"
  done
} >"$dropin"
systemctl daemon-reload
if systemctl is-active --quiet docker.service; then
  for rule in "${allow_rules[@]}"; do
    # shellcheck disable=SC2086
    iptables -C DOCKER-USER $rule 2>/dev/null || iptables -I DOCKER-USER $rule ||
      echo "warning: could not add DOCKER-USER rule: $rule" >&2
  done
fi

step "Result"
incus storage list
incus network list
echo
if [[ "$acl" == yes ]]; then
  echo "Done. $TARGET_USER can use Incus now, in this session too: no need to log out."
  echo "(An ACL on $socket, put back whenever Incus restarts. $TARGET_USER is in"
  echo " incus-admin as well, which takes over at the next login.)"
else
  echo "Done. $TARGET_USER is in incus-admin: log out and back in for it to apply"
  echo "(or run commands through: echo '<command>' | newgrp incus-admin)."
fi
