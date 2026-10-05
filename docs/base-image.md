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

A new AgentBox that only moves the agent tools on (Claude Code, Codex, OpenCode, the GitHub CLI
and the like) doesn't ask for a rebuild: on start, the daemon updates them in the base image in
place, in the background, installing only the tools that changed and checking every one. Setup
says "Updating agent tools…" while it runs, and new agents are made from the current image until
the updated one is in place. If the update fails, the image stays as it was and keeps working, and
Setup says why and offers **Rebuild base image**.

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

## Docker images agents share

Agents that run Docker pull Docker Hub's images through one cache that AgentBox's daemon keeps in
its VM, on the agents' disk, so an image several agents use is downloaded and stored once. It's on
by default, holds at most 20 GiB or an eighth of its disk, whichever is less (the least recently used
images go first), and never fills the disk past its floor. A `--max` bigger than the disk can hold is
capped, with a warning.
Settings → Resources → "Share Docker images between agents" turns it off, changes its size, shows
what it holds and empties it; so does `agentbox docker-cache [on|off] [--max 30GiB] [--clear]`.

- Each agent's `/etc/docker/daemon.json` lists the cache in `registry-mirrors`, at
  `http://127.0.0.1:47500` inside the agent; `docker info | grep -A2 Mirrors` shows it. Agents that
  already exist get it when they next start: no rebuild needed.
- Only Docker Hub goes through it. Images from `ghcr.io`, `quay.io` and other registries are pulled
  from them directly by every agent, because Docker's mirror setting only covers Docker Hub.
- If the cache can't answer (the daemon is restarting, or Docker Hub is unreachable from it), Docker
  pulls from Docker Hub itself, as it does whenever a mirror fails.

## Package caches agents share

Agents' package managers download into caches that AgentBox keeps in its VM and every agent shares,
so a new agent installs from what earlier ones fetched instead of downloading it all again: pnpm's
store, npm's cache and `npx`, Yarn 2+ (Berry), Go's module and build caches, pip, uv, Corepack's
package managers and Playwright's browsers. They're on the agents' disk, outlive the agents, are on
by default, hold at most 20 GiB together or an eighth of their disk, whichever is less (what was used
longest ago goes first), and never fill the disk past its floor.
Settings → Resources → "Share package caches between agents" turns them off, changes their size,
shows what they hold and empties them; so does `agentbox package-cache [on|off] [--max 30GiB] [--clear]`.

- They're mounted at `/var/cache/agentbox/packages` in every agent, and the agent's environment
  points the tools there (`npm_config_cache`, `pnpm_config_store_dir`, `GOMODCACHE`, `GOCACHE`,
  `PIP_CACHE_DIR`, `UV_CACHE_DIR`, `COREPACK_HOME`, `PLAYWRIGHT_BROWSERS_PATH`). pnpm 10 and older
  read `~/.config/pnpm/rc`, Yarn Berry `~/.yarnrc.yml`; a value you set there yourself is kept.
  Agents that already exist get them when they next start.
- Only downloads go there. Logins, tokens, `.env` files and npm's logs stay in each agent. Every
  agent of every project reads them, though, so a private package one project installs is in the
  cache for the others too.
- Yarn 1 keeps a cache per agent, as it can't share one between agents installing at the same time.
- A project's `node_modules` is copied from pnpm's store rather than hard-linked, as the two are on
  different disks: still much faster than downloading.
- Turned off, agents go back to caches of their own when their shells next start.

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
