#!/usr/bin/env bash
# Builds a release, for the version in desktop/package.json: the AppImage (and
# latest-linux.yml, its update feed), the .deb and the .pacman (the app, with
# the command-line tool inside each), the Windows installer and portable .exe
# (D94), the command-line tool alone (for Linux on x86-64 and arm64, and for
# macOS, where it is the front end of AgentBox's Linux VM), and their
# checksums, in desktop/dist/release/v<version>/.
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
#   scripts/release-build.sh --nightly 20260929.12 --tag v0.8.0-nightly.20260929.12 [--check | --stamp | --linux | ...]
#
# --tag is the tag the caller means to publish: the build refuses to produce
# anything else, so a workflow run can't be given the wrong version by hand.
#
# --nightly <YYYYMMDD>.<run> builds a nightly instead (.github/workflows/nightly.yml):
# desktop/package.json's version, which on the release PR is already the next
# release, becomes <version>-nightly.<YYYYMMDD>.<run>, a semver prerelease
# that sorts before the release it leads up to and after every nightly before
# it. The version is written into desktop/package.json and package-lock.json
# for the build, since everything built reads it from there, and put back
# afterwards; --stamp only writes it, for the Mac app, which the workflow
# builds without this script. A nightly's --check wants a clean checkout and
# no release with its tag yet, since the nightly workflow makes the release
# itself, after the builds, rather than release-please making a draft first.
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

want=
nightly=
part=all
while [ $# -gt 0 ]; do
  case $1 in
    --tag) want=${2:-}; [ -n "$want" ] || { echo "--tag needs a tag" >&2; exit 2; }; shift 2 ;;
    --nightly) nightly=${2:-}; [[ $nightly =~ ^[0-9]{8}\.[1-9][0-9]*$ ]] || { echo "--nightly needs <YYYYMMDD>.<run>, not \"$nightly\"" >&2; exit 2; }; shift 2 ;;
    --check|--stamp|--linux|--windows|--mac-cli) part=${1#--}; shift ;;
    *) echo "usage: scripts/release-build.sh [--nightly <YYYYMMDD>.<run>] [--tag v<version>] [--check | --stamp | --linux | --windows | --mac-cli]" >&2; exit 2 ;;
  esac
done
[ -n "$nightly" ] || [ "$part" != stamp ] || { echo "--stamp is for a nightly: give --nightly too" >&2; exit 2; }

version=$(node -p "require('$root/desktop/package.json').version")
if [ -n "$nightly" ]; then
  # From the release's version, or from a nightly's, when a job stamped it
  # already: the same nightly either way.
  release=${version%%-*}
  [[ $release =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "desktop/package.json is $version, which isn't a release a nightly can lead up to" >&2; exit 1; }
  version=$release-nightly.$nightly
fi
tag=v$version
out=$root/desktop/dist/release/$tag

[ -z "$want" ] || [ "$want" = "$tag" ] || { echo "asked to release $want, but desktop/package.json is $version, which releases as $tag" >&2; exit 1; }

# release-please's own commit lands the version bump and the changelog, so the
# checkout is already clean and $tag already exists, as a draft release, by
# the time this runs. The only thing left to check is that the tag this job
# was asked to build still points at what it built from.
check() {
  [ -z "$(git -C "$root" status --porcelain)" ] || { echo "the checkout has uncommitted changes: commit them, so the release matches a commit" >&2; exit 1; }
  if [ -n "$nightly" ]; then
    # gh answers "release not found" the same whether or not the tag exists
    # without a release, so ask for the tag as well.
    ! gh release view "$tag" >/dev/null 2>&1 || { echo "$tag is already released: a nightly is never rebuilt, the next one gets a new run number" >&2; exit 1; }
    ! git -C "$root" ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1 || { echo "the tag $tag already exists" >&2; exit 1; }
    return
  fi
  is_draft=$(gh release view "$tag" --json isDraft --jq .isDraft 2>/dev/null) || { echo "$tag has no GitHub release yet" >&2; exit 1; }
  [ "$is_draft" = "true" ] || { echo "$tag is already published: a published release doesn't get rebuilt" >&2; exit 1; }
}

# stamp writes a nightly's version into desktop/package.json and
# package-lock.json (its two root version fields, the ones release-please
# bumps), where dist.mjs and electron-builder read it.
stamp() {
  node -e '
    const fs = require("fs");
    const [version, ...files] = process.argv.slice(1);
    for (const f of files) {
      const json = JSON.parse(fs.readFileSync(f, "utf8"));
      json.version = version;
      if (json.packages && json.packages[""]) json.packages[""].version = version;
      fs.writeFileSync(f, JSON.stringify(json, null, 2) + "\n");
    }' "$version" "$root/desktop/package.json" "$root/desktop/package-lock.json"
}

# stamp_for_build is a nightly's build stamping the version for its own sake:
# it puts the files back when it's done, so a build on a developer's checkout
# leaves nothing behind. --stamp is the one part that means to leave it.
stamp_for_build() {
  [ -n "$nightly" ] || return 0
  stamp
  trap 'git -C "$root" checkout -- desktop/package.json desktop/package-lock.json' EXIT
}

ldflags="-s -w -X agentbox/internal/cli.version=$version -X agentbox/internal/daemon.Version=$version"

build_linux() {
  echo "==> Building AgentBox $version (Linux)"
  npm --prefix "$root/desktop" run dist
  mkdir -p "$out"
  cp "$root/desktop/dist/AgentBox-$version-x86_64.AppImage" "$out/"
  cp "$root/desktop/dist/AgentBox-$version-amd64.deb" "$out/"
  cp "$root/desktop/dist/AgentBox-$version-x64.pacman" "$out/"
  # latest-linux.yml is the AppImage's update feed: the app updates itself in
  # place from the release the daemon picks for its channel, reading this file
  # from that release's assets (desktop/src/main/appupdate.ts) for the
  # AppImage's name, size and sha512. Each release, nightly or stable, has its
  # own, under the same name (detectUpdateChannel is off in package.json).
  grep -qx "version: $version" "$root/desktop/dist/latest-linux.yml" || { echo "desktop/dist/latest-linux.yml isn't for $version" >&2; exit 1; }
  cp "$root/desktop/dist/latest-linux.yml" "$out/"
  cp "$root/bin/agentbox" "$out/agentbox-$version-linux-amd64"
  # The command-line tool for a Mac is two files: the macOS agentbox, built
  # on a Mac (--mac-cli), and the Linux one it installs in the VM, which it
  # looks for beside itself as agentbox-linux (the README's "On a Mac"):
  # linux-arm64 for Apple silicon, linux-amd64 above for Intel.
  (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$ldflags" -o "$out/agentbox-$version-linux-arm64" ./cmd/agentbox)
  chmod +x "$out"/AgentBox-* "$out"/agentbox-*
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
  stamp) stamp; echo "==> desktop/package.json is $version" ;;
  linux) stamp_for_build; rm -rf "$out"; mkdir -p "$out"; build_linux ;;
  windows) stamp_for_build; rm -rf "$out"; mkdir -p "$out"; build_windows ;;
  mac-cli) stamp_for_build; rm -rf "$out"; mkdir -p "$out"; build_mac_cli ;;
  all)
    check
    stamp_for_build
    rm -rf "$out"
    mkdir -p "$out"
    build_linux
    build_windows
    (cd "$out" && sha256sum AgentBox-* agentbox-* >SHA256SUMS && cat SHA256SUMS)
    ;;
esac
[ "$part" = stamp ] || echo "==> $tag ($part) is built in $out"
