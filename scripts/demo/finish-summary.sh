#!/usr/bin/env bash
# Reproduces the finish-summary evidence with throwaway state: what an agent's
# brief tells it about finishing, and the notice its project's lead is sent when
# it does — with a summary, with an enormous one, and with none at all.
#
#   scripts/demo/finish-summary.sh
set -euo pipefail
cd "$(dirname "$0")/../.."

work=$(mktemp -d)
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data"
finish() {
  "$work/agentbox" daemon stop >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap finish EXIT

section() { printf '\n\n######## %s\n' "$*"; }

go build -o "$work/agentbox" ./cmd/agentbox

section "Half one: what the agent is told to write"

repo="$work/repos/hello-stack"
mkdir -p "$work/repos"
cp -r testdata/fixtures/hello-stack "$repo"
git -C "$repo" init -q -b main
git -C "$repo" add -A
git -C "$repo" -c commit.gpgsign=false commit -qm fixture
"$work/agentbox" add "$repo" >/dev/null

printf '\n$ agentbox brief hello-stack --agent agent-01   # the section it grew\n\n'
"$work/agentbox" brief hello-stack --agent agent-01 | sed -n '/^## When you finish/,/^## Git/p' | sed '$d'

section "Half two: what the lead is sent"
# A real daemon, a real chat manager and the real noticeAgentFinished, with the
# finishing agent's conversation constructed rather than produced by Claude Code
# inside a machine: an agent machine has no Incus of its own (D43), so there is
# no second machine here to run an agent's AI tool in.
printf '\n$ go test ./internal/daemon -run TestFinishNotice -v\n'
go test ./internal/daemon -run TestFinishNotice -v -count=1

section "The cap itself"
printf '\n$ go test ./internal/daemon -run "TestSummaryOf|TestShorten|TestQuote" -v\n'
go test ./internal/daemon -run 'TestSummaryOf|TestShorten|TestQuote' -v -count=1

section "And the agent's own side of it"
printf '\n$ go test ./internal/chat -run TestLastMessage -v\n'
go test ./internal/chat -run TestLastMessage -v -count=1
