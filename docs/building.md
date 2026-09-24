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

Model a release on [v0.3.2](https://github.com/leciric/agentbox/releases/tag/v0.3.2) (a fix) or
[v0.3.0](https://github.com/leciric/agentbox/releases/tag/v0.3.0) (features):

1. Get the changes onto `main`, and run `go test ./...` there.
2. Bump the version: `version` in `desktop/package.json`, and the two root `version` fields in
   `desktop/package-lock.json`. A patch for fixes, a minor for features.
3. Write `.github/releases/v<version>.md` — what changed, `## Downloads` (the three packaged
   files), for a fix `## What went wrong`, and `## Known limitations`.
4. Commit as `chore(release): <version>` and land it on `main`.
5. Cut the release, either:
   - the [Release workflow](../.github/workflows/release.yml) from the Actions tab, with
     `v<version>` as the tag — it builds `main`, tags the commit it built, and publishes the
     release; or
   - `scripts/release.sh` locally, when the Actions tab isn't an option — it needs `gh` logged
     in, pushes `main` itself, and tags whatever `HEAD` is once the build finishes, so **nothing
     should be committed while it runs**. `scripts/release.sh --dry-run` does everything except
     the push and the release, leaving the built files in
     `desktop/dist/release/v<version>/`.

Both paths run the same check-and-build script,
[`scripts/release-build.sh`](../scripts/release-build.sh), so a release means one thing wherever
it's cut: nothing uncommitted, release notes that exist and have no `TODO`, a version matching
the tag asked for, a version not already released, and — once built — a command-line tool that
reports that version. It produces
`AgentBox-<version>-x86_64.AppImage`, `AgentBox-<version>-amd64.deb`,
`AgentBox-<version>-x64.pacman`, `agentbox-<version>-linux-amd64` and `SHA256SUMS`.

The [Release workflow](../.github/workflows/release.yml) additionally checks the tag's format,
confirms it matches `desktop/package.json`'s version, and refuses to move a tag that already
points at another commit — a published tag never moves. The whole build takes a few minutes.
