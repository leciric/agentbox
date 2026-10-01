# AgentBox

A Go daemon and command-line tool (`cmd/agentbox`, `internal/`) and an Electron desktop app (`desktop/`). Go and Node come from mise (`mise.toml`).

## What it is

AgentBox runs several AI coding agents against one project at the same time, each in a Linux
container of its own.

- **The daemon owns the state.** `internal/daemon` is the control plane: every agent operation goes
  through it (slow ones as jobs), and it serves the HTTP API from `internal/api` over a unix socket
  at `~/.local/share/agentbox/run/agentbox.sock`. All state is one SQLite database, `state.db`
  (`internal/state`). The CLI and the app are both clients of that socket (D15).
- **The desktop app is a thin client**: its main process only relays the socket to the renderer, with
  no AgentBox logic, Incus or git (D19). The renderer's API types are **generated from Go** into
  [`desktop/src/shared/api.ts`](desktop/src/shared/api.ts): after changing `internal/api`, run
  `UPDATE_TS=1 go test ./internal/api` or `TestTypeScriptTypesAreUpToDate` fails (D18).
- **An agent is a machine, a worktree and a branch**: an Incus container, a git worktree on
  `agentbox/<slug>` (prefix configurable, `internal/agent/branch.go`), its own network and a tmux
  terminal with its AI tool running. A project's **lead** is its chat: it runs on the host with no
  machine, directs the agents over MCP and never does the work itself.
- **Three AI tools**: Claude Code, Codex and OpenCode, driven by the app's chat through **ACP**
  adapters (`internal/acp`, `ChatAdapters` in `internal/agent/chat.go`, D40).
- **Project memory** (`internal/memory`, D72): events, memories, tasks, artifacts and reports in the
  same database, searched with FTS5; no model calls or embeddings in the store. Capture from the
  daemon's chokepoints, consolidation on the tool's cheap model, and a context builder that gives
  each agent the slice its task needs.
- **Token ledger**: `token_usage`, one row per model per turn (`internal/chat/tokens.go`), read by
  `agentbox tokens` and each project's Tokens tab. Claude Code chats compact at the installation's
  compact window (200k by default); the desktop tools and Playwright belong to a `desktop` subagent,
  searches go to an `Explore` subagent on Haiku, at most three subagents at once (D83–D86).
- **The brief** is the instructions each AI tool gets about its machine, rendered by `internal/brief`
  from [`brief.md.tmpl`](internal/brief/brief.md.tmpl) (agents) and `lead.md.tmpl` (the lead), with
  the project's notes (`internal/notes`) and memory folded in. It is resent with every model call,
  so keep it short. Golden tests: `go test ./internal/brief -update` after changing a template.

## Where it runs

AgentBox runs in a VM, and the `agentbox` on the user's machine is that VM's front end
(`internal/hostvm`): `agentbox vm …` manages the VM, and every other command runs inside it.

- **Linux**: Cloud Hypervisor (`agentbox vm init`, `internal/hostvm/chv`). Host mode (Incus on the
  machine itself) is no longer offered, but still runs for installs from earlier releases until
  `agentbox vm migrate` moves them (`MovePrompt` in `desktop/src/renderer/components/RunInVM.tsx`),
  for root, inside agents, and with `AGENTBOX_FRONT_END=host` (CI); `hostvm.Front` decides.
- **Mac** (D92): Lima, with krunkit when installed (gives memory back) and vz otherwise
  (`internal/hostvm/krunkit.go`). The socket is forwarded to its usual path, the Mac's home is shared
  at the same path, and worktrees go there (`AGENTBOX_WORKTREES`). Android is off.
  `AGENTBOX_FRONT_END=vm AGENTBOX_VM_TYPE=qemu` tests the front end on Linux.

## Conventions

- **Migrations are appended, never edited**: `migrations` in
  [`internal/state/state.go`](internal/state/state.go), tracked with `PRAGMA user_version`.
- **Decisions and features are explained in their pull request**: context, what was decided and
  rejected, what was checked, where the code is, what it still can't do. The code cites decisions by
  number (D1–D95); their records aren't in this repository.
- **[`CHANGELOG.md`](CHANGELOG.md) is generated** by release-please (D95) from conventional-commit PR
  titles, so a PR's title is its changelog line: write it for a user. `feat:` is Added, `fix:`/`perf:`
  Fixed, `refactor:` Changed; `test:`, `ci:`, `docs:` and `chore:` don't appear.

## Build and test

```bash
go test ./...
go build -o bin/agentbox ./cmd/agentbox   # "agentbox version dev"; bin/ is gitignored
npm --prefix desktop start                # build and launch the app unpacked, in seconds
npm --prefix desktop run dist             # installers in desktop/dist/: minutes, only when packaging changed
```

Test the packages you changed while you work, and `go test ./...` once before you finish. The slow
packages (`internal/daemon`, `internal/chat`, `internal/agent`) run tests in parallel, and
`internal/state`/`internal/memory` migrate a template database once per binary.

[CI](.github/workflows/ci.yml) runs on every push to `main` and every PR, each as its own job:

- `go vet ./...`, `go test ./...`, and the desktop app's `typecheck` and `build`. Incus tests are
  behind the `integration` build tag and only vetted (D49).
- `golangci-lint run ./...` (`.golangci.yml`).
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- `go mod tidy && git diff --exit-code go.mod go.sum`.
- `scripts/check-coverage.sh`: a floor in [`.github/coverage-floor.txt`](.github/coverage-floor.txt),
  bumped as coverage rises, never lowered.
- `npm --prefix desktop run lint`: oxlint ([`desktop/.oxlintrc.json`](desktop/.oxlintrc.json)), used
  because typescript-eslint doesn't support TypeScript 7; only `react-hooks`' two rules from its
  React plugin.
- `npm --prefix desktop test` (Node's test runner, with coverage).
- The PR title, checked as a conventional commit (`feat|fix|refactor|docs|chore|test|perf|ci`).

## Previewing the rail and the sidebar

Both are narrow fixed-width columns, prone to overflow: a `<pre>`, an unbroken URL, or any flex/grid
child without `min-w-0` widens them. `desktop/src/renderer/dev/preview.tsx` renders both against
fixtures built to trigger that (`dev/fixtures.ts`); it isn't part of the app build.

```bash
npm --prefix desktop run preview                                    # serve it, print the URL
npm --prefix desktop run preview -- --shots out                     # screenshot every scenario in dev/scenarios.json
npm --prefix desktop run preview -- --shots out --against HEAD~1    # and before/after against another commit
```

A scenario is a URL (`?open=agent-99&theme=light`, see the comment atop `preview.tsx`). `--against`
checks the ref out into a throwaway worktree (refs after #82 only). It uses `/usr/bin/chromium` when
present, else `npx playwright install chromium` once.

## The agents' base image

`agentbox image build` makes it on the user's machine from Debian and
[`internal/image/provision.sh`](internal/image/provision.sh). **Never publish a built image**: it
holds software we may not redistribute (Claude Code) and GPL packages.

- **Agent tools** (Go, Node, pnpm, Claude Code, gh, Codex, OpenCode, the ACP adapters, the Playwright
  MCP server) are pinned in [`tools.txt`](internal/image/tools.txt), one per line with the command
  that proves it works. **To move one on, change its line and nothing else**; don't bump
  `image.Version`. A daemon finding other tools on its base updates them in place in the background
  (`image.UpdateTools`, `internal/daemon/imagetools.go`), swapping the copy in only when every tool
  checks out (`image.UseBase`); on failure the base stays and Setup offers a rebuild.
  `internal/agent/chat.go` takes the ACP adapters' versions from the same file.
- **Anything in `provision.sh`** means bumping `image.Version` in
  [`image.go`](internal/image/image.go), which makes Setup ask for a rebuild, as does turning an
  optional component on or off. The [Base image](.github/workflows/base-image.yml) workflow builds it
  on every change to `internal/image/`.

Agents' temporary files, including `t.TempDir()`, go to a tmpfs on `/t` (`TMPDIR`): a unix socket's
path must stay under 107 bytes, and `/tmp` is where Incus mounts worktrees. `--dev-caches` fills the
Go, npm and Electron caches from this repository at build time. `sudo scripts/check-personalise.sh`
checks `personalise.sh` without Incus.

## Testing against a real daemon

An agent's own machine has no Incus, so image builds, devices and networking can only be unit-tested
there, against fakes. **Nesting** gives an agent a real Incus of its own:

- `agentbox image build --incus` adds Incus to the base image (`image.Components.Incus`), off by default.
- A project turns it on with `PATCH /v1/projects/<name>` (`Project.Nesting`), in the app under the
  project's Settings, "Testing AgentBox itself"; refused unless the base image has Incus.
- New agents of that project get it set up (`agent.Manager.EnsureNesting`,
  `internal/agent/nesting.go`): a `dir` storage pool and the bridge `10.88.8.1/24`, which can't clash
  with the host's 10.8.8.0/24. No `/dev/kvm`: containers only.
- Inside, `go test -tags integration ./internal/incus/...`, `sudo agentbox host setup` (idempotent),
  and `agentbox image build` then `agentbox create` work as on a real host.

## The hub is in another repository

The hub, the server half, is the private [leciric/agentbox-hub](https://github.com/leciric/agentbox-hub)
(D44). The two share [`hubapi/`](hubapi/), the protocol, of which the hub keeps a byte-identical
copy: when you change `hubapi/`, copy it to the hub (its README says how). `hubapi/` must never
import `internal/` or the rest of this module, and `internal/` must never import the hub.

## Commits

Conventional commits: `feat: ...`, `fix: ...`, `refactor: ...`, `docs: ...`, and `chore(release): <version>` for a release.

## Releasing

Releasing is merging release-please's PR (D95, `.github/workflows/release.yml`, configured in
`.github/release-please-config.json` and `.release-please-manifest.json`). It keeps one open release
PR that bumps `version` in `desktop/package.json` and `desktop/package-lock.json` and rewrites
`CHANGELOG.md`, minor if any `feat:` landed, patch otherwise.

1. Land the changes on `main`, each PR titled for a user.
2. Find the release PR (`gh pr list --search "head:release-please--branches--main"`) and fix any
   `CHANGELOG.md` line that reads like a commit message.
3. CI doesn't run on it (a bot's push can't trigger workflows): close and reopen it to get a run.
4. Merge it. The release workflow tags the merge, opens a draft release, and builds the AppImage,
   `.deb`, `.pacman`, the Windows setup and portable `.exe`, the CLI for linux/darwin × amd64/arm64,
   and the signed Mac `.dmg`/`.zip` when Apple's signing secrets are set (skipped otherwise). Only
   when every build succeeds does it upload, write `SHA256SUMS` and publish (D49, D92, D94, D95).

Every build goes through `scripts/release-build.sh`: nothing uncommitted, and a version matching the
tag. A failed build leaves the release a draft; rerunning the workflow picks up where it stopped.

Model release notes on [v0.1.0](https://github.com/leciric/agentbox/releases/tag/v0.1.0). The
history here starts from one commit; never link to leciric/agentbox-private, the old history.
