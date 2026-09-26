#!/usr/bin/env bash
# Fails a pull request that touches internal/, cmd/ or desktop/src/ without
# adding a line to CHANGELOG.md's Unreleased section. Skipped by the
# "no-changelog" label, for tests-, CI- and docs-only changes.
#
#   BASE=<base sha> HEAD=<head sha> LABELS=<comma-separated> scripts/check-changelog.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

base=${BASE:?BASE sha is required}
head=${HEAD:?HEAD sha is required}
labels=${LABELS:-}

if [[ ",$labels," == *",no-changelog,"* ]]; then
  echo "no-changelog label present, skipping"
  exit 0
fi

if ! git diff --name-only "$base" "$head" -- internal cmd desktop/src | grep -q .; then
  echo "no changes under internal/, cmd/ or desktop/src/, skipping"
  exit 0
fi

extract_unreleased() {
  awk '/^## Unreleased/{flag=1; next} /^## /{flag=0} flag' "$1"
}

base_changelog=$(mktemp)
trap 'rm -f "$base_changelog"' EXIT
git show "$base:CHANGELOG.md" >"$base_changelog" 2>/dev/null || : >"$base_changelog"

if diff -q <(extract_unreleased "$base_changelog") <(extract_unreleased CHANGELOG.md) >/dev/null; then
  echo "This PR touches internal/, cmd/ or desktop/src/ but CHANGELOG.md's Unreleased section is unchanged." >&2
  echo "Add a user-facing line under Added/Changed/Fixed, or label the PR 'no-changelog' for a tests-, CI- or docs-only change." >&2
  exit 1
fi

echo "CHANGELOG.md's Unreleased section changed."
