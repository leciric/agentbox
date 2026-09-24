#!/usr/bin/env bash
# Checks a release and builds it, for the version in desktop/package.json: the
# AppImage, the .deb and the .pacman (the app, with the command-line tool
# inside each), the Windows installer and portable .exe (D94), the command-line
# tool alone (for Linux on x86-64 and arm64, and for macOS, where it is the
# front end of AgentBox's Linux VM), and their checksums, in
# desktop/dist/release/v<version>/. The Windows build runs on Linux, and needs
# wine for the installer's uninstaller. The Mac app is built by the release
# workflow's macos job, on a Mac.
#
# It publishes nothing. scripts/release.sh and .github/workflows/release.yml
# both call it, so what a release is — and what it has to pass — has one
# definition wherever the release is made.
#
#   scripts/release-build.sh [--tag v0.7.1]
#
# --tag is the tag the caller means to publish: the build refuses to produce
# anything else, so a workflow run can't be given the wrong version by hand.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version=$(node -p "require('$root/desktop/package.json').version")
tag=v$version
notes=$root/.github/releases/$tag.md
out=$root/desktop/dist/release/$tag

want=
while [ $# -gt 0 ]; do
  case $1 in
    --tag) want=${2:-}; [ -n "$want" ] || { echo "--tag needs a tag" >&2; exit 2; }; shift 2 ;;
    *) echo "usage: scripts/release-build.sh [--tag v<version>]" >&2; exit 2 ;;
  esac
done

[ -z "$want" ] || [ "$want" = "$tag" ] || { echo "asked to release $want, but desktop/package.json is $version, which releases as $tag" >&2; exit 1; }
[ -z "$(git -C "$root" status --porcelain)" ] || { echo "the checkout has uncommitted changes: commit them, so the release matches a commit" >&2; exit 1; }
[ -f "$notes" ] || { echo "no release notes in $notes" >&2; exit 1; }
! grep -q 'TODO' "$notes" || { echo "$notes still has a TODO" >&2; exit 1; }
if gh release view "$tag" >/dev/null 2>&1; then
  echo "$tag is already released" >&2
  exit 1
fi

echo "==> Building AgentBox $version"
npm --prefix "$root/desktop" run dist
npm --prefix "$root/desktop" run dist -- --win
rm -rf "$out"
mkdir -p "$out"
cp "$root/desktop/dist/AgentBox-$version-x86_64.AppImage" "$out/"
cp "$root/desktop/dist/AgentBox-$version-amd64.deb" "$out/"
cp "$root/desktop/dist/AgentBox-$version-x64.pacman" "$out/"
cp "$root/desktop/dist/AgentBox-$version-x64-setup.exe" "$out/"
cp "$root/desktop/dist/AgentBox-$version-x64-portable.exe" "$out/"
cp "$root/bin/agentbox" "$out/agentbox-$version-linux-amd64"
# The command-line tool for a Mac is two files: the macOS agentbox, and the
# Linux one it installs in the VM, which it looks for beside itself as
# agentbox-linux (the README's "On a Mac").
ldflags="-s -w -X agentbox/internal/cli.version=$version -X agentbox/internal/daemon.Version=$version"
for target in linux/arm64 darwin/arm64 darwin/amd64; do
  (cd "$root" && CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} go build -trimpath -ldflags "$ldflags" -o "$out/agentbox-$version-${target%/*}-${target#*/}" ./cmd/agentbox)
done
chmod +x "$out"/*
"$out/agentbox-$version-linux-amd64" --version | grep -qx "agentbox version $version" || { echo "the binary isn't version $version" >&2; exit 1; }
(cd "$out" && sha256sum AgentBox-* agentbox-* >SHA256SUMS && cat SHA256SUMS)
echo "==> $tag is built in $out"
