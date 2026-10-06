#!/usr/bin/env bash
# Copies a published GitHub release to the Cloudflare R2 bucket users download
# from, since the repository is private and its releases answer nobody else:
#
#   releases/<tag>/<asset>      every asset, SHA256SUMS and latest-linux.yml included
#   releases/<tag>/index.html   the release's page (scripts/r2-page.py): its downloads by
#                               platform beside the other channel's, and its notes
#   releases.json               the newest stable release and the newest nightly, newest
#                               first, in the shape of GitHub's release list
#                               (internal/update reads it)
#   index.html                  the same page, led by the stable release
#
# The release workflow runs it after publishing a release, and the nightly
# workflow after publishing a nightly. Run it by hand to copy a release that
# missed it (a rerun is harmless: every object is written again):
#
#   scripts/r2-publish.sh v0.11.0
#
# It needs gh with read access to the repository, the AWS CLI, jq, and an R2
# API token's S3 credentials: R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY and
# CLOUDFLARE_ACCOUNT_ID.
#
# The bucket keeps only the newest release of each channel: publishing a
# stable release deletes the previous stable's releases/<tag>/, and publishing
# a nightly the previous nightly's. Copying an older release than the one
# there (by published_at) leaves the newer one, and deletes what it uploaded.
set -euo pipefail

tag=${1:?usage: $0 <tag>}
repo=${GITHUB_REPOSITORY:-leciric/agentbox}
bucket=${R2_BUCKET:-agentbox-releases}
public=${DOWNLOADS_URL:-https://downloads.agentbox.linting.dev}
nightly='^v[0-9]+\.[0-9]+\.[0-9]+-nightly\.[0-9]{8}\.[0-9]+$'

: "${R2_ACCESS_KEY_ID:?R2_ACCESS_KEY_ID is not set}"
: "${R2_SECRET_ACCESS_KEY:?R2_SECRET_ACCESS_KEY is not set}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID is not set}"
export AWS_ACCESS_KEY_ID=$R2_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY=$R2_SECRET_ACCESS_KEY
export AWS_DEFAULT_REGION=auto AWS_ENDPOINT_URL=https://$CLOUDFLARE_ACCOUNT_ID.r2.cloudflarestorage.com
# R2 rejects the checksums newer AWS CLIs send by default.
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_RESPONSE_CHECKSUM_VALIDATION=when_required

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "==> Downloading $tag from $repo"
gh release view "$tag" -R "$repo" --json tagName,name,isDraft,isPrerelease,publishedAt,body >"$tmp/release.json"
if [ "$(jq -r .isDraft "$tmp/release.json")" = true ]; then
  echo "$tag is still a draft: publish it on GitHub first" >&2
  exit 1
fi
gh release download "$tag" -R "$repo" -D "$tmp/assets"

echo "==> Uploading to r2://$bucket/releases/$tag/"
aws s3 cp "$tmp/assets/" "s3://$bucket/releases/$tag/" --recursive --only-show-errors

# The entry for releases.json: the fields internal/update and the app read,
# under GitHub's names, with the downloads pointing at the bucket.
(cd "$tmp/assets" && for f in *; do printf '%s\t%s\n' "$f" "$(stat -c %s -- "$f")"; done) |
  jq -R -s --slurpfile rel "$tmp/release.json" --arg base "$public/releases/$tag/" '
    ($rel[0]) as $r |
    {
      tag_name: $r.tagName,
      name: $r.name,
      draft: false,
      prerelease: $r.isPrerelease,
      published_at: $r.publishedAt,
      assets: [split("\n")[] | select(. != "") | split("\t") |
        {name: .[0], size: (.[1] | tonumber), browser_download_url: ($base + (.[0] | @uri))}]
    }' >"$tmp/entry.json"

echo "==> Rewriting releases.json"
if ! aws s3 cp "s3://$bucket/releases.json" "$tmp/old.json" --only-show-errors 2>/dev/null; then
  echo '[]' >"$tmp/old.json"
fi
jq --slurpfile entry "$tmp/entry.json" --arg nightly "$nightly" '
  [.[] | select(.tag_name != $entry[0].tag_name)] + $entry |
  group_by(.tag_name | test($nightly)) | map(max_by(.published_at)) |
  sort_by(.published_at) | reverse
' "$tmp/old.json" >"$tmp/new.json"

# The pages: one per release kept, and the bucket's index.html, each showing
# every release kept (scripts/r2-page.py), with its notes as GitHub renders
# them. A release whose notes can't be had is shown without them.
mkdir -p "$tmp/notes" "$tmp/pages"
for t in $(jq -r '.[].tag_name' "$tmp/new.json"); do
  if gh release view "$t" -R "$repo" --json body --jq .body >"$tmp/notes/$t.md" 2>/dev/null; then
    gh api markdown -F text=@"$tmp/notes/$t.md" -f mode=gfm -f context="$repo" >"$tmp/notes/$t.html" || rm -f "$tmp/notes/$t.html"
  fi
  python3 "$(dirname "$0")/r2-page.py" "$tmp/new.json" "$tmp/notes" "$t" >"$tmp/pages/$t.html"
done
python3 "$(dirname "$0")/r2-page.py" "$tmp/new.json" "$tmp/notes" >"$tmp/pages/index.html"

for t in $(jq -r '.[].tag_name' "$tmp/new.json"); do
  echo "==> Writing releases/$t/index.html"
  aws s3 cp "$tmp/pages/$t.html" "s3://$bucket/releases/$t/index.html" \
    --content-type 'text/html; charset=utf-8' --cache-control 'max-age=300' --only-show-errors
done
aws s3 cp "$tmp/new.json" "s3://$bucket/releases.json" \
  --content-type application/json --cache-control 'max-age=60' --only-show-errors
echo "==> Writing index.html"
aws s3 cp "$tmp/pages/index.html" "s3://$bucket/index.html" \
  --content-type 'text/html; charset=utf-8' --cache-control 'max-age=300' --only-show-errors

# What the list no longer holds goes from the bucket too.
jq -r -s '
  ([.[1][].tag_name]) as $kept | ([.[0][].tag_name] + [.[2].tag_name]) | unique[] | select(. as $t | $kept | index($t) | not)
' "$tmp/old.json" "$tmp/new.json" "$tmp/entry.json" | while read -r old; do
  echo "==> Deleting $old"
  aws s3 rm "s3://$bucket/releases/$old/" --recursive --only-show-errors
done

echo "Published $tag at $public/releases/$tag/index.html"
