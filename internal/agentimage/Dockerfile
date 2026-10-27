# The container image an agent runs as when its machine is a pod in a
# Kubernetes cluster, instead of an Incus instance.
#
# Three stages: base is the system, dev adds the agentbox binary, and agent
# adds the user a pod runs as. Build it from the repository root, because the
# Dockerfile copies bin/agentbox out of the build context:
#
#   scripts/build-agent-image.sh --tag dev
#
# or by hand:
#
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/agentbox ./cmd/agentbox
#   docker build -f internal/agentimage/Dockerfile -t agentbox-agent:dev .
#
# The image is linux/amd64, which is what the script builds the binary for. An
# arm64 image needs a binary and pins of its own.
#
# This is the base system and the binary only, for now. The desktop layers
# (Chromium, Xvnc, openbox, ffmpeg and the rest of the display an agent's
# browser and media use) come with the media work, and the AI tools with
# 2.1b: the TODO in the agent stage says what they are and why
# internal/image/provision.sh is not reused as it stands.
#
# There is no init and no systemd. agentd is PID 1, so it is also the pod's
# SIGTERM handler, and nothing else here has to be running for a pod to stay
# up.

ARG DEBIAN_FRONTEND=noninteractive

FROM --platform=linux/amd64 debian:trixie-slim AS base
ARG DEBIAN_FRONTEND

# What an agent's work needs before any AI tool is installed. python3 is here
# because Debian's own tooling and many projects expect it; git, tmux, ssh and
# the rest are what the task and the chat use, as on an Incus machine.
RUN apt-get update \
 && DEBIAN_FRONTEND=$DEBIAN_FRONTEND apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      jq \
      less \
      openssh-client \
      procps \
      python3 \
      tmux \
      unzip \
      xz-utils \
 && rm -rf /var/lib/apt/lists/*

# Go and Node, from the pins in internal/image/tools.txt: go@1.27.3 and
# node@24.21.0, the versions provision.sh installs with mise on an Incus
# machine. They are unpacked here rather than installed through mise, so the
# image needs no shims and every process gets the same PATH. The checksums are
# go.dev's own for the tarball and nodejs.org's SHASUMS256.txt line. Move them
# on together with tools.txt.
ARG GO_VERSION=1.27.3
ARG GO_SHA256=65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40
ARG NODE_VERSION=24.21.0
ARG NODE_SHA256=fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6

# PATH rather than symlinks: npm and npx are symlinks of their own inside the
# Node directory, and corepack comes with them.
ENV PATH=/usr/local/go/bin:/usr/local/lib/nodejs/bin:$PATH

# The Incus image's /etc/profile.d/agentbox.sh sets this too: without a UTF-8
# locale, git prints a non-ASCII author or message as escapes.
ENV LANG=C.UTF-8

RUN set -eux; \
    curl -fsSL -o /tmp/go.tar.gz "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"; \
    echo "${GO_SHA256}  /tmp/go.tar.gz" | sha256sum -c -; \
    tar -C /usr/local -xzf /tmp/go.tar.gz; \
    rm /tmp/go.tar.gz; \
    curl -fsSL -o /tmp/node.tar.xz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz"; \
    echo "${NODE_SHA256}  /tmp/node.tar.xz" | sha256sum -c -; \
    mkdir -p /usr/local/lib/nodejs; \
    tar -xJf /tmp/node.tar.xz -C /usr/local/lib/nodejs --strip-components=1; \
    rm /tmp/node.tar.xz; \
    go version; node --version; npm --version

# dev is the system plus the binary, without a user: what a quick check runs
# ("docker run --rm <image> agentbox version"), and the stage a test of the
# binary alone can build.
FROM base AS dev
COPY bin/agentbox /usr/local/bin/agentbox

# agent is the image a pod runs. Its claim is mounted at /workspace and
# /home/agent, so what is created here is what a pod has before its volume
# covers it. The UID and GID are internal/k8s's AgentUser, and they are the
# UID an agent has on an Incus machine too, so a worktree stays owned by the
# same user across the two.
FROM dev AS agent

RUN groupadd --gid 1000 agent \
 && useradd --uid 1000 --gid 1000 --create-home --home-dir /home/agent --shell /bin/bash agent \
 && install -d -o agent -g agent /workspace /home/agent/.cache /run/agentbox

ENV HOME=/home/agent
WORKDIR /workspace
USER agent

# TODO(2.1b): the AI tools are not in this image yet, and this image is meant to
# run without them until they are: agentd starts, and an agent whose AI tool is
# "none" is a state the daemon already has. internal/image/provision.sh
# installs, with mise and the pins in internal/image/tools.txt:
#
#   - pnpm, and Go and Node (Go and Node are here already, from the same pins);
#   - Claude Code and its ACP adapter, which the app's chat drives;
#   - the GitHub CLI, for a project that pushes to GitHub with it;
#   - the Playwright MCP server, for the browser tools;
#   - the Codex CLI and its adapter, and the OpenCode CLI, for the optional
#     components of an image;
#   - and the Android, dev-cache and Incus components, which a pod does not
#     have and which stay with the Incus image.
#
# None of them is installed here. The publishable split is not made here
# either: Claude Code and its adapter may not be redistributed, so an image
# with them must never go to a public registry, and internal/image/published.go
# is where that list lives. Both arrive with the tools themselves.
#
# provision.sh itself is not copied: it is written for an Incus instance. It
# runs as root, installs Docker, Chromium, Xvnc and the desktop, makes a
# placeholder user that personalise.sh renames to the host's, and reads helper
# scripts that internal/image puts in that instance's /root. This image has no
# root login to set up and its user is already the pod's.

ENTRYPOINT ["/usr/local/bin/agentbox", "agentd"]
