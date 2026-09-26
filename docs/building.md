# Building, testing and releasing

[← back to the index](README.md)

## Build and test

```bash
go test ./...
go build -o bin/agentbox ./cmd/agentbox   # reports "agentbox version dev"
npm --prefix desktop run dist             # bin/agentbox with the version in desktop/package.json,
                                           # plus the AppImage, .deb and .pacman in desktop/dist/
```

`bin/` is gitignored, so rebuild it after pulling. `npm --prefix desktop run dist`
(`desktop/scripts/dist.mjs`) builds the Go binary with the version injected via `ldflags`,
compiles the app (`npm run build`), and calls `electron-builder --linux AppImage deb pacman` to
package both together.

## CI

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on every push to `main` and every
pull request:

- `go vet ./...`, and `go vet -tags integration ./...` — vetting the Incus-dependent tests
  without running them, since CI's runner can't run Incus itself;
- `go test ./...`;
- the desktop app's `npm run typecheck` and `npm run build` (with
  `ELECTRON_SKIP_BINARY_DOWNLOAD=1`, so it doesn't need to fetch Electron's binary just to
  type-check and bundle).

It doesn't package the AppImage — that takes minutes and belongs to a release — and it can't run
the `integration`-tagged Incus tests themselves, only vet them.

## Releasing

Releasing means merging a pull request, not running a script.
[release-please](https://github.com/googleapis/release-please) watches every push to `main` and
keeps a single open "release PR" bumping `version` in `desktop/package.json` and
`desktop/package-lock.json`'s two root `version` fields, and rewriting `CHANGELOG.md` from the pull
requests merged since the last release — every PR's title is a changelog line, so title it for a
user reading the changelog.

Merging that PR is the release: the [Release workflow](../.github/workflows/release.yml) tags the
merge commit, opens a draft GitHub release, and builds
`AgentBox-<version>-x86_64.AppImage`, `AgentBox-<version>-amd64.deb`,
`AgentBox-<version>-x64.pacman`, the Windows installer and portable `.exe`, the command-line tool
for Linux, macOS and (as the front end of the Mac's Linux VM) `darwin`, and the Mac app when the
repository has Apple's signing secrets. Only once every build has succeeded does it upload the
assets, write `SHA256SUMS`, and take the release off draft — a failed build leaves it a draft,
visible to collaborators only, until a rerun gets everything green. The whole build takes a few
minutes.
