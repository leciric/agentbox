# Contributing to AgentBox

## Read this first

AgentBox is young, and where it goes is still being decided. Contributions are welcome, but the ones
most likely to land are small and focused:

- a bug fix, with a test that fails without it;
- a reliability or performance fix you can show the before and after of;
- a correction to the docs, where they say something the code doesn't do.

Less likely to land: large pull requests, new features nobody asked for, rewrites, and anything that
widens what AgentBox is for. **If you want to build something bigger, open an issue first** and say
what you have in mind. It saves you writing something that won't be merged.

Opening a pull request doesn't oblige anyone to merge it. It may be closed, asked to shrink, or
reimplemented differently later.

## What you need

- **Linux on x86_64.** AgentBox runs its agents in [Incus](https://linuxcontainers.org/incus/)
  containers, and Incus is Linux-only. Arch, Debian 12 or 13, Ubuntu 22.04 or newer, and Fedora are
  known to work. macOS and Windows aren't supported.
- **[mise](https://mise.jdx.dev/)**, for Go and Node. [`mise.toml`](mise.toml) pins them: `mise install`
  in the checkout installs Go 1.27, Node LTS and the rest.
- **Incus, set up for your user**, to run agents at all. `sudo bash scripts/host-setup.sh` does it once
  per machine: Incus, the firewall rules and the UID mapping. You don't have to log out afterwards.
  Unit tests and the desktop build don't need it.
- A **Claude Code, Codex or OpenCode** login, if you want an agent to do real work.

## Build and run it

```bash
git clone https://github.com/leciric/agentbox.git
cd agentbox
mise install

go build -o bin/agentbox ./cmd/agentbox      # the CLI and the daemon, one binary; reports "agentbox version dev"
sudo bash scripts/host-setup.sh              # once per machine
bin/agentbox image build                     # the agents' base image, built here in a few minutes
bin/agentbox auth claude                     # runs `claude setup-token`
```

**The daemon** is the same binary. Any command starts it on demand, in the background; to watch its
log, run it in the foreground instead:

```bash
bin/agentbox daemon stop      # if a command already started one
bin/agentbox daemon           # serves ~/.local/share/agentbox/run/agentbox.sock
```

**The CLI** talks to that socket:

```bash
bin/agentbox add ~/src/my-project
bin/agentbox create my-project
bin/agentbox chat my-project/agent-01 "Why does the login fail?"
bin/agentbox list
```

**The desktop app** is an Electron client of the same socket. Point it at the binary you built:

```bash
npm --prefix desktop install
cd desktop && AGENTBOX_BIN=$PWD/../bin/agentbox npm start
```

`npm --prefix desktop run dist` builds `bin/agentbox` with the version in `desktop/package.json`, and
the AppImage, `.deb` and `.pacman` in `desktop/dist/`. `bin/` is gitignored: rebuild after pulling.

Before you change anything, read [AGENTS.md](AGENTS.md): the short tour of the codebase that
AgentBox's own agents are given, and a good one for people too.

## Tests

```bash
go vet ./...
go test ./...                               # everything that runs without Incus
npm --prefix desktop run typecheck
npm --prefix desktop run build
```

- **Integration tests** are behind the `integration` build tag. They create real Incus containers, so
  they need a host set up with `scripts/host-setup.sh` and the base image built with
  `bin/agentbox image build`. They skip what they can't reach (a base image that isn't built, a
  display for the browser tests, `opencode` if it isn't installed). They can't run inside an AgentBox
  agent, which has no Incus.

  ```bash
  go test -tags integration ./internal/agent
  go test -tags integration -p 1 ./internal/daemon ./internal/agent
  ```

- **The API types are generated.** The renderer's types in
  [`desktop/src/shared/api.ts`](desktop/src/shared/api.ts) come from `internal/api`. Change
  `internal/api` and `TestTypeScriptTypesAreUpToDate` fails until you regenerate them:

  ```bash
  UPDATE_TS=1 go test ./internal/api
  ```

- **The agent's brief has golden files.** Change
  [`internal/brief/brief.md.tmpl`](internal/brief/brief.md.tmpl) and update them:

  ```bash
  go test ./internal/brief -update
  ```

- **Demo scripts** in [`scripts/demo/`](scripts/demo/) check features end to end. Many need no
  Incus and no login: they stand up a throwaway daemon with a stub `incus` and drive the real app.
  The comment at the top of each script says what it needs and how to run it.

## The rail and the sidebar

Both are narrow, fixed-width columns filled with whatever agents write, so a long path, an unbroken
URL or a `<pre>` without `min-w-0` drags them wider than they're allowed to be. A preview harness
stands both up against fixtures made to trigger exactly that:

```bash
npm --prefix desktop run preview                                    # serve it, print the URL
npm --prefix desktop run preview -- --shots out                     # screenshot every scenario into out/
npm --prefix desktop run preview -- --shots out --against HEAD~1    # and diff against another commit
```

The scenarios are in `desktop/src/renderer/dev/scenarios.json`. If Playwright can't launch its
browser, run `npx playwright install chromium` once. If your change touches either column, put the
before and after in your pull request.

## Conventions

- **Conventional commits:** `feat: ...`, `fix: ...`, `refactor: ...`, `docs: ...`, `test: ...`,
  `chore: ...`.
- **Migrations are appended, never edited.** `migrations` in
  [`internal/state/state.go`](internal/state/state.go) is one ordered list, tracked with
  `PRAGMA user_version`. Editing a past entry leaves already-migrated databases behind.
- **Record a decision** in your pull request when the shape of something is worth recording: the
  context, what was decided, what was rejected, and what proves it. Code comments cite earlier
  decisions by number (D1–D90); their records aren't in this repository.
- **Explain a feature worth explaining** in its pull request: what it does, what was checked, where
  the code is, and what it still can't do.
- **The desktop app stays a thin client.** Its main process relays the daemon's socket to the
  renderer and nothing else: AgentBox logic, Incus and git belong in the daemon.
- **Changing [`internal/image/provision.sh`](internal/image/provision.sh) means bumping
  `image.Version`** in [`internal/image/image.go`](internal/image/image.go). That is what tells Setup
  the installed image is outdated. The [Base image](.github/workflows/base-image.yml) workflow
  builds it on every change to `internal/image/`, and publishes nothing: every machine builds its
  own image, because it holds software we may not redistribute. `sudo scripts/check-personalise.sh` checks `personalise.sh`
  without Incus.

## The hub

The hub, the server half behind remote environments, is a separate program in a **private**
repository, [leciric/agentbox-hub](https://github.com/leciric/agentbox-hub). Everything that
connects to a hub is here.

The protocol between the two, [`hubapi/`](hubapi/), is shared: the hub keeps a byte-identical copy.
Nothing checks the copies against each other, so a pull request that changes `hubapi/` needs a
matching change in the hub, which a maintainer makes. `internal/` never imports anything from the hub.

## Pull requests

1. Branch from `main` and keep the change to one thing. Don't mix unrelated fixes.
2. Run `go vet ./...`, `go test ./...` and the desktop `typecheck` and `build` before you push.
3. Say what changed and why, and how you checked it. If you can't say how you checked it, it isn't
   ready.
4. For anything visible in the app, include before-and-after screenshots; for something that moves
   or is a flow, a short recording.
5. If the change alters how something works, say so, and record the decision if the shape of it
   changed.
6. Sign off every commit (see [below](#license-and-the-dco)).

[CI](.github/workflows/ci.yml) runs on every pull request: `go vet ./...`,
`go vet -tags integration ./...` and `go test ./...` in one job, and `npm ci`, `typecheck` and `build`
for the desktop app in another. It doesn't run the integration tests (GitHub's runners have no
Incus) or package the AppImage; both are yours to check when your change touches them.

Releases are made by the maintainer from `main`, as described in [AGENTS.md](AGENTS.md#releasing).

## Reporting bugs

[Open an issue](https://github.com/leciric/agentbox/issues/new/choose) with the version (`agentbox
--version`, or the AppImage's name), your distribution, what you did, and what happened. The
daemon's log is `~/.local/share/agentbox/daemon.log`. **Redact tokens, API keys and private paths**
before you paste it.

A security problem doesn't belong in a public issue: contact the maintainer privately first.

## License and the DCO

AgentBox is fair source, licensed under the [Functional Source License, version 1.1, ALv2 Future
License](LICENSE) (FSL-1.1-ALv2), except [`hubapi/`](hubapi/), which is under
the [Apache License 2.0](hubapi/LICENSE) because the private hub keeps a copy of it.

Every commit in a pull request has to be signed off, certifying the [Developer Certificate of
Origin](DCO.md): that you wrote it, or otherwise have the right to submit it under the project's
licence. Add `-s` when you commit:

```bash
git commit -s
```

That appends a `Signed-off-by: Your Name <your.email@example.com>` trailer matching your git
`user.name` and `user.email`. If you forget on commits you haven't pushed yet, add it after the
fact:

```bash
git rebase --signoff main
```

The [DCO check](.github/workflows/dco.yml) fails the pull request if any commit is missing a
`Signed-off-by` that matches its author.
