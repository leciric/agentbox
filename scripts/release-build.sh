#!/usr/bin/env bash
# Builds a release, for the version in desktop/package.json: the AppImage, the
# .deb and the .pacman (the app, with the command-line tool inside each), the
# Windows installer and portable .exe (D94), the command-line tool alone (for
# Linux on x86-64 and arm64, and for macOS, where it is the front end of
# AgentBox's Linux VM), and their checksums, in desktop/dist/release/v<version>/.
# The Windows build runs on Linux, and needs wine for the installer's
# uninstaller. The Mac app is built by the release workflow's macos job, on a
# Mac, and never goes through this script; the macOS command-line tool is
# built there too (--mac-cli), since it needs cgo and the macOS SDK for the
# experimental vz driver, and signing with its entitlement (scripts/mac-sign.sh).
#
# It publishes nothing. .github/workflows/release.yml calls it for every part,
# so what a release is — and what it has to pass — has one definition wherever
# the release is made. release-please (D95) makes the release itself, as a
# draft, and its own commit bumps desktop/package.json and CHANGELOG.md before
# this ever runs: there is no local equivalent of this script that also tags
# and pushes, since release-please owns both.
#
#   scripts/release-build.sh --tag v0.7.1 [--check | --linux | --windows | --mac-cli]
#
# --tag is the tag the caller means to publish: the build refuses to produce
# anything else, so a workflow run can't be given the wrong version by hand.
#
# With no part given, it checks and builds everything but the Mac's (Linux
# then Windows) and writes SHA256SUMS over the lot. The release workflow
# instead runs the parts as separate jobs, in parallel: --check once, then
# --linux and --windows alongside each other and the Mac app and its
# command-line tool (--mac-cli), each part writing only what
# it built to desktop/dist/release/, without a checksum file — the workflow's
# publish job sums the merged result once every part has finished.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version=$(node -p "require('$root/desktop/package.json').version")
tag=v$version
out=$root/desktop/dist/release/$tag

want=
part=all
while [ $# -gt 0 ]; do
  case $1 in
    --tag) want=${2:-}; [ -n "$want" ] || { echo "--tag needs a tag" >&2; exit 2; }; shift 2 ;;
    --check|--linux|--windows|--mac-cli) part=${1#--}; shift ;;
    *) echo "usage: scripts/release-build.sh [--tag v<version>] [--check | --linux | --windows | --mac-cli]" >&2; exit 2 ;;
  esac
done

[ -z "$want" ] || [ "$want" = "$tag" ] || { echo "asked to release $want, but desktop/package.json is $version, which releases as $tag" >&2; exit 1; }

# release-please's own commit lands the version bump and the changelog, so the
# checkout is already clean and $tag already exists, as a draft release, by
# the time this runs. The only thing left to check is that the tag this job
# was asked to build still points at what it built from.
check() {
  [ -z "$(git -C "$root" status --porcelain)" ] || { echo "the checkout has uncommitted changes: commit them, so the release matches a commit" >&2; exit 1; }
  is_draft=$(gh release view "$tag" --json isDraft --jq .isDraft 2>/dev/null) || { echo "$tag has no GitHub release yet" >&2; exit 1; }
  [ "$is_draft" = "true" ] || { echo "$tag is already published: a published release doesn't get rebuilt" >&2; exit 1; }
}

ldflags="-s -w -X agentbox/internal/cli.version=$version -X agentbox/internal/daemon.Version=$version"

build_linux() {
  echo "==> Building AgentBox $version (Linux)"
  npm --prefix "$root/desktop" run dist
  mkdir -p "$out"
  cp "$root/desktop/dist/AgentBox-$version-x86_64.AppImage" "$out/"
  cp "$root/desktop/dist/AgentBox-$version-amd64.deb" "$out/"
  cp "$root/desktop/dist/AgentBox-$version-x64.pacman" "$out/"
  cp "$root/bin/agentbox" "$out/agentbox-$version-linux-amd64"
  # The command-line tool for a Mac is two files: the macOS agentbox, built
  # on a Mac (--mac-cli), and the Linux one it installs in the VM, which it
  # looks for beside itself as agentbox-linux (the README's "On a Mac"):
  # linux-arm64 for Apple silicon, linux-amd64 above for Intel.
  (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$ldflags" -o "$out/agentbox-$version-linux-arm64" ./cmd/agentbox)
  chmod +x "$out"/*
  "$out/agentbox-$version-linux-amd64" --version | grep -qx "agentbox version $version" || { echo "the binary isn't version $version" >&2; exit 1; }
}

# build_mac_cli builds the macOS agentbox for Apple silicon and Intel, with
# cgo, and signs each with the Virtualization entitlement: with
# MAC_SIGN_IDENTITY's certificate when it's set, ad hoc when it isn't.
build_mac_cli() {
  echo "==> Building AgentBox $version's command-line tool (macOS)"
  [ "$(uname -s)" = Darwin ] || { echo "the macOS agentbox is built on a Mac: it needs cgo and the macOS SDK" >&2; exit 1; }
  mkdir -p "$out"
  for arch in arm64 amd64; do
    (cd "$root" && CGO_ENABLED=1 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$out/agentbox-$version-darwin-$arch" ./cmd/agentbox)
  done
  "$root/scripts/mac-sign.sh" ${MAC_SIGN_IDENTITY:+--identity "$MAC_SIGN_IDENTITY"} "$out"/agentbox-"$version"-darwin-*
  here=$(uname -m); [ "$here" = x86_64 ] && here=amd64
  "$out/agentbox-$version-darwin-$here" --version | grep -qx "agentbox version $version" || { echo "the binary isn't version $version" >&2; exit 1; }
}

build_windows() {
  echo "==> Building AgentBox $version (Windows)"
  npm --prefix "$root/desktop" run dist -- --win
  mkdir -p "$out"
  cp "$root/desktop/dist/AgentBox-$version-x64-setup.exe" "$out/"
  cp "$root/desktop/dist/AgentBox-$version-x64-portable.exe" "$out/"
}

case $part in
  check) check ;;
  linux) rm -rf "$out"; mkdir -p "$out"; build_linux ;;
  windows) rm -rf "$out"; mkdir -p "$out"; build_windows ;;
  mac-cli) rm -rf "$out"; mkdir -p "$out"; build_mac_cli ;;
  all)
    check
    rm -rf "$out"
    mkdir -p "$out"
    build_linux
    build_windows
    (cd "$out" && sha256sum AgentBox-* agentbox-* >SHA256SUMS && cat SHA256SUMS)
    ;;
esac
echo "==> $tag ($part) is built in $out"
