#!/usr/bin/env bash
# Publishes the ready-made VM disk to this repository's GitHub releases, and
# deletes every other VM disk release:
#
#   release vmdisk-<version>, a prerelease, never latest, with
#   agentbox-vm-<arch>.tar.xz   the disk
#   vm-disk-<arch>.json         what it is
#   vm-packages-<arch>.txt      its Debian packages, with versions
#   VM-SOURCES-<arch>.md        where their sources are
#   SHA256SUMS                  the sums of all of the above, every architecture
#
# (internal/hostvm/chv/vmdisk.go). The Published VM disk workflow runs it, with
# a directory per architecture as `agentbox vm build-disk-for-publishing` wrote
# it:
#
#   scripts/github-vm-disks.sh out/amd64 [out/arm64]
#
# Every disk must be of the same version. Only the newest VM disk release
# stays, so an AgentBox made for an older one finds nothing to download and
# makes its VM from Debian's cloud image. An architecture not given keeps
# whatever disk of this version the release already has. SHA256SUMS goes last,
# and is removed first: a machine downloading meanwhile finds nothing, and
# falls back the same way. The base image's releases (image-*,
# scripts/github-images.sh) are left alone, as that script leaves these.
#
# It needs gh (GH_TOKEN, with contents: write), python3 and GITHUB_SHA (or
# TARGET_COMMITISH) for a new release's tag.
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: $0 <dir>..." >&2; exit 2; }

field() { python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }

version= archs=()
for dir in "$@"; do
  (cd "$dir" && sha256sum --quiet -c SHA256SUMS)
  manifest=$(ls "$dir"/vm-disk-*.json)
  v=$(field "$manifest" version)
  [ -z "$version" ] || [ "$v" = "$version" ] || { echo "$dir is version $v, not $version" >&2; exit 1; }
  version=$v
  archs+=("$(field "$manifest" arch)")
done
tag=vmdisk-$version
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if gh release view "$tag" >/dev/null 2>&1; then
  echo "==> $tag exists: replacing the assets of ${archs[*]}"
  gh release edit "$tag" --prerelease --latest=false >/dev/null
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
The ready-made disk of AgentBox's VM, version $version: Debian 13's cloud image with the packages the VM needs already installed, which \`agentbox vm init\` downloads so that the VM's first boot doesn't install them. Nothing to install by hand.

Built by the Published VM disk workflow from \`${GITHUB_SHA:-main}\`, and checked against \`SHA256SUMS\` when downloaded. \`VM-SOURCES-<arch>.md\` and \`vm-packages-<arch>.txt\` say what is in it and where the sources of its Debian packages are.
NOTES
  gh release create "$tag" --title "VM disk $version" --notes-file "$tmp/notes" \
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

# Only the newest VM disk release stays, with its tag.
for old in $(gh release list --limit 200 --json tagName --jq '.[].tagName' | grep '^vmdisk-' || true); do
  [ "$old" = "$tag" ] && continue
  echo "==> Deleting $old"
  gh release delete "$old" --cleanup-tag -y
done
echo "Published VM disk $version at $(gh release view "$tag" --json url --jq .url)"
