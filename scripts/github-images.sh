#!/usr/bin/env bash
# Publishes the base image to this repository's GitHub releases, and deletes
# every other image release:
#
#   release image-<version>, a prerelease, never latest, with
#   agentbox-base-<arch>.tar.zst.000, …       the image, compressed with zstd, in parts
#   agentbox-base-<arch>.tar.gz.000, …        the same gzipped, while image.gzipUntil says
#   image-<arch>.json                         what it is
#   packages-<arch>.txt                       its Debian packages, with versions
#   SOURCES-<arch>.md                         where their sources are, the tools' licenses
#   SHA256SUMS                                the sums of all of the above, every architecture
#
# (internal/image/published.go). The Published image workflow runs it, with a
# directory per architecture as `agentbox image build-for-publishing` wrote it:
#
#   scripts/github-images.sh out/amd64 [out/arm64]
#
# Every image must be of the same version. Only the newest image release stays,
# so an AgentBox built for an older version finds nothing to download and builds
# its image itself. An architecture not given keeps whatever image of this
# version the release already has. SHA256SUMS goes last, and is removed first:
# a machine downloading meanwhile finds nothing, and builds its own image.
#
# It needs gh (GH_TOKEN, with contents: write), python3 and GITHUB_SHA (or
# TARGET_COMMITISH) for a new release's tag.
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: $0 <dir>..." >&2; exit 2; }

field() { python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }

version= archs=()
for dir in "$@"; do
  (cd "$dir" && sha256sum --quiet -c SHA256SUMS)
  manifest=$(ls "$dir"/image-*.json)
  v=$(field "$manifest" version)
  [ -z "$version" ] || [ "$v" = "$version" ] || { echo "$dir is version $v, not $version" >&2; exit 1; }
  version=$v
  archs+=("$(field "$manifest" arch)")
done
tag=image-$version
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if gh release view "$tag" >/dev/null 2>&1; then
  echo "==> $tag exists: replacing the assets of ${archs[*]}"
  gh release edit "$tag" --prerelease --latest=false >/dev/null
  # The old sums first, for the architectures not given, and then out of the way.
  gh release download "$tag" -p SHA256SUMS -O "$tmp/old" 2>/dev/null || : >"$tmp/old"
  gh release delete-asset "$tag" SHA256SUMS -y 2>/dev/null || true
  for arch in "${archs[@]}"; do
    for name in $(gh release view "$tag" --json assets --jq '.assets[].name' | grep -e "-$arch\." || true); do
      gh release delete-asset "$tag" "$name" -y
    done
    grep -v -e "-$arch\." "$tmp/old" >"$tmp/old.next" || true
    mv "$tmp/old.next" "$tmp/old"
  done
else
  echo "==> Creating $tag"
  cat >"$tmp/notes" <<NOTES
The prebuilt agent base image for AgentBox image version $version, which Setup downloads instead of building one from Debian. Nothing to install by hand.

Built by the Published image workflow from \`${GITHUB_SHA:-main}\`. The image is split into parts, which Setup joins and checks against \`SHA256SUMS\`. \`SOURCES-<arch>.md\` and \`packages-<arch>.txt\` say what is in it and where the sources of its Debian packages are.
NOTES
  gh release create "$tag" --title "Base image $version" --notes-file "$tmp/notes" \
    --prerelease --latest=false --target "${GITHUB_SHA:-${TARGET_COMMITISH:-main}}"
  : >"$tmp/old"
fi

for dir in "$@"; do
  echo "==> Uploading $dir"
  files=()
  for f in "$dir"/*; do
    case $(basename "$f") in SHA256SUMS) ;; *) files+=("$f") ;; esac
  done
  gh release upload "$tag" "${files[@]}" --clobber
  cat "$dir/SHA256SUMS" >>"$tmp/old"
done
cp "$tmp/old" "$tmp/SHA256SUMS"
gh release upload "$tag" "$tmp/SHA256SUMS" --clobber

# Only the newest image release stays, with its tag.
for old in $(gh release list --limit 200 --json tagName --jq '.[].tagName' | grep '^image-' || true); do
  [ "$old" = "$tag" ] && continue
  echo "==> Deleting $old"
  gh release delete "$old" --cleanup-tag -y
done
echo "Published base image $version at $(gh release view "$tag" --json url --jq .url)"
