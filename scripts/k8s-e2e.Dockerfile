# The image scripts/k8s-e2e.sh imports into its throwaway k3d cluster: the
# smallest thing that runs an agent's pod and can be driven by agentd.
#
# It is not internal/agentimage/Dockerfile and must not become it. That image
# carries the AI tools and the desktop, takes more than ten minutes and several
# GiB to build, and is what a real cluster runs. This one has git, tmux and the
# binary, which is all a pod needs to clone, keep a session and answer the
# gateway.
#
# Built from the repository root, because it copies bin/agentbox:
#
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/agentbox ./cmd/agentbox
#   docker build -f scripts/k8s-e2e.Dockerfile -t agentbox-e2e:k8s .
FROM debian:trixie-slim

RUN apt-get update \
 && apt-get install --no-install-recommends --yes \
      ca-certificates \
      coreutils \
      git \
      procps \
      tmux \
 && rm -rf /var/lib/apt/lists/*

COPY bin/agentbox /usr/local/bin/agentbox

# The user a pod runs as: internal/k8s's AgentUser, 1000, which is the UID an
# agent has on an Incus machine too. A cluster mounts its claim over
# /workspace and /home/agent, so both are made here and the image has the same
# shape as a pod before its volume covers it.
RUN groupadd --gid 1000 agent \
 && useradd --uid 1000 --gid 1000 --create-home --home-dir /home/agent --shell /bin/bash agent \
 && install -d -o agent -g agent /workspace /home/agent/.cache /home/agent/.local /run/agentbox

# The container the script runs the daemon in is this image too, under the uid
# that owns the test's HOME, because os/user refuses a uid with no passwd entry
# and a unix socket is only reachable by its owner. 1000 is the agent already,
# so only a different uid needs a second user.
ARG DAEMON_UID=1000
ARG DAEMON_GID=1000
RUN if [ "$DAEMON_UID" != "1000" ]; then \
      if ! getent group "$DAEMON_GID" >/dev/null; then groupadd --gid "$DAEMON_GID" agentbox; fi \
      && useradd --uid "$DAEMON_UID" --gid "$DAEMON_GID" --create-home --home-dir /home/agentbox --shell /bin/bash agentbox; \
    fi

ENV HOME=/home/agent
ENV PATH=/home/agent/.local/bin:$PATH
WORKDIR /workspace
USER agent

# A pod replaces this with its own command ("agentbox agentd"), which lands on
# the same thing; `docker run` with no command starts an agentd too.
ENTRYPOINT ["/usr/local/bin/agentbox", "agentd"]
