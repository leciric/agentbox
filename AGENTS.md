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
worktrees go there (`AGENTBOX_WORKTREES`). Android is off in the VM. The whole design is
D92; on Linux,
`AGENTBOX_FRONT_END=vm AGENTBOX_VM_TYPE=qemu` runs the front end against a QEMU VM, to test it
without a Mac.

## Conventions

- **Migrations are appended, never edited.** `migrations` in
  [`internal/state/state.go`](internal/state/state.go) is one ordered list, tracked with
  `PRAGMA user_version`. Editing a past entry leaves already-migrated databases behind.
- **Record a decision** when the shape of something is worth recording — what the context was, what
  was decided, what was rejected, and what proves it — in the pull request that makes it. The code
  cites earlier decisions by number (D1–D92); their records aren't in this repository.
- **Explain a feature worth explaining** in its pull request: what it does, what was checked, where
  the code is, and what it still can't do.

## Build and test

```bash
go test ./...
go build -o bin/agentbox ./cmd/agentbox   # reports "agentbox version dev"
npm --prefix desktop run dist             # bin/agentbox with the version in desktop/package.json, and the AppImage, .deb and .pacman in desktop/dist/
```

`bin/` is gitignored: rebuild after pulling.

Every push to `main` and every pull request runs [CI](.github/workflows/ci.yml): `go vet ./...` and
`go test ./...`, and the desktop app's `typecheck` and `build`. It doesn't package the AppImage, which
takes minutes and belongs to a release, and it can't run the Incus tests — those are behind the
`integration` build tag, and CI only vets them (D49).

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
ref to already carry this harness, so it can diff anything after #82, not further back. Playwright's
own Chromium is a separate download from the one `agentbox browser` manages: `npx playwright install
chromium` once if launching it fails.

## The agents' base image

`agentbox image build` makes the image on the machine it runs on, from Debian and
[`internal/image/provision.sh`](internal/image/provision.sh). **Nothing publishes a built image, and
nothing should:** it holds software we may not redistribute (Claude Code first of all), and Debian's
GPL packages would need their source published with it. Built on the user's machine, each tool is
the user's own install.

**Changing `provision.sh` means bumping `image.Version` in [`image.go`](internal/image/image.go)**,
which is what tells Setup the installed image is outdated and asks for a rebuild. The
[Base image](.github/workflows/base-image.yml) workflow builds the image on every change to
`internal/image/`, the way a new machine does, and publishes nothing.

`personalise.sh`, which renames the image's placeholder user to the host's, can be checked without
Incus: `sudo scripts/check-personalise.sh`.

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

Model a release on 0.3.2 (a fix) or 0.3.0 (features), not on 0.4.0: 0.4.0 was published by hand, with no notes in the repo and only the AppImage on GitHub.

1. Get the changes onto `main`, and run `go test ./...` there.
2. Bump the version, a patch for fixes and a minor for features: `version` in `desktop/package.json`, and the two root `version` fields in `desktop/package-lock.json`.
3. Write `.github/releases/v<version>.md` in the shape of [v0.3.2](https://github.com/leciric/agentbox/releases/tag/v0.3.2) for a fix, or [v0.3.0](https://github.com/leciric/agentbox/releases/tag/v0.3.0) for features:
   - It opens with what changed, in bold. A fix says which versions had the bug.
   - `## Downloads` lists the three files a release carries.
   - A fix explains `## What went wrong`: the cause, and what changed.
   - `## Known limitations` lists new ones and links to the releases that list the rest.
   - It links to the README or to GitHub releases, never to `docs/`, which no longer exists.

   The notes lived in `docs/releases/` until 0.15.0; those are on their [GitHub releases](https://github.com/leciric/agentbox/releases) now, and in git history.
4. Commit as `chore(release): <version>`, and land that commit on `main`.
5. Run the **Release** workflow from the [Actions tab](https://github.com/leciric/agentbox/actions/workflows/release.yml), with `v<version>` as the tag. It builds `main`, tags the commit it built, builds the Mac app on a Mac runner, and only once both builds succeed publishes the notes with `AgentBox-<version>-x86_64.AppImage`, `AgentBox-<version>-amd64.deb`, `AgentBox-<version>-x64.pacman`, the Windows `AgentBox-<version>-x64-setup.exe` and `AgentBox-<version>-x64-portable.exe`, the command-line tool for `linux-amd64`, `linux-arm64`, `darwin-arm64` and `darwin-amd64` (`agentbox-<version>-<os>-<arch>`), `SHA256SUMS`, and the Mac's `AgentBox-<version>-mac-{arm64,x64}.dmg` and `.zip` with `SHA256SUMS-mac` (D49, D92, D94). The Mac app is signed and notarized only when the repository has Apple's secrets. The build takes a few minutes; if the Mac job fails, nothing is published, and running the workflow again carries on from the tag it pushed.

The workflow and the local script check and build through the same script, `scripts/release-build.sh`, so a release means one thing wherever it is made: nothing uncommitted, notes that exist and have no `TODO`, a version that matches the tag asked for, a version that isn't released yet, and a built command-line tool that reports the version. The workflow also refuses to move a tag that already points at another commit — a published tag doesn't move.

`scripts/release.sh` stays as the local alternative, for a release the Actions tab can't make. It needs `gh` logged in, pushes `main` itself, and tags whatever `HEAD` is when the build finishes: **don't commit while it runs.** `scripts/release.sh --dry-run` does everything except the push and the release, and leaves the three files in `desktop/dist/release/v<version>/`.
