#!/usr/bin/env bash
# Posts the last parity.sh run to a pull request: for every scenario its score
# and its Electron | native | diff image (halved, as JPEG), one comment per
# theme. The comments this script posted before are deleted first, so the
# pull request shows only the latest run.
#
#   native/scripts/parity-comment.sh <pr-number>
set -euo pipefail

pr=$1
here=$(cd "$(dirname "$0")" && pwd)
out=$(dirname "$here")/parity-out
marker='<!-- parity-run -->'
tmp=${TMPDIR:-/tmp}/parity-comment.$$
mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT

repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
for id in $(gh api "repos/$repo/issues/$pr/comments" --paginate -q ".[] | select(.body | contains(\"$marker\")) | .id"); do
  gh api -X DELETE "repos/$repo/issues/comments/$id" >/dev/null
done

commit=$(git -C "$here" rev-parse --short HEAD)
for theme in $(awk '{split($1, p, "-"); for (i in p) if (p[i] == "dark" || p[i] == "light") print p[i]}' "$out/scores.txt" | sort -u); do
  pass=$(grep -c -- "-$theme-.* ok$" "$out/scores.txt" || true)
  total=$(grep -c -- "-$theme-" "$out/scores.txt" || true)
  body=$tmp/$theme.md
  {
    echo "$marker"
    echo "### Parity run on $commit, $theme: $pass of $total at ≥ 0.99"
    echo
    echo "Each image is Electron | native | diff (red: pixels that differ beyond the 1px, ±24/255 tolerance)."
    echo
  } >"$body"
  args=()
  while read -r name score verdict; do
    jpg=$tmp/$name.jpg
    convert "$out/$name.png" -resize 50% -quality 82 "$jpg"
    mark=$([ "$verdict" = ok ] && echo "✅" || echo "❌")
    printf '**%s** %s %s\n\n![%s](%s)\n\n' "$name" "$score" "$mark" "$name $score" "$jpg" >>"$body"
    args+=(--attach "$jpg#$name $score")
  done < <(grep -- "-$theme-" "$out/scores.txt")
  gh pr comment "$pr" --body-file "$body" "${args[@]}" >/dev/null
  echo "posted $theme: $pass/$total"
done
