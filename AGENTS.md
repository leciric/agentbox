# AgentBox

A Go daemon and command-line tool (`cmd/agentbox`, `internal/`) and an Electron desktop app (`desktop/`). Go and Node come from mise (`mise.toml`).

## What it is

AgentBox runs several AI coding agents against one project at the same time, each in a Linux
container of its own so they can't collide.

- **The daemon owns the state.** `internal/daemon` is the control plane: every agent operation goes
  through it, the slow ones as jobs, and it serves the HTTP API from `internal/api` over a unix
  socket at `~/.local/share/agentbox/run/agentbox.sock`. All of the state is one SQLite database,
  `state.db`, in `internal/state`. The CLI is a client of that socket, and so is the app
  (D15).
- **The desktop app is a thin client.** Its main process only relays the socket to the renderer: no
  AgentBox logic, no Incus, no git (D19). The API types the renderer uses are
  **generated from Go** into [`desktop/src/shared/api.ts`](desktop/src/shared/api.ts); change
  `internal/api` and `TestTypeScriptTypesAreUpToDate` fails until you run
  `UPDATE_TS=1 go test ./internal/api` (D18).
- **An agent is a machine, a worktree and a branch.** `agentbox create` gives it an Incus container,
  a git worktree on `agentbox/<slug>`, a branch named after its work — the slug given to create, or one made from its title or task (`agentbox/` is the project's branch prefix, and can be changed; `internal/agent/branch.go`) — its own network and a tmux terminal with its AI tool already
  running. A project also has a **lead** —
  its chat — which runs on the host with no machine of its own, so a project you have never chatted
  with costs nothing; it directs the project's agents over MCP and never does the work itself.
- **Three AI tools:** Claude Code, Codex and OpenCode. The app's chat drives all three through
  **ACP** adapters (`internal/acp`, `ChatAdapters` in `internal/agent/chat.go`) — Claude Code and
  Codex through adapters of their own, OpenCode through its own `acp` subcommand
  (D40).
- **Project memory is what the project knows, kept across agents.** Events, memories, working
  memory, tasks, artifacts and reports, as tables in the same database and searched with FTS5 — the
  store calls no model and embeds nothing. Around it: automatic capture from the daemon's own
  chokepoints, consolidation of events into memories (by default on the project's tool's cheap
  model, in a session of its own), and a context builder that gives each agent the slice of memory
  its task needs. It is in `internal/memory` (D72).
- **What chats spend is kept in a token ledger.** `token_usage` gets one row per model per turn,
  written by the chat as each turn ends (`internal/chat/tokens.go`) from what the ACP adapters
  report. It's read through `agentbox tokens` and each project's Tokens tab. Every Claude Code chat
  compacts at the installation's compact window, 200k tokens by default, because each model call
  resends the whole conversation. In Claude Code the desktop tools belong to a `desktop` subagent,
  so screenshots don't stay in an agent's context. Searches go to an `Explore` subagent on Haiku,
  and at most three subagents run at once. Each Claude account's five-hour and weekly limits are
  kept as its chats report them. Subagents show in the chat as cards
  (D83–D86).
- **Project notes** are one markdown file per project (`internal/notes`), written by the user in the
  app and added to by the lead under `## From the lead`. They are folded into every agent's brief,
  the instructions each AI tool gets about its machine, rendered by `internal/brief` from
  [`brief.md.tmpl`](internal/brief/brief.md.tmpl) — which is also where the memory section lands.
  The brief has golden tests: change the template and run `go test ./internal/brief -update`.

## On a Mac

AgentBox runs in a Linux VM there, made with Lima, and nothing of the above is ported: the daemon,
Incus and the agents are the Linux ones, inside the VM. The macOS `agentbox` is a front end
(`internal/hostvm`): `agentbox vm …` makes and manages the VM, and every other command runs in it
through `limactl shell`. The daemon's socket is forwarded to the Mac at its usual path, so the app
is the same client it is on Linux; the Mac's home is shared at the same path, and the agents'
worktrees go there (`AGENTBOX_WORKTREES`). Android is off in the VM. `vm init` makes the VM
with Lima's krunkit VM type when krunkit is installed, which gives the Mac back the memory the VM
frees, and with vz, which doesn't, otherwise (`internal/hostvm/krunkit.go`). The whole design is
D92; on Linux,
`AGENTBOX_FRONT_END=vm AGENTBOX_VM_TYPE=qemu` runs the front end against a QEMU VM, to test it
without a Mac.

On Linux too, AgentBox runs in a VM of its own, made with Cloud Hypervisor (`agentbox vm init`,
`internal/hostvm/chv`), and the Linux `agentbox` is that VM's front end: every command but
`agentbox vm …` runs in it, and says to run `vm init` until it's made. Host mode — Incus on the
machine itself, `agentbox host setup` — is no longer offered. It stays as the VM's internals, for a
machine set up that way by an earlier release (its own `state.db`, no VM), which keeps working until
`agentbox vm migrate` moves it and is prompted to once per update (`MovePrompt` in
`desktop/src/renderer/components/RunInVM.tsx`), for root, inside agents, and with
`AGENTBOX_FRONT_END=host` (CI) — `hostvm.Front` decides.

## Conventions

- **Migrations are appended, never edited.** `migrations` in
  [`internal/state/state.go`](internal/state/state.go) is one ordered list, tracked with
  `PRAGMA user_version`. Editing a past entry leaves already-migrated databases behind.
- **Record a decision** when the shape of something is worth recording — what the context was, what
  was decided, what was rejected, and what proves it — in the pull request that makes it. The code
  cites earlier decisions by number (D1–D92); their records aren't in this repository.
- **Explain a feature worth explaining** in its pull request: what it does, what was checked, where
  the code is, and what it still can't do.
- **[`CHANGELOG.md`](CHANGELOG.md) is generated, not written.** release-please (D95) turns the
  conventional-commit PR titles since the last release into it, so a PR's title is its changelog
  line: write it for a user reading the changelog, not for `git log`. `feat:` becomes an Added
  entry, `fix:`/`perf:` a Fixed one, `refactor:` a Changed one; `test:`, `ci:`, `docs:` and `chore:`
  don't show up at all. See Releasing below for how this plays out.

## Build and test

```bash
go test ./...
go build -o bin/agentbox ./cmd/agentbox   # reports "agentbox version dev"
npm --prefix desktop run dist             # bin/agentbox with the version in desktop/package.json, and the AppImage, .deb and .pacman in desktop/dist/
```

`bin/` is gitignored: rebuild after pulling.

The slowest packages (`internal/daemon`, `internal/chat`, `internal/agent`) run their independent
tests with `t.Parallel()`, and `internal/state` and `internal/memory` migrate a template database
once per test binary rather than once per test, so testing one of those packages on its own, or
`go test ./...` as a whole, is far faster than it was.

While you work, test the packages you changed (`go test ./internal/brief/...`), and run
`go test ./...` once before you finish. To show a change in the app, `npm --prefix desktop start`
builds it and launches it unpacked, which takes seconds; `dist` packages installers, which takes
minutes, and is only worth it when packaging is what you changed.

Every push to `main` and every pull request runs [CI](.github/workflows/ci.yml): `go vet ./...` and
`go test ./...`, and the desktop app's `typecheck` and `build`. It doesn't package the AppImage, which
takes minutes and belongs to a release, and it can't run the Incus tests — those are behind the
`integration` build tag, and CI only vets them (D49).

CI also runs, each as its own parallel job so the wall time stays about the same:

- **golangci-lint**, against `.golangci.yml` (errcheck, govet, ineffassign, misspell, staticcheck,
  unused): `golangci-lint run ./...`.
- **govulncheck**, against the Go vulnerability database: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- **`go mod tidy`**, checked for a clean diff: `go mod tidy && git diff --exit-code go.mod go.sum`.
- **Go coverage**, with a per-package summary and a floor below which the build fails. The floor
  lives in [`.github/coverage-floor.txt`](.github/coverage-floor.txt); bump it as coverage goes up,
  never down: `scripts/check-coverage.sh`.
- **oxlint** on the desktop app, configured in [`desktop/.oxlintrc.json`](desktop/.oxlintrc.json) —
  it's used instead of ESLint/typescript-eslint because typescript-eslint doesn't yet support
  TypeScript 7 (this repo's version); oxlint doesn't depend on the `typescript` package. Only
  `react-hooks`'s two rules (`rules-of-hooks`, `exhaustive-deps`) are enabled from its `react`
  plugin — the rest of that plugin's newer React best-practice rules are off, since type-aware
  checking is already `tsc`'s job: `npm --prefix desktop run lint`.
- **Desktop test coverage**, via Node's built-in test runner:
  `npm --prefix desktop test` (now runs with `--experimental-test-coverage`).
- **PR title**, checked against conventional commits (`feat|fix|refactor|docs|chore|test|perf|ci`)
  since squash merges use the title as the commit message — nothing to run locally, it only checks
  `github.event.pull_request.title`.

## Previewing the rail and the sidebar

Both are narrow, fixed-width columns fed whatever agents and the daemon put in them, so both are
prone to the same overflow bug: a `<pre>`, an unbroken URL, or any other flex/grid child without
`min-w-0` drags the column wider than it's allowed to be. `desktop/src/renderer/dev/preview.tsx`
stands both up against fixture content designed to trigger that (see `dev/fixtures.ts` for the long
paths, URLs, branch names, stack traces and JSON) so nobody has to rebuild a throwaway harness to
check a change here, the way [#82](https://github.com/leciric/agentbox/pull/82) originally did.
It isn't part of the app — `vite.config.mts`'s build only bundles `index.html` and `web.html`, so
`npm run build` never touches it.

```bash
npm --prefix desktop run preview                                    # serve it, print the URL
npm --prefix desktop run preview -- --shots out                     # screenshot every scenario in dev/scenarios.json into out/
npm --prefix desktop run preview -- --shots out --against HEAD~1    # and diff against another commit
```

A scenario is a URL (`?open=agent-99&theme=light`, see the comment atop `preview.tsx`), so
`scripts/preview.mjs` can drive it headlessly with Playwright rather than scripting clicks.
`--against <ref>` checks that ref out into a throwaway `git worktree` — the same primitive every
agent already runs in — screenshots it into `out/before/`, and screenshots the working tree into
`out/after/`, so a change here can show its before and after instead of asserting them. It needs the
ref to already carry this harness, so it can diff anything after #82, not further back. It launches
the system's `/usr/bin/chromium` when there is one, as in every agent's machine; elsewhere it needs
Playwright's own Chromium: `npx playwright install chromium` once if launching it fails.

## The agents' base image

`agentbox image build` makes the image on the machine it runs on, from Debian and
[`internal/image/provision.sh`](internal/image/provision.sh). **Nothing publishes a built image, and
nothing should:** it holds software we may not redistribute (Claude Code first of all), and Debian's
GPL packages would need their source published with it. Built on the user's machine, each tool is
the user's own install.

Two versions say what a base image has, and each is recorded on it:

- **The agent tools** — Go, Node.js, pnpm, Claude Code, the GitHub CLI, Codex, OpenCode, the ACP
  adapters, the Playwright MCP server: everything mise installs — are pinned in
  [`tools.txt`](internal/image/tools.txt), one a line with the command that proves it works. **To
  move one on, change its line there and nothing else**: don't bump `image.Version`. The tools
  version is a hash of those lines, so it can't be forgotten. A daemon that finds its base image
  with other tools updates them in place, in the background, on start (`image.UpdateTools`, driven
  from `internal/daemon/imagetools.go`): it copies the base, installs only what changed with mise,
  checks every tool, and swaps the copy in. Setup shows "Updating agent tools…" meanwhile, and
  agents keep being made from the old base until the swap, which waits for creates copying the
  base and makes new ones wait for it (`image.UseBase`). If the update fails, the base is left as
  it was, and Setup shows why as a warning and offers the rebuild; the daemon never starts a full
  rebuild on its own.
  `internal/agent/chat.go` takes the ACP adapters' versions from the same file.
- **Everything else in the image** is `provision.sh`. **Changing it means bumping `image.Version`
  in [`image.go`](internal/image/image.go)**, which is what tells Setup the installed image is
  outdated and asks for a rebuild — as does turning an optional component on or off. The
[Base image](.github/workflows/base-image.yml) workflow builds the image on every change to
`internal/image/`, the way a new machine does, and publishes nothing.

Every agent's temporary files, and so every test's `t.TempDir()`, go to a tmpfs on `/t` (`TMPDIR`):
a short path, because a unix socket's path can't be longer than 107 bytes, and not `/tmp`, which
stays on disk because Incus mounts worktrees under it. A machine whose agents work on AgentBox
itself can have the Go, npm and Electron caches filled from this repository at build time, with
`agentbox image build --dev-caches`; it's off by default, since other projects' agents don't need
them.

`personalise.sh`, which renames the image's placeholder user to the host's, can be checked without
Incus: `sudo scripts/check-personalise.sh`.

## Testing against a real daemon

An agent working on AgentBox has none of the above to test against: its own machine has no Incus,
so features that touch agent machines — image builds, devices, networking — can only be unit-tested there,
against fakes. **Nesting** gives an agent a real Incus daemon of its own, inside its own container,
so it can run AgentBox against it for real.

- `agentbox image build --incus` adds Incus itself to the base image, recorded on it like
  `--android` or `--dev-caches` (`image.Components.Incus`, `user.agentbox.with-incus`): off by
  default, since most agents never need it, and bumping `image.Version` the way any other
  `provision.sh` change does.
- A project turns nesting on for its agents with `agentbox`'s `PATCH /v1/projects/<name>`
  (`Project.Nesting`, off by default) — in the app, the project's Settings, "Testing AgentBox
  itself". It's refused unless the base image already has Incus.
- A new agent of a project with nesting on gets it set up automatically
  (`agent.Manager.EnsureNesting`, `internal/agent/nesting.go`), the same best-effort way it gets
  its browser: the container's `security.nesting=true` already lets it run Docker, so nothing more
  is needed there, but Incus itself starts masked (`provision.sh`) until nesting turns it on. Setup
  runs `incus admin init --preseed` with a `dir` storage pool — no block device or filesystem
  support needed nested — and its own bridge, `10.88.8.1/24`, chosen so it can never clash with the
  host's own (`host-setup.sh`'s `--bridge-subnet`, 10.8.8.0/24 by default). `/dev/kvm` isn't part of
  it: nesting is for containers, not virtual machines.
- Inside, `agentbox`, `go test ./internal/incus/...` with the `integration` build tag, and
  `sudo agentbox host setup` (idempotent, so running it again after the automatic setup only adds
  what it left out) all work the way they do on a real host. The smoke test this feature was built
  for: `agentbox image build` then `agentbox create` of a tiny agent, inside an agent.
- An agent's own brief says so, under "Testing against a real daemon" (`brief.md.tmpl`), only when
  its project has nesting on.

## The hub is in another repository

The hub, the server half of AgentBox, is [leciric/agentbox-hub](https://github.com/leciric/agentbox-hub),
which is private: it stays closed while the rest is open source (D44).
This repository has everything that connects to a hub.

The two halves share [`hubapi/`](hubapi/), the protocol, and the hub keeps a byte-identical copy of it.
Nothing checks the two copies against each other: when you change `hubapi/`, copy it to the hub too
(its README says how). Don't make `internal/` import anything from the hub.

Because the hub copies it, `hubapi/` must stand on its own: it must never import anything from
`internal/` or the rest of this module.

## Commits

Conventional commits: `feat: ...`, `fix: ...`, `refactor: ...`, `docs: ...`, and `chore(release): <version>` for a release.

## Releasing

Releasing means merging a pull request, not running a script. [release-please](https://github.com/googleapis/release-please)
(D95, `.github/workflows/release.yml`, configured in `.github/release-please-config.json` and
`.release-please-manifest.json`) watches every push to `main` and keeps a single open "release PR"
matching what's landed since the last release: it bumps `version` in `desktop/package.json` and the
two root `version` fields in `desktop/package-lock.json`, and rewrites `CHANGELOG.md` from the
conventional-commit PR titles since then, patch or minor depending on whether any of them was a
`feat:`.

1. Get the changes onto `main` — each PR's title is what ends up in the changelog, so title it for a
   user, not for `git log` (see Conventions above).
2. Find the current release PR (`gh pr list --search "head:release-please--branches--main"`, or the
   pinned one release-please comments on). Read `CHANGELOG.md`'s diff on it and fix anything that
   reads like a commit message instead of a changelog line — release-please only groups by type, it
   doesn't rewrite prose.
3. CI does not run on the release PR itself: release-please opens and updates it with
   `GITHUB_TOKEN`, and a bot's own push can't trigger a workflow. Close it and reopen it (its branch
   doesn't need to change) to get a CI run before merging.
4. Merge it. That push to `main` is what the release workflow is waiting for: it tags the merge
   commit, opens the GitHub release as a draft with release-please's own generated notes, then builds
   `AgentBox-<version>-x86_64.AppImage`, `AgentBox-<version>-amd64.deb`, `AgentBox-<version>-x64.pacman`,
   the Windows `AgentBox-<version>-x64-setup.exe` and `AgentBox-<version>-x64-portable.exe`, the
   command-line tool for `linux-amd64`, `linux-arm64`, `darwin-arm64` and `darwin-amd64`
   (`agentbox-<version>-<os>-<arch>`), and the Mac app when the repository has Apple's signing
   secrets (`AgentBox-<version>-mac-{arm64,x64}.dmg`/`.zip`, signed and notarized; without the
   secrets that job is skipped and the release goes out without it — add them later and the next
   release picks them up on its own). Only once every build has succeeded does it upload the assets,
   write `SHA256SUMS`, and take the release off draft (D49, D92, D94, D95).

The build jobs check and build through the same script every part runs, `scripts/release-build.sh`,
so what a release has to pass is one thing wherever it's checked: nothing uncommitted, and a version
that matches the tag release-please made. If a build fails, the release stays a draft — visible to
collaborators, not published — until a rerun of the workflow gets every job green; nothing about the
version or the tag changes in between, so a rerun just picks up where it left off.

Model release notes' prose on [v0.1.0](https://github.com/leciric/agentbox/releases/tag/v0.1.0), the
first public one: this repository's history starts from a single commit, so there are no earlier
releases here to link to or to diff against — the old ones, back to 0.3.x, are only on
[leciric/agentbox-private](https://github.com/leciric/agentbox-private), which nothing here should
link to.
