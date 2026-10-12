#!/usr/bin/env bash
# Uploads the published base image to the Cloudflare R2 bucket app releases are
# downloaded from, and deletes every other image version there:
#
#   images/<version>/<arch>/agentbox-base.tar.gz  the image (internal/image/published.go)
#   images/<version>/<arch>/image.json            what it is
#   images/<version>/<arch>/packages.txt          its Debian packages, with versions
#   images/<version>/<arch>/SOURCES.md            where their sources are, the tools' licenses
#   images/<version>/<arch>/SHA256SUMS            the sums of the four above
#   images/index.json                             every image kept
#
# The Published image workflow runs it, with a directory per architecture as
# `agentbox image build-for-publishing` wrote it:
#
#   scripts/r2-images.sh out/amd64 [out/arm64]
#
# Every image must be of the same version. The bucket keeps that version only,
# the same rule as app releases: an AgentBox built for an older one finds
# nothing to download, and builds its image itself. An architecture not given
# keeps whatever image of this version it already has.
#
# It needs the AWS CLI, python3 and an R2 API token's S3 credentials:
# R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY and CLOUDFLARE_ACCOUNT_ID.
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: $0 <dir>..." >&2; exit 2; }
bucket=${R2_BUCKET:-agentbox-releases}
public=${DOWNLOADS_URL:-https://downloads.agentbox.linting.dev}

: "${R2_ACCESS_KEY_ID:?R2_ACCESS_KEY_ID is not set}"
: "${R2_SECRET_ACCESS_KEY:?R2_SECRET_ACCESS_KEY is not set}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID is not set}"
export AWS_ACCESS_KEY_ID=$R2_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY=$R2_SECRET_ACCESS_KEY
export AWS_DEFAULT_REGION=auto AWS_ENDPOINT_URL=${R2_ENDPOINT_URL:-https://$CLOUDFLARE_ACCOUNT_ID.r2.cloudflarestorage.com}
# R2 rejects the checksums newer AWS CLIs send by default.
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_RESPONSE_CHECKSUM_VALIDATION=when_required

field() { python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }

version=
for dir in "$@"; do
  (cd "$dir" && sha256sum --quiet -c SHA256SUMS)
  v=$(field "$dir/image.json" version)
  [ -z "$version" ] || [ "$v" = "$version" ] || { echo "$dir is version $v, not $version" >&2; exit 1; }
  version=$v
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for dir in "$@"; do
  arch=$(field "$dir/image.json" arch)
  file=$(field "$dir/image.json" file)
  dest="s3://$bucket/images/$version/$arch"
  # The image first and SHA256SUMS last: a machine downloading meanwhile finds
  # either the previous sums, which the new image fails, or the new ones, and
  # builds its own image for the one in between.
  echo "==> Uploading $version for $arch to $dest/"
  aws s3 cp "$dir/$file" "$dest/$file" --only-show-errors
  for f in image.json packages.txt SOURCES.md SHA256SUMS; do
    aws s3 cp "$dir/$f" "$dest/$f" --cache-control 'max-age=60' --only-show-errors \
      --content-type "$(case $f in *.json) echo application/json ;; *) echo 'text/plain; charset=utf-8' ;; esac)"
  done
done

# The index lists every architecture of this version the bucket has,
# including one this run didn't build.
mkdir -p "$tmp/kept"
for arch in $(aws s3 ls "s3://$bucket/images/$version/" | awk '$1 == "PRE" { sub("/$", "", $2); print $2 }'); do
  aws s3 cp "s3://$bucket/images/$version/$arch/image.json" "$tmp/kept/$arch.json" --only-show-errors || true
done
python3 - "$tmp/kept" "$public/images/$version" >"$tmp/index.json" <<'EOF'
import json, os, sys
kept, base = sys.argv[1], sys.argv[2]
images = []
for name in sorted(os.listdir(kept)):
    p = json.load(open(os.path.join(kept, name)))
    url = f"{base}/{p['arch']}/"
    images.append({**p, "url": url + p["file"], "sha256sums": url + "SHA256SUMS", "sources": url + "SOURCES.md"})
json.dump({"images": images}, sys.stdout, indent=2)
print()
EOF
echo "==> Writing images/index.json"
aws s3 cp "$tmp/index.json" "s3://$bucket/images/index.json" \
  --content-type application/json --cache-control 'max-age=60' --only-show-errors

# Only the newest image version stays.
for old in $(aws s3 ls "s3://$bucket/images/" | awk '$1 == "PRE" { sub("/$", "", $2); print $2 }'); do
  [ "$old" = "$version" ] && continue
  echo "==> Deleting images/$old/"
  aws s3 rm "s3://$bucket/images/$old/" --recursive --only-show-errors
done

echo "Published base image $version at $public/images/index.json"
