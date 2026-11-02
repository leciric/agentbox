#!/usr/bin/env bash
# Copies the base image release scripts/github-images.sh just published to
# the Cloudflare R2 bucket behind https://downloads.agentbox.linting.dev, where
# Setup looks first (internal/image/published.go's DefaultImagesURLs), GitHub's
# release being its fallback:
#
#   images/image-<version>/agentbox-base-<arch>.tar.zst.000, …  the zstd image, in parts
#   images/image-<version>/image-<arch>.json, packages-<arch>.txt, SOURCES-<arch>.md
#   images/image-<version>/SHA256SUMS                            the release's, every architecture
#
# Only the zstd image goes there: the bucket's space is short, and an AgentBox
# that reads the gzipped one (one from before zstd, or a machine without the
# zstd command) is sent to GitHub for it by the 404. Every other images/
# object is deleted, so only the newest image version stays, as on GitHub.
# SHA256SUMS goes last, and is removed first: a machine downloading meanwhile
# finds nothing on the CDN, and downloads from GitHub.
#
#   scripts/r2-images.sh out/amd64 [out/arm64]
#
# with the directories given to scripts/github-images.sh. It needs gh, the AWS
# CLI, python3 and an R2 API token's S3 credentials: R2_ACCESS_KEY_ID,
# R2_SECRET_ACCESS_KEY and CLOUDFLARE_ACCOUNT_ID.
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: $0 <dir>..." >&2; exit 2; }
bucket=${R2_BUCKET:-agentbox-releases}

: "${R2_ACCESS_KEY_ID:?R2_ACCESS_KEY_ID is not set}"
: "${R2_SECRET_ACCESS_KEY:?R2_SECRET_ACCESS_KEY is not set}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID is not set}"
export AWS_ACCESS_KEY_ID=$R2_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY=$R2_SECRET_ACCESS_KEY
export AWS_DEFAULT_REGION=auto AWS_ENDPOINT_URL=https://$CLOUDFLARE_ACCOUNT_ID.r2.cloudflarestorage.com
# R2 rejects the checksums newer AWS CLIs send by default.
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_RESPONSE_CHECKSUM_VALIDATION=when_required

field() { python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }

version=$(field "$(ls "$1"/image-*.json)" version)
tag=image-$version
dest=s3://$bucket/images/$tag
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The release's SHA256SUMS, with the lines of every architecture.
gh release download "$tag" -p SHA256SUMS -O "$tmp/SHA256SUMS"

aws s3 rm "$dest/SHA256SUMS" --only-show-errors || true
for dir in "$@"; do
  echo "==> Uploading the zstd image in $dir to $dest/"
  # The names stay, so a rerun of the same version writes over the parts:
  # the CDN keeps them no more than an hour, and a stale part fails its sum
  # and comes from GitHub instead.
  for f in "$dir"/*; do
    case $(basename "$f") in
      SHA256SUMS | *.tar.gz.*) ;;
      *.tar.zst.*) aws s3 cp "$f" "$dest/" --cache-control 'max-age=3600' --only-show-errors ;;
      *) aws s3 cp "$f" "$dest/" --cache-control 'max-age=60' --only-show-errors ;;
    esac
  done
done
aws s3 cp "$tmp/SHA256SUMS" "$dest/SHA256SUMS" --content-type text/plain --cache-control 'max-age=60' --only-show-errors

# Every other image version, and what images/ held before (#278).
aws s3 ls "s3://$bucket/images/" | awk '{print $NF}' | while read -r entry; do
  case $entry in
    "$tag/") ;;
    */) echo "==> Deleting images/$entry"; aws s3 rm "s3://$bucket/images/$entry" --recursive --only-show-errors ;;
    *) echo "==> Deleting images/$entry"; aws s3 rm "s3://$bucket/images/$entry" --only-show-errors ;;
  esac
done
echo "Copied base image $version to https://downloads.agentbox.linting.dev/images/$tag/"
