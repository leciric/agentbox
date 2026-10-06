#!/usr/bin/env bash
# Copies a published GitHub release to the Cloudflare R2 bucket users download
# from, since the repository is private and its releases answer nobody else:
#
#   releases/<tag>/<asset>      every asset, SHA256SUMS and latest-linux.yml included
#   releases/<tag>/index.html   the release's page: its notes and its assets
#   releases.json               the newest releases first, in the shape of GitHub's
#                               release list (internal/update reads it)
#   index.html                  sends a visitor on to the newest stable release's page
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
# releases.json keeps the newest $KEEP_RELEASES entries (30), of which at most
# $KEEP_NIGHTLIES (10) are nightlies. A nightly that drops off the list has its
# assets deleted too; a stable release's stay, since packages pin them.
set -euo pipefail

tag=${1:?usage: $0 <tag>}
repo=${GITHUB_REPOSITORY:-leciric/agentbox}
bucket=${R2_BUCKET:-agentbox-releases}
public=${DOWNLOADS_URL:-https://downloads.agentbox.linting.dev}
keep=${KEEP_RELEASES:-30}
keep_nightlies=${KEEP_NIGHTLIES:-10}
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

echo "==> Writing releases/$tag/index.html"
jq -r .body "$tmp/release.json" >"$tmp/notes.md"
gh api markdown -F text=@"$tmp/notes.md" -f mode=gfm -f context="$repo" >"$tmp/notes.html"
jq -r --rawfile notes "$tmp/notes.html" '
  def esc: gsub("&"; "&amp;") | gsub("<"; "&lt;") | gsub(">"; "&gt;") | gsub("\""; "&quot;");
  def size: if . >= 1048576 then "\(. / 1048576 | floor) MB" elif . >= 1024 then "\(. / 1024 | floor) kB" else "\(.) bytes" end;
  "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
  "<title>\(.name | esc)</title>\n" +
  "<style>body{font:16px/1.5 system-ui,sans-serif;max-width:46rem;margin:2rem auto;padding:0 1rem;color:#1f2328}a{color:#0969da}table{border-collapse:collapse}td{padding:.2rem 1rem .2rem 0}code,pre{font-size:85%}pre{overflow:auto;background:#f6f8fa;padding:.75rem}@media(prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}a{color:#4493f8}pre{background:#161b22}}</style>\n" +
  "</head>\n<body>\n<h1>\(.name | esc)</h1>\n" +
  "<p>\(if .prerelease then "A nightly build, published" else "Published" end) \(.published_at[0:10]).</p>\n" +
  "<h2>Downloads</h2>\n<table>\n" +
  ([.assets[] | "<tr><td><a href=\"\(.name | @uri)\">\(.name | esc)</a></td><td>\(.size | size)</td></tr>\n"] | join("")) +
  "</table>\n<h2>Release notes</h2>\n\($notes)\n</body>\n</html>"
' "$tmp/entry.json" >"$tmp/index.html"
aws s3 cp "$tmp/index.html" "s3://$bucket/releases/$tag/index.html" \
  --content-type 'text/html; charset=utf-8' --cache-control 'max-age=300' --only-show-errors

echo "==> Rewriting releases.json"
if ! aws s3 cp "s3://$bucket/releases.json" "$tmp/old.json" --only-show-errors 2>/dev/null; then
  echo '[]' >"$tmp/old.json"
fi
jq --slurpfile entry "$tmp/entry.json" --arg nightly "$nightly" --argjson keep "$keep" --argjson nightlies "$keep_nightlies" '
  [.[] | select(.tag_name != $entry[0].tag_name)] + $entry |
  sort_by(.published_at) | reverse |
  ([.[] | select(.tag_name | test($nightly)) | .tag_name][$nightlies:]) as $old |
  [.[] | select(.tag_name as $t | $old | index($t) | not)] | .[:$keep]
' "$tmp/old.json" >"$tmp/new.json"
aws s3 cp "$tmp/new.json" "s3://$bucket/releases.json" \
  --content-type application/json --cache-control 'max-age=60' --only-show-errors

# A nightly no longer listed goes; a stable release's assets stay.
jq -r --slurpfile new "$tmp/new.json" --arg nightly "$nightly" '
  [$new[0][].tag_name] as $kept | .[].tag_name | select(test($nightly)) | select(. as $t | $kept | index($t) | not)
' "$tmp/old.json" | while read -r old; do
  echo "==> Deleting the nightly $old"
  aws s3 rm "s3://$bucket/releases/$old/" --recursive --only-show-errors
done

stable=$(jq -r '[.[] | select(.prerelease | not)][0].tag_name // empty' "$tmp/new.json")
if [ -n "$stable" ]; then
  echo "==> Pointing index.html at $stable"
  page="releases/$stable/index.html"
  printf '<!doctype html>\n<meta charset="utf-8">\n<title>AgentBox downloads</title>\n<meta http-equiv="refresh" content="0; url=%s">\n<p><a href="%s">AgentBox %s</a></p>\n' \
    "$page" "$page" "${stable#v}" >"$tmp/root.html"
  aws s3 cp "$tmp/root.html" "s3://$bucket/index.html" \
    --content-type 'text/html; charset=utf-8' --cache-control 'max-age=300' --only-show-errors
fi

echo "Published $tag at $public/releases/$tag/index.html"
