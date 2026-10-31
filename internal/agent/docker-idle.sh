#!/bin/sh
# Stops this machine's Docker once it has been idle for idle_after seconds: no
# container running and no client on its socket (a build, `docker logs -f`,
# an attached `compose up`). docker.socket stays, and starts Docker again on
# the next docker command. agentbox-docker-idle.timer runs it every minute,
# as root (dockerondemand.go).
set -u

idle_after=${AGENTBOX_DOCKER_IDLE:-300}
# since is when it was first seen idle; /run, so a reboot forgets it.
since=${AGENTBOX_DOCKER_IDLE_SINCE:-/run/agentbox-docker-idle}

if ! systemctl is-active --quiet docker.service; then
  rm -f "$since"
  exit 0
fi
# Before docker ps, whose own connection would count.
if [ -n "$(ss -xH state connected src /run/docker.sock 2>/dev/null)" ]; then
  rm -f "$since"
  exit 0
fi
# A Docker that doesn't answer is left as it is.
running=$(timeout 30 docker ps -q 2>/dev/null) || exit 0
if [ -n "$running" ]; then
  rm -f "$since"
  exit 0
fi
if [ ! -e "$since" ]; then
  touch "$since"
  exit 0
fi
[ $(($(date +%s) - $(stat -c %Y "$since"))) -ge "$idle_after" ] || exit 0
rm -f "$since"
echo "idle for ${idle_after}s: stopping Docker until it is next used"
systemctl stop docker.service containerd.service
