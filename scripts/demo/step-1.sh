#!/usr/bin/env bash
# Reproduces the Step 1 evidence with throwaway state: projects, brief and auth.
# Extra arguments are real repositories to register (read-only: only git
# rev-parse / symbolic-ref / ls-files are run against them).
#
#   scripts/demo/step-1.sh [repo...]
set -euo pipefail
cd "$(dirname "$0")/../.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data"

go build -o "$work/agentbox" ./cmd/agentbox
ab() {
  printf '\n$ agentbox %s\n' "$*"
  "$work/agentbox" "$@" 2>&1 || echo "(exit $?)"
}

repo="$work/repos/hello-stack"
mkdir -p "$work/repos"
cp -r testdata/fixtures/hello-stack "$repo"
git -C "$repo" init -q -b main
git -C "$repo" add -A
git -C "$repo" -c commit.gpgsign=false commit -qm fixture
echo "SECRET=local-only" >"$repo/.env"

ab add "$repo"
ab add "$repo"
for extra in "$@"; do
  ab add "$extra"
done
ab projects
ab brief hello-stack --agent agent-01
ab auth status
printf '\n$ printf sk-ant-oat01-example | agentbox auth claude --token-stdin\n'
printf 'sk-ant-oat01-example' | "$work/agentbox" auth claude --token-stdin | sed "s#$work#\$TMP#"
printf '\n$ stat -c "%%a %%n" credentials/claude-oauth-token\n'
stat -c '%a %n' "$XDG_CONFIG_HOME/agentbox/credentials/claude-oauth-token" | sed "s#$work#\$TMP#"
ab auth status
ab remove hello-stack
ab projects
