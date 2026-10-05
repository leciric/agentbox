#!/bin/sh
# A machine's first process, as root: makes the user commands run as (the
# host user's uid, so what they write in the worktree is the user's), starts
# Docker for a project that wants it inside, says it's ready, and stays.
set -eu

uid=${MACHINE_UID:-0}
gid=${MACHINE_GID:-0}
if [ "$uid" != 0 ]; then
  getent group "$gid" >/dev/null || groupadd -g "$gid" machine
  getent passwd "$uid" >/dev/null || useradd -u "$uid" -g "$gid" -d /home/machine -M -s /bin/bash machine
  chown "$uid:$gid" /home/machine
  echo "#$uid ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/machine
fi

if [ "${MACHINE_DOCKERD:-}" = 1 ]; then
  # kind's nodes each watch a lot of files.
  sysctl -qw fs.inotify.max_user_watches=524288 fs.inotify.max_user_instances=512 || true
  dockerd >/var/log/dockerd.log 2>&1 &
  i=0
  until [ -S /var/run/docker.sock ] || [ $i -ge 60 ]; do sleep 0.5; i=$((i + 1)); done
  chmod 666 /var/run/docker.sock || echo "dockerd didn't start: see /var/log/dockerd.log" >&2
fi

touch /run/machine-ready
exec sleep infinity
