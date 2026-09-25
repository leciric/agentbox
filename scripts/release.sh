#!/usr/bin/env bash
# Builds a release and publishes it on GitHub: the AppImage, the .deb and the
# .pacman (the app, with the command-line tool inside each), the command-line
# tool alone, and their checksums, for the version in desktop/package.json.
# The notes are .github/releases/v<version>.md.
# Run it from a checkout of the commit to release, with nothing uncommitted.
#
#   scripts/release.sh [--dry-run]
#
# This is the local alternative to the Release workflow
# (.github/workflows/release.yml), which does the same thing on GitHub from the
# Actions tab. Both check and build through scripts/release-build.sh; what this
# one adds is the push and the GitHub release.
#
# --dry-run checks and builds everything, then stops before pushing main and
# creating the release, and says what it would have run.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)

dry=false
while [ $# -gt 0 ]; do
  case $1 in
    --dry-run) dry=true; shift ;;
    *) echo "usage: scripts/release.sh [--dry-run]" >&2; exit 2 ;;
  esac
done

version=$(node -p "require('$root/desktop/package.json').version")
tag=v$version
notes=$root/.github/releases/$tag.md
out=$root/desktop/dist/release/$tag

"$root/scripts/release-build.sh" --tag "$tag"

commit=$(git -C "$root" rev-parse HEAD)
if $dry; then
  echo "==> --dry-run: stopping before publishing $tag at $(git -C "$root" rev-parse --short HEAD)"
  echo "    would run: git push origin HEAD:main"
  echo "    would run: gh release create $tag --target $commit --title \"AgentBox $version\" --notes-file $notes $out/*"
  ls -l "$out"
  exit 0
fi

echo "==> Publishing $tag on GitHub, at $(git -C "$root" rev-parse --short HEAD)"
git -C "$root" push origin HEAD:main
gh release create "$tag" --target "$commit" --title "AgentBox $version" --notes-file "$notes" "$out"/*
gh release view "$tag" --json url,assets --jq '.url, (.assets[] | "  \(.name)  \(.size) bytes")'
