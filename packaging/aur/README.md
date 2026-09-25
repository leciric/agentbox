# AgentBox as an Arch package

A PKGBUILD that installs this checkout as the `agentbox` package, without the
AppImage. It builds the command-line tool and the desktop app here, and packages
both, with the app's own Electron (Electron 44), since Arch's `electron` is
older and the app doesn't use it.

## Build and install

```bash
cd packaging/aur
makepkg -si
```

`makepkg` builds from the working tree, so commit or stash what you want in the
package: only what is on disk is copied, but the build does not touch your tree
(everything happens in `src/`).

The build takes a few minutes the first time: `mise install` fetches the Go and
Node versions `mise.toml` pins, `npm ci` fetches the app's dependencies, and
electron-builder downloads Electron 44.

## What it installs

| Path | What |
|---|---|
| `/usr/bin/agentbox` | The command-line tool and daemon |
| `/usr/lib/agentbox-desktop/` | The desktop app, with its own Electron |
| `/usr/bin/agentbox-desktop` | Launcher: runs the app with `AGENTBOX_BIN=/usr/bin/agentbox` |
| `/usr/share/applications/agentbox-desktop.desktop` | Menu entry |
| `/usr/share/icons/hicolor/…` | Icon |

`AGENTBOX_BIN` is what makes the app use the packaged command-line tool instead
of copying its own into `~/.local/share/agentbox/bin` and asking Setup to link
it. The app and the terminal always run the same version.

Setting up a machine afterwards (Incus, groups, the base image) is the same as
the AppImage: open Setup in the app, or follow the root
[README](../../README.md#set-up).

## Updating

`pkgver` must match `version` in `desktop/package.json`; the build refuses to
run when they differ. On a release, bump both, set `pkgrel=1`, and rebuild.

## Limitations

- **Arch, x86_64, only.** Like the released AppImage.
- **Not reproducible in the AUR's sense.** It builds the working tree rather
  than a release tarball with checksums, so it is for local use, not for
  publishing as-is.
