# The daemon as a container: the control plane on its own, with no machine of
# its own to run agents in. Its agents are pods in a Kubernetes cluster it
# drives, so this image carries no Incus, no Docker and no agent's desktop:
# just the binary and the system commands it runs.
#
# The AI tools are not baked in, but the project's chat does install them: it
# runs on this container's host, and its first use downloads Claude Code, Node
# and the GitHub CLI into the volume (internal/agent/hosttools.go). That means
# the system packages below are the ones those installers call, not the ones a
# control plane happens to need, which is why this list is the pod image's own
# (internal/agentimage/Dockerfile, its base stage) and must move with it.
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
#
# The pod image is debian:trixie-slim and the Incus base image is
# images:debian/13, so this one is trixie too. It was the odd one out on
# bookworm, where a Claude Code login died on "curl: command not found" and
# Node's tarball had nothing to unpack it with, and one release of Debian across
# the three images means one set of commands known to work in all of them.
FROM debian:trixie-slim

# The same system packages the pod image installs before its AI tools (see
# internal/agentimage/Dockerfile): the two lists are the same question, and
# should move together. ca-certificates is not optional on a slim image, which
# ships no CA bundle: without one, curl fails every https URL with "error
# setting certificate file". curl and xz-utils are what the chat's installers
# call (internal/agent/hosttools.go): curl runs Claude Code's installer and
# fetches Node's tarball, and tar -xJ unpacks that tarball.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      iproute2 \
      jq \
      less \
      openssh-client \
      procps \
      python3 \
      tmux \
      unzip \
      xz-utils \
 && rm -rf /var/lib/apt/lists/*

COPY bin/agentbox /usr/local/bin/agentbox

# The dataset is root's on the NAS, and nothing here runs as a user of its own:
# there is no machine to install tools into, so the chat's tools are installed
# under the volume too (AgentBox's own tools directory, D6).
ENV HOME=/root \
    XDG_DATA_HOME=/state \
    AGENTBOX_FRONT_END=host
VOLUME ["/state"]
EXPOSE 7782

ENTRYPOINT ["/usr/local/bin/agentbox", "daemon"]
