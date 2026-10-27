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
  compact window (500k by default); the desktop tools and Playwright belong to a `desktop` subagent,
  searches go to an `Explore` subagent on Haiku, at most three subagents at once (D83–D86).
- **The brief** is the instructions each AI tool gets about its machine, rendered by `internal/brief`
  from [`brief.md.tmpl`](internal/brief/brief.md.tmpl) (agents) and `lead.md.tmpl` (the lead), with
  the project's notes (`internal/notes`) and memory folded in. It is resent with every model call,
  so keep it short. Golden tests: `go test ./internal/brief -update` after changing a template.

## Where it runs

AgentBox runs in a VM, and the `agentbox` on the user's machine is that VM's front end
(`internal/hostvm`): `agentbox vm …` manages the VM, and every other command runs inside it.

- **Linux**: Cloud Hypervisor (`agentbox vm init`, `internal/hostvm/chv`), booting a ready-made
  disk with its packages installed (`vmdisk.go`, the `vmdisk-<version>` release the
  [Published VM disk](.github/workflows/published-vm-disk.yml) workflow makes), or Debian's cloud
  image when there's none, whose first boot installs them. Host mode (Incus on the
  machine itself) is no longer offered, but still runs for installs from earlier releases until
  `agentbox vm migrate` moves them (`MovePrompt` in `desktop/src/renderer/components/RunInVM.tsx`),
  for root, inside agents, and with `AGENTBOX_FRONT_END=host` (CI); `hostvm.Front` decides.
- **Mac** (D92): Lima, with krunkit when installed (gives memory back) and vz otherwise
  (`internal/hostvm/krunkit.go`). The socket is forwarded to its usual path, the Mac's home is shared
  at the same path, and worktrees go there (`AGENTBOX_WORKTREES`). Android is off.
  `AGENTBOX_FRONT_END=vm AGENTBOX_VM_TYPE=qemu` tests the front end on Linux.

## Conventions

- **The app's text is translated** (`desktop/src/shared/i18n`): every user-visible string in the
  desktop app is a key in `en-US.ts` and `pt-BR.ts`, shown with `useT()`/`t()` from
  `renderer/lib/i18n.tsx`; `i18n.test.ts` and the typecheck fail when pt-BR lacks a key. A language
  is a catalog file plus a line in `languages.ts`. The CLI, the brief and the chats stay in English.
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
go test ./internal/cli ./internal/brief   # the packages you changed
go build -o bin/agentbox ./cmd/agentbox   # "agentbox version dev"; bin/ is gitignored
npm --prefix desktop start                # build and launch the app unpacked, in seconds
npm --prefix desktop run dist             # installers in desktop/dist/: minutes, only when packaging changed
```

Run only the tests your change touches: `go test` on the packages you changed (and on those that use
an exported API you changed), and for the app the test files of what you changed (`cd desktop && node
--test src/…/x.test.ts`), plus `typecheck` and `lint`. Don't run `go test ./...` or a bare
`npm --prefix desktop test`: CI runs the whole suite, and on an agent's VM it takes every other agent's
memory. Run it only when your task, or a message from the lead or the user, asks for it by name. The slow
packages (`internal/daemon`, `internal/chat`, `internal/agent`) run tests in parallel, and
`internal/state`/`internal/memory` migrate a template database once per binary.

[CI](.github/workflows/ci.yml) is billed by the minute (the repository is private; macOS 10x,
Windows 2x), so a run runs only what its files can break. A first job checks the PR title as a
conventional commit (`feat|fix|refactor|docs|chore|test|perf|ci`) and works out, with
[`scripts/ci-changes.sh`](scripts/ci-changes.sh), what the change touches; the other jobs skip.
Go is anything but `desktop/`, `mobile/`, `docs/`, `promo/`, `packaging/`, `scripts/`, `.github/` and
root prose, plus `desktop/src/shared/api.ts`, `scripts/check-coverage.sh` and
`.github/coverage-floor.txt`: a file Go embeds or a test reads belongs under a package (or in that
list). The desktop app is `desktop/` and `vnc.js`; the phone app is `mobile/` and the desktop's
`shared/api.ts` and `shared/i18n/`. A change to `ci.yml` or `ci-changes.sh` runs everything, macOS
and Windows included. A new push cancels the run it supersedes.

- **Go tests and coverage**: `scripts/check-coverage.sh`, the one `go test ./...`, with a floor in
  [`.github/coverage-floor.txt`](.github/coverage-floor.txt), bumped as coverage rises, never
  lowered. It includes `TestTypeScriptTypesAreUpToDate`. Packages a change doesn't reach come from
  the test cache, coverage included: CI sets each file's mtime from its git object id, since a
  test that opens a file is cached by that file's mtime and size.
- **Go vet, lint, tidy**: `go mod tidy` with no diff, `go vet ./...` (and with `-tags integration`:
  Incus tests are only vetted, D49), `golangci-lint` (`.golangci.yml`), every package vetted for
  darwin, and builds for darwin/arm64, linux/arm64 and Windows.
- **Desktop**: `npm --prefix desktop` `lint` (oxlint, [`desktop/.oxlintrc.json`](desktop/.oxlintrc.json),
  used because typescript-eslint doesn't support TypeScript 7; only `react-hooks`' two rules from its
  React plugin), `typecheck`, `test` (Node's test runner, with coverage), `build`, and the committed
  `vnc.js` matching its build.
- **macOS** (cgo, signed, the vz driver) and **Windows** (the WSL front end): on pushes to `main`,
  and on a PR labelled `ci:full` (adding the label runs just these two, whatever changed), when the
  change reaches what they test: `go.mod`/`go.sum`, `mise.toml`, `cmd/`, a package their tests
  import (`go list -deps -test` of `internal/hostvm/...`, `internal/paths` and `internal/api` for
  darwin; `internal/hostos` and `internal/hostwsl/...` for Windows), a file only that OS builds,
  and for macOS `scripts/mac-sign.sh` and the entitlements.
- **govulncheck**: weekly, on `main`, and on PRs that change `go.mod`/`go.sum`.

Go caches are saved by `main`'s runs only and restored by PRs, so a PR's tests skip packages it
doesn't reach. [Base image](.github/workflows/base-image.yml) runs on PRs that change
`internal/image/` (its tests aside), or by hand.

## Previewing the rail and the sidebar

Both are narrow fixed-width columns, prone to overflow: a `<pre>`, an unbroken URL, or any flex/grid
child without `min-w-0` widens them. `desktop/src/renderer/dev/preview.tsx` renders both against
fixtures built to trigger that (`dev/fixtures.ts`); it isn't part of the app build.

```bash
npm --prefix desktop run preview                                    # serve it, print the URL
npm --prefix desktop run preview -- --shots out                     # screenshot every scenario in dev/scenarios.json
npm --prefix desktop run preview -- --shots out --against HEAD~1    # and before/after against another commit
npm --prefix desktop run preview -- --shots out --lang pt-BR           # in another language; overflow is reported
npm --prefix desktop run preview -- --shots out --only window-         # only the whole app at window sizes (1920×1080, portrait, 800×600, 480)
```

`?app=project` (or `home`, `pulls`, `agent-99`, `settings`) renders the whole App, where below `lg`
the sidebar and below `xl` the rail become drawers; a page wider than its window is reported too.

A scenario is a URL (`?open=agent-99&theme=light`, see the comment atop `preview.tsx`). `--against`
checks the ref out into a throwaway worktree (refs after #82 only). It uses `/usr/bin/chromium` when
present, else `npx playwright install chromium` once.

`--shots` is for overflow and before/after checks. Proof for a PR is a recording plus `agentbox media
screenshot` of the preview in the agent's display, made by the desktop subagent.

## The agents' base image

Setup downloads it (`internal/image/published.go`), adding the Codex and OpenCode tools on top when they
are on, unless Android, dev caches or Incus are (`Components.NeedsLocalBuild`), from the
`image-<version>` prerelease on this repository's GitHub releases, or first from its copy on the CDN
(`downloads.agentbox.linting.dev/images/`, `scripts/r2-images.sh`), compressed with zstd and split in
128 MiB parts that Setup downloads at once, resumes, joins and checks (`internal/image/download.go`;
`scripts/github-images.sh` publishes it, and keeps only the newest image release; app releases,
release-please and the update check never see it), and
`agentbox image build` makes it on the user's machine from Debian and
[`internal/image/provision.sh`](internal/image/provision.sh) otherwise, or when the download fails.

- **What may be published**: only what the [Published image](.github/workflows/published-image.yml)
  workflow builds on `main`, the image with no components and without `unpublished`
  (`published.go`): Claude Code, its ACP adapter (it bundles Anthropic's Agent SDK) and Cursor's
  SDK, which we may not redistribute, and which each machine installs itself. Every other tool in
  `tools.txt` must be in `toolLicenses` with a license that allows it (`TestEveryToolIsPublishedOrNot`).
  Its Debian packages are GPL and others, so the exact package list and where their sources are
  (snapshot.debian.org) go next to it, as the GPL asks. **Never publish an image built any other
  way**, such as a user's own, which has Claude Code in it.
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
  on every PR that changes `internal/image/`.

Agents' temporary files, including `t.TempDir()`, go to a tmpfs on `/t` (`TMPDIR`), capped at 2 GiB as each
machine starts (`agent.CapTmp`, no rebuild): a unix socket's
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

## The hub

The hub, the server half (D44), is its own program in [`hub/`](hub/), in this module:
`hub/cmd/agentbox-hub` and `hub/internal/hub`, with the npm launcher `npx agentbox-hub` in
`hub/npm/` (`hub/scripts/npm-build.sh` builds it, `hub/npm/PUBLISHING.md` publishes it). It was
lintingdotdev/agentbox-hub until #547 brought it back. The two halves share only
[`hubapi/`](hubapi/), the protocol: the hub imports it, and nothing else of AgentBox
(`TestHubImportsOnlyHubapi`). `internal/` can't import the hub, which Go's `internal/` rule
enforces, and `hubapi/` must never import `internal/` or the rest of this module.

```bash
go test ./hub/... ./hubapi/...
go build -o bin/agentbox-hub ./hub/cmd/agentbox-hub
hub/scripts/npm-build.sh 0.0.0-dev     # the npm packages, in hub/npm/dist/
```

CI runs the hub's Go tests with everyone else's; a change to `hub/`, `hubapi/` or `go.mod` also
builds its npm packages and runs `npx agentbox-hub --version` from them (the Hub job).

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
