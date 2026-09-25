# The base image

[← back to the index](README.md)

Every agent's container starts life as a clone of a shared Incus snapshot, `agentbox-base/ready`
(or a project's own saved base), rather than being provisioned from scratch each time — see
[An agent's lifecycle](agent-lifecycle.md).

## Building it

[`internal/image`](../internal/image) tracks the image with a version string, `image.Version`
(`image.go`). `agentbox image build` builds it on the user's own machine, from
`images:debian/13`, by running [`provision.sh`](../internal/image/provision.sh) against a fresh
container, then snapshots the result as `agentbox-base/ready`. Nothing is downloaded: the image
carries Claude Code, which Anthropic doesn't license for redistribution, so it isn't published
anywhere, and every tool in it is the user's own install. A first setup, and every rebuild after a
version bump, takes a few minutes of building.

**Changing `provision.sh` means bumping `image.Version`**: that's what tells an existing
machine's Setup page the installed image is outdated, and asks for a rebuild.

The agent tools mise installs are the exception: they are pinned in
[`tools.txt`](../internal/image/tools.txt), and have a version of their own, recorded on the image
next to `image.Version`. When a new AgentBox pins other tools, its daemon updates them in place on
start, in the background: it starts a copy of the base, installs only the tools that changed,
checks every tool, and swaps the copy in as the base. Setup says "Updating agent tools…" while it
runs, and agents go on being made from the current base until the new one is in place. If the
update fails, the daemon rebuilds the image in the background instead; the current base stays
until the rebuild is done, and Setup asks you for a rebuild only if that fails too.

## What `provision.sh` sets up

Run inside the container before it's snapshotted:

- base packages — `curl`, `git`, `tmux`, `build-essential`, Docker, Python, and the rest an agent
  needs to work;
- a display stack for the desktop tools: a VNC server, Chromium, an Openbox desktop, a terminal,
  `ffmpeg` for recordings, and (opt-in, `AGENTBOX_WITH_ANDROID`) `scrcpy` for the Android emulator
  screen;
- [mise](https://mise.jdx.dev), and through it the tools pinned in `tools.txt` that every agent
  gets: Go, Node, Claude Code, the GitHub CLI, the Playwright MCP server, and Codex/OpenCode where
  requested;
- a placeholder user, `agent` (UID/GID 1000), in the `sudo` and `docker` groups;
- a check at the end that every pinned tool actually reports the version it was asked to install.

## Personalising it

`agentbox-base/ready` still has the placeholder `agent` user in it when it's built.
[`personalise.sh`](../internal/image/personalise.sh) renames that user to the host's own — same
UID/GID or a new one, same group memberships and subordinate UID/GID ranges, `/etc/sudoers.d/
90-agentbox` and `/etc/subuid`/`/etc/subgid` rewritten to match — the moment before a machine
snapshots its own container as ready to clone from. It can be checked without Incus at all:
`sudo scripts/check-personalise.sh` runs it against a real Debian 13 root filesystem (pulled via
Docker) in a chroot, across cases like a plain rename, a new UID/GID, a clash with an existing
system account, and a placeholder left unchanged.

## Checking it in CI

The [Base image workflow](../.github/workflows/base-image.yml) builds the image the way a new
machine does, on pull requests and pushes to `main` that touch `internal/image/`, so a
`provision.sh` change is checked end to end. It publishes nothing.
