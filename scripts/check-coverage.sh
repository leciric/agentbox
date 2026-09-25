#!/usr/bin/env bash
# Runs the Go test suite with coverage, writes a per-package summary to
# $GITHUB_STEP_SUMMARY (or stdout, outside CI), and fails if total coverage
# drops below the floor in .github/coverage-floor.txt. Bump that floor as
# coverage goes up; nothing here lowers it automatically.
#
#   scripts/check-coverage.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

profile=$(mktemp)
packages=$(mktemp)
trap 'rm -f "$profile" "$packages"' EXIT

go test -coverprofile="$profile" -covermode=atomic ./... 2>&1 | tee "$packages"

total=$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%","",$3); print $3}')
floor=$(tr -d '[:space:]' <.github/coverage-floor.txt)

{
  echo "## Coverage"
  echo
  echo '| Package | Coverage |'
  echo '| --- | --- |'
  grep -E '^(ok|---)' "$packages" | sed -E 's/^ok[[:space:]]+([^[:space:]]+).*coverage: ([0-9.]+)% of statements.*/| \1 | \2% |/; s/^--- (FAIL|no test files)[[:space:]]+([^[:space:]]+).*/| \2 | \1 |/' \
    | grep '^|'
  echo
  echo "**Total: ${total}%** (floor: ${floor}%)"
} | tee -a "${GITHUB_STEP_SUMMARY:-/dev/stdout}" >/dev/null

echo "total coverage: ${total}% (floor: ${floor}%)"

awk -v t="$total" -v f="$floor" 'BEGIN { exit (t + 0 < f + 0) ? 1 : 0 }' || {
  echo "coverage ${total}% is below the floor of ${floor}% (.github/coverage-floor.txt)" >&2
  exit 1
}
