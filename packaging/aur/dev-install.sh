#!/usr/bin/env bash
# Build this checkout and install it as the `agentbox` package, for development.
#
#   packaging/aur/dev-install.sh                # build, sudo pacman -U, stop the daemon
#   packaging/aur/dev-install.sh --build-only   # only leave the package file
#   packaging/aur/dev-install.sh --restart-app  # and reopen the desktop app
#
# Anything after the flags goes to makepkg, so `--nocheck` or `-L` work too.
#
# The package is built from the working tree, so uncommitted changes go in:
# makepkg builds in packaging/aur/src and leaves the checkout alone.
#
# Two parts matter after the build. `pacman -U` replaces /usr/bin/agentbox on
# disk, but a daemon already running keeps the executable it started with, so
# this stops it and the next command starts the new one. The window is renderer
# code from app.asar, which an install does not reload, so --restart-app closes
# and reopens it; a change to the Go daemon alone does not need that.
#
# `pkgver` in the PKGBUILD only moves on a release, and a checkout usually
# differs from it, which makes the build refuse to run. This copies the version
# from desktop/package.json into `pkgver` for the run and gives `pkgrel` a
# timestamp, so a dev build sorts above whatever is installed (0.16.0-7, or a
# release) instead of looking like a downgrade. The PKGBUILD is put back
# afterwards, so a dev build never rewrites the committed file.
#
# Needs makepkg, and sudo for the install.

set -euo pipefail

die() { printf 'dev-install: %s\n' "$*" >&2; exit 1; }

install=1
restart_app=0
while [ $# -gt 0 ]; do
  case "$1" in
    -b|--build-only) install=0; shift ;;
    -r|--restart-app) restart_app=1; shift ;;
    -h|--help) sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    --) shift; break ;;
    *) break ;;
  esac
done

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo=$(realpath "$here/../..")
pkgbuild=$here/PKGBUILD
pkgname=agentbox
arch=$(uname -m)

[ -f "$pkgbuild" ] || die "no PKGBUILD next to this script"
[ -f "$repo/desktop/package.json" ] || die "no desktop/package.json in $repo"
command -v makepkg >/dev/null || die "no makepkg: this is the Arch package"

version=$(sed -n 's/^  "version": "\([^"]*\)",$/\1/p' "$repo/desktop/package.json" | head -n1)
[ -n "$version" ] || die "could not read the version from desktop/package.json"

# A timestamp pkgrel, so this build sorts above anything installed. vercmp
# compares it as a number, and 20261029150159 is greater than any release's.
pkgrel=$(date -u +%Y%m%d%H%M%S)

backup=$(mktemp -t agentbox-pkgbuild.XXXXXX)
cp "$pkgbuild" "$backup"
restore() { cp "$backup" "$pkgbuild"; rm -f "$backup"; }
trap restore EXIT

sed -i "s/^pkgver=.*/pkgver=$version/; s/^pkgrel=.*/pkgrel=$pkgrel/" "$pkgbuild"

pkgfile=$here/$pkgname-$version-$pkgrel-$arch.pkg.tar.zst

printf 'building %s %s-%s from %s\n' "$pkgname" "$version" "$pkgrel" "$repo"
( cd "$here" && makepkg --force "$@" )

[ -f "$pkgfile" ] || die "makepkg finished but there is no $pkgfile"

if [ "$install" -eq 0 ]; then
  printf '\nbuilt %s\ninstall it with: sudo pacman -U %s\n' "$pkgfile" "$pkgfile"
  exit 0
fi

if [ ! -t 0 ] && ! sudo -n true 2>/dev/null; then
  printf '\nbuilt %s\n' "$pkgfile"
  printf 'no terminal to ask for the sudo password: run\n  sudo pacman -U %s\n' "$pkgfile"
  exit 1
fi

printf '\n==> Installing %s\n' "$pkgfile"
sudo pacman -U --noconfirm "$pkgfile"

# The installed binary, not whatever `agentbox` resolves to: a ~/.local/bin link
# may still point at this checkout, which would hide a broken package.
if /usr/bin/agentbox daemon stop >/dev/null 2>&1; then
  echo "==> Stopped the running daemon; running jobs were cancelled and rolled back"
else
  echo "==> No daemon was running"
fi

app=/usr/lib/agentbox-desktop/agentbox-desktop
launcher=/usr/bin/agentbox-desktop
if [ "$restart_app" -eq 1 ]; then
  if pgrep -f "$app" >/dev/null; then
    echo "==> Restarting the desktop app"
    pkill -f "$app" || true
    for _ in $(seq 1 100); do pgrep -f "$app" >/dev/null || break; sleep 0.1; done
    setsid -f "$launcher" >/dev/null 2>&1
  else
    echo "==> The desktop app is not running; open it to test"
  fi
else
  echo "==> Done. Reopen the desktop app to load changed UI code."
fi
