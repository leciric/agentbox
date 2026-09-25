# AgentBox as an AUR binary package

A PKGBUILD that installs `agentbox-bin`, a real AUR submission: it downloads the
`AgentBox-<version>-x86_64.AppImage` and `agentbox-<version>-linux-amd64` assets from a
[GitHub release](https://github.com/leciric/agentbox/releases) and checks them against the
release's own `SHA256SUMS`, instead of building from source like [`../aur`](../aur).

## Build and install

```bash
cd packaging/aur-bin
makepkg -si
```

The AppImage is extracted with `--appimage-extract` (no FUSE needed) and repackaged the same way
[`../aur`](../aur)'s PKGBUILD lays out a from-source build: the command-line tool at
`/usr/bin/agentbox`, the app at `/usr/lib/agentbox-desktop/`, and `/usr/bin/agentbox-desktop` as its
launcher, so the two packages conflict and provide `agentbox` for each other.

## Updating for a new release

`pkgver`'s two download sums (the AppImage and the CLI binary) and the `LICENSE` sum are release
build artifacts and can't be known before a release is published. After tagging and publishing a
release:

```bash
packaging/aur-bin/update-checksums.sh [version]   # defaults to PKGBUILD's own pkgver
```

This fetches the release's `SHA256SUMS`, fills in `PKGBUILD`'s `sha256sums`, bumps `pkgver` and
resets `pkgrel=1`, then regenerates `.SRCINFO` with `makepkg --printsrcinfo` if `makepkg` is
available. `updpkgsums` (from Arch's `pacman-contrib`) is the usual alternative — it downloads and
hashes the files itself instead of trusting the published `SHA256SUMS`, which is a stronger check
but needs `makepkg` (and so an Arch machine) to run at all.

For a fix release that only bumps `pkgrel` (no new upstream version), edit `pkgrel` by hand and
regenerate `.SRCINFO`.

## Checking it

`namcap PKGBUILD` and, after a build, `namcap agentbox-bin-*.pkg.tar.zst` want an Arch machine; this
repository's own checks (`go test ./...`, CI) don't run either, since nothing here is Arch. Do both
before submitting a checksum update.

## What it installs

Same layout as [`../aur`](../aur#what-it-installs).
