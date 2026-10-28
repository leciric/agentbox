# The container image an agent runs as when its machine is a pod in a
# Kubernetes cluster, instead of an Incus instance.
#
# Three stages: base is the system and the agent tools, dev adds the agentbox
# binary, and agent adds the user a pod runs as. Build it from the repository
# root, because the Dockerfile copies bin/agentbox and internal/image/tools.txt
# out of the build context:
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
# The base system, the desktop, the binary and the AI tools. The desktop is the
# same one an Incus machine has (Xvnc, Chromium, openbox, the dock, the file
# manager, a terminal and ffmpeg), because internal/agent/browser.sh is the same
# script in both runtimes. The Playwright MCP server is here too, and it drives
# that Chromium at its DevTools endpoint: it needs no browser of its own and
# downloads none at build time.
#
# This image is for the user's own cluster and registry, and must never be
# pushed to a public registry: Claude Code and its ACP adapter (which bundles
# Anthropic's Agent SDK) may not be redistributed. No release workflow builds,
# tags or publishes this image; internal/image/published.go's `unpublished`
# list is that same rule for the Incus base image, which Setup downloads. A
# publishable variant, without Claude Code and the adapters, is not made yet.
#
# There is no init and no systemd. agentd is PID 1, so it is also the pod's
# SIGTERM handler, and nothing else here has to be running for a pod to stay
# up.

ARG DEBIAN_FRONTEND=noninteractive

FROM --platform=linux/amd64 debian:trixie-slim AS base
ARG DEBIAN_FRONTEND

# What an agent's work needs before any AI tool is installed: the one list every
# runtime installs, in internal/image/base-packages.txt. python3 is there
# because Debian's own tooling and many projects expect it, and iproute2 for
# `ss`, which internal/agent/browser.sh uses to see that Xvnc is listening on
# 5900 before it goes on. sudo is not: a pod is one unprivileged user, and the
# Incus image installs sudo itself for the user it makes.
COPY internal/image/base-packages.txt /tmp/base-packages.txt

RUN apt-get update \
 && DEBIAN_FRONTEND=$DEBIAN_FRONTEND apt-get install -y --no-install-recommends \
      $(grep -v -e '^#' -e '^$' /tmp/base-packages.txt | xargs) \
 && rm -rf /var/lib/apt/lists/* /tmp/base-packages.txt

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

# The agent tools, at the pins in internal/image/tools.txt: pnpm, the three AI
# tools, their ACP adapters, the GitHub CLI and the Playwright MCP server. The
# versions are read from that file, not repeated here, so moving a tool on is
# its line there and nothing else: spec() prints mise's spec for a tool and
# version() its version, and either fails the build rather than installing
# whatever npm calls latest when the line it wants is missing. Go and Node are
# above (the file pins them for mise), and cloudflared is the daemon's rather
# than an agent's, so neither is installed from the file.
#
# npm installs every npm package, into the Node directory the PATH above
# already has. Three of them have npm names that differ from mise's:
# @anthropic-ai/claude-code, @openai/codex and opencode-ai. npm 11 skips
# install scripts for a global install unless they are named, and those three
# need theirs: OpenCode's fetches its binary (the reason tools.txt pins its mise
# tool as the native backend), Claude Code's its own, and pnpm's handles its
# environment.
#
# gh is GitHub's own release tarball, as the Incus base image gets it. Its
# checksum is the release's gh_<version>_checksums.txt line: it moves with the
# gh line in internal/image/tools.txt, and a version whose sum is not here
# fails the build rather than installing something unverified.
#
# @playwright/mcp is an MCP server that drives a browser over CDP, so nothing
# installs a browser of its own: the Chromium the desktop below puts in the
# image is what it drives. PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD says so to any
# dependency whose install script would fetch one anyway.
COPY internal/image/tools.txt /tmp/tools.txt

ARG GH_SHA256=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8

RUN set -eux; \
    spec() { \
      s=$(awk -v name="$1" '!/^#/ && NF >= 3 && $2 ~ ("(^|[/:@])" name "@[0-9]") { print $2; exit }' /tmp/tools.txt); \
      [ -n "$s" ] || { echo "internal/image/tools.txt pins no $1" >&2; exit 1; }; \
      printf '%s' "$s"; \
    }; \
    version() { s=$(spec "$1"); printf '%s' "${s##*@}"; }; \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install -g --no-fund --no-audit --cache /tmp/npm-cache \
      --allow-scripts=@anthropic-ai/claude-code,opencode-ai,pnpm \
      "pnpm@$(version pnpm)" \
      "@anthropic-ai/claude-code@$(version claude)" \
      "@openai/codex@$(version codex)" \
      "opencode-ai@$(version opencode)" \
      "@agentclientprotocol/claude-agent-acp@$(version claude-agent-acp)" \
      "@agentclientprotocol/codex-acp@$(version codex-acp)" \
      "@playwright/mcp@$(version playwright/mcp)"; \
    rm -rf /tmp/npm-cache; \
    gh=$(version gh); \
    curl -fsSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/v${gh}/gh_${gh}_linux_amd64.tar.gz"; \
    echo "${GH_SHA256}  /tmp/gh.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/gh.tar.gz -C /tmp; \
    install -m 0755 "/tmp/gh_${gh}_linux_amd64/bin/gh" /usr/local/bin/gh; \
    rm -rf "/tmp/gh_${gh}_linux_amd64" /tmp/gh.tar.gz

# Every tool, checked the way its own line in internal/image/tools.txt says:
# that line's check command, run while the image builds, so a broken install
# fails here rather than in an agent's first turn. scripts/build-agent-image.sh
# and a run of the image prove the same commands as the agent user.
RUN set -eux; \
    check() { awk -v name="$1" '!/^#/ && NF >= 3 && $2 ~ ("(^|[/:@])" name "@[0-9]") { for (i = 3; i <= NF; i++) printf "%s%s", (i > 3 ? " " : ""), $i; exit }' /tmp/tools.txt; }; \
    for tool in pnpm claude codex opencode gh claude-agent-acp codex-acp playwright/mcp; do \
      printf '%s: ' "$tool"; \
      sh -c "$(check "$tool")"; \
      echo; \
    done; \
    rm /tmp/tools.txt

# OpenCode 2, beside the OpenCode 1 above: the same npm platform package
# internal/image/provision.sh installs on an Incus base image, linked as
# `opencode2` because it declares no `bin` entry of its own and must not shadow
# `opencode`. Its version is image.OpenCode2Version, which
# `agentbox image agent build` passes in, so there is one pin for both runtimes.
# An installation that switches to OpenCode 2 chats through `opencode2 acp`, so
# a pod without this answers "command not found" on its first turn.
ARG OPENCODE2_VERSION
RUN set -eux; \
    [ -n "$OPENCODE2_VERSION" ] || { echo "OPENCODE2_VERSION is not set: build with agentbox image agent build, which passes image.OpenCode2Version" >&2; exit 1; }; \
    npm install -g --no-fund --no-audit --cache /tmp/npm-cache "@opencode/cli-linux-x64@${OPENCODE2_VERSION}"; \
    bin="$(npm root -g)/@opencode/cli-linux-x64/bin/opencode"; \
    test -x "$bin"; \
    ln -sfn "$bin" /usr/local/bin/opencode2; \
    rm -rf /tmp/npm-cache; \
    printf 'opencode2: '; opencode2 --version

# The desktop an agent's browser and media work runs on: Xvnc, Chromium,
# openbox, the dock, the file manager, a terminal, the fonts and ffmpeg. The
# list mirrors the one internal/image/system.sh installs on an Incus base image,
# because internal/agent/browser.sh is the same script in both runtimes: it
# starts Xvnc on :99, openbox, tint2, Chromium with the DevTools protocol on
# 127.0.0.1:9222, and grabs frames with ffmpeg. The two lists must move
# together.
RUN apt-get update \
 && DEBIAN_FRONTEND=$DEBIAN_FRONTEND apt-get install -y --no-install-recommends \
      tigervnc-standalone-server \
      chromium \
      openbox \
      tint2 \
      pcmanfm \
      xfce4-terminal \
      xdotool \
      x11-utils \
      x11-xserver-utils \
      xwallpaper \
      adwaita-icon-theme \
      librsvg2-common \
      fonts-liberation \
      fonts-dejavu-core \
      fonts-noto-color-emoji \
      ffmpeg \
 && rm -rf /var/lib/apt/lists/*

# Chromium's own flags, sourced by Debian's wrapper. A pod has no setuid
# sandbox, and its seccomp profile (RuntimeDefault) refuses the user namespace
# Chromium's sandbox needs, so the container is the sandbox.
# internal/machines/image/chromium.conf is the same decision for
# `agentbox machines`.
COPY internal/agentimage/chromium.conf /etc/chromium.d/agentbox-agent

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
 && install -d -o agent -g agent /workspace /home/agent/.cache /home/agent/.local /run/agentbox

ENV HOME=/home/agent
# The tools are installed as root, outside the home, because a pod mounts its
# claim at /home/agent: whatever a build puts there, the volume hides. What an
# agent installs itself goes into its home instead, which is also how the Incus
# image gives its user ~/.local/bin (provision.sh's /etc/profile.d/agentbox.sh),
# so npm's global prefix and the PATH entry have to agree.
ENV PATH=/home/agent/.local/bin:$PATH
ENV NPM_CONFIG_PREFIX=/home/agent/.local
WORKDIR /workspace
USER agent

# The AI tools, their adapters and the rest are in the base stage, at the pins
# internal/image/tools.txt holds, with no mise: the Incus base image installs
# these same pins through it, as its user, and this image has them already, for
# every user. The one thing a pod's chat needs beyond this image is in
# internal/agent/chat.go, whose chat helper runs an adapter through `mise exec`,
# a command this image does not have.

ENTRYPOINT ["/usr/local/bin/agentbox", "agentd"]
