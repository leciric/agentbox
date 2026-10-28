# The daemon as a container: the control plane on its own, with no machine of
# its own to run agents in. Its agents are pods in a Kubernetes cluster it
# drives, so this image carries no Incus, no Docker and no AI tool: just the
# binary, git for the repositories it reads, and a CA bundle for the registries
# and hubs it talks to.
#
#   go build -o bin/agentbox ./cmd/agentbox
#   docker build -f packaging/daemon/Dockerfile -t <registry>/<owner>/agentbox-daemon:dev .
#
# Run it with AGENTBOX_FRONT_END=host, so it serves instead of asking for the
# Linux VM a desktop install would use, and with XDG_DATA_HOME pointing at a
# volume for state.db, the secrets key and the daemon's socket:
#
#   docker run -d --network host -e AGENTBOX_FRONT_END=host \
#     -e AGENTBOX_GATEWAY_ADDR=0.0.0.0:7782 \
#     -e XDG_DATA_HOME=/state -v /mnt/Safe/agentbox:/state <image>
#
# --network host is what lets `tailscale serve` on the host publish the
# daemon's tailnet listener at 127.0.0.1:7781, and what puts the agent gateway
# on the host's own 7782 for pods to dial.
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      git \
      openssh-client \
 && rm -rf /var/lib/apt/lists/*

COPY bin/agentbox /usr/local/bin/agentbox

# The dataset is root's on the NAS, and nothing here runs as a user of its own:
# there is no machine to install tools into.
ENV HOME=/root \
    XDG_DATA_HOME=/state \
    AGENTBOX_FRONT_END=host
VOLUME ["/state"]
EXPOSE 7782

ENTRYPOINT ["/usr/local/bin/agentbox", "daemon"]
