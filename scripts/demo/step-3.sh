#!/usr/bin/env bash
# Reproduces the Step 3 evidence: one agent sets the project up, its machine is
# saved as the project's base, and new agents start from it. Throwaway state;
# no AI tool is used.
set -uo pipefail
cd "$(dirname "$0")/../.."

work=$(mktemp -d)
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data"
go build -o "$work/agentbox" ./cmd/agentbox || exit 1
AB="$work/agentbox"
source scripts/demo/lib.sh
P=hello-stack
A1=$P/agent-01
A2=$P/agent-02
A3=$P/agent-03

section "Setup"
fixture_repo hello-stack "$work/repos/$P"
ab add "$work/repos/$P"

section "1. agent-01 sets the project up, starting from the plain base image"
ab create $P --ai none
T1_COMPOSE=$(seconds "$AB" exec $A1 -- 'docker compose up -d --wait')
T1_NODE=$(seconds "$AB" exec $A1 -- 'mise use -g node@22')
ab exec $A1 -- 'echo "Postgres runs with: docker compose up -d --wait" > ~/SETUP-NOTES.md'
# Things that belong to agent-01 alone and must not reach the base:
ab exec $A1 -- 'mkdir -p ~/.claude/projects/agent-01-session && echo "agent-01 history" > ~/.codex/history.jsonl && echo scratch > scratch.txt'
ab exec $A1 -- 'docker images --format "{{.Repository}}:{{.Tag}}" && mise ls node'
printf '\nagent-01: docker compose up took %ss (pulls postgres); mise use node@22 took %ss (downloads Node)\n' "$T1_COMPOSE" "$T1_NODE"

section "2. Save agent-01 as the project's base"
host "time $AB base save $A1"
ab base show $P
host "incus snapshot list ab-$P-base --format csv"
expect_match "base saved from agent-01" "$(q base show $P)" "saved from +$A1"
expect "agent-01 keeps running" "$(q list | awk '$1=="'$A1'"{print $3}')" "running"

section "3. agent-02 starts from the base"
ab create $P --ai none
ab exec $A2 -- 'docker images --format "{{.Repository}}:{{.Tag}}"; mise ls node; cat ~/SETUP-NOTES.md'
T2_COMPOSE=$(seconds "$AB" exec $A2 -- 'docker compose up -d --wait')
T2_NODE=$(seconds "$AB" exec $A2 -- 'mise use -g node@22')
ab exec $A2 -- 'docker compose ps --format "{{.Service}} {{.State}}"; ls; sed -n "s/^- Agent: //p" ~/AGENTBOX.md'
ab exec $A2 -- 'ls ~/.claude/projects 2>&1; cat ~/.codex/history.jsonl 2>&1'
expect "agent-02 was copied from the project base" "$(q list | awk '$1=="'$A2'"{print $1}')" "$A2"
expect_match "agent-02 already has the postgres image" "$(q exec $A2 -- 'docker images --format "{{.Repository}}:{{.Tag}}"')" "postgres:17-alpine"
expect_match "agent-02 already has Node 22" "$(q exec $A2 -- 'mise ls node')" "22\."
expect "agent-02 has agent-01's setup notes" "$(q exec $A2 -- 'cat ~/SETUP-NOTES.md')" "Postgres runs with: docker compose up -d --wait"
expect "agent-02 has a fresh worktree" "$(q exec $A2 -- 'test -e scratch.txt && echo leaked || echo fresh')" "fresh"
expect "agent-02's brief is its own" "$(q exec $A2 -- 'sed -n "s/^- Agent: //p" ~/AGENTBOX.md')" "agent-02"
expect "agent-01's AI sessions were not copied" "$(q exec $A2 -- 'test -e ~/.claude/projects/agent-01-session -o -e ~/.codex/history.jsonl && echo copied || echo removed')" "removed"
expect "agents have different machine ids" "$([[ "$(q exec $A1 -- cat /etc/machine-id)" != "$(q exec $A2 -- cat /etc/machine-id)" ]] && echo different)" "different"
expect "agent-02 runs Postgres" "$(q exec $A2 -- 'docker compose ps --format "{{.Service}} {{.State}}"')" "postgres running"

section "4. --clean ignores the base"
ab create $P --ai none --clean
ab exec $A3 -- 'docker images -q | wc -l; test -e ~/SETUP-NOTES.md && echo "has notes" || echo "no notes"'
expect "a --clean agent has no Docker images" "$(q exec $A3 -- 'docker images -q | wc -l')" "0"
expect "a --clean agent has no setup notes" "$(q exec $A3 -- 'test -e ~/SETUP-NOTES.md && echo yes || echo no')" "no"

section "5. Time to a running project"
printf '\n%-40s %10s %10s\n' "" "agent-01" "agent-02"
printf '%-40s %10s %10s\n' "docker compose up -d --wait (seconds)" "$T1_COMPOSE" "$T2_COMPOSE"
printf '%-40s %10s %10s\n' "mise use -g node@22 (seconds)" "$T1_NODE" "$T2_NODE"
expect "compose up is faster from the base" "$(awk -v a="$T1_COMPOSE" -v b="$T2_COMPOSE" 'BEGIN { print (b < a) ? "faster" : "not faster" }')" "faster"
expect "Node 22 is faster from the base" "$(awk -v a="$T1_NODE" -v b="$T2_NODE" 'BEGIN { print (b < a) ? "faster" : "not faster" }')" "faster"

section "6. Remove the base"
ab base rm $P
ab base show $P
expect_match "no base after rm" "$(q base show $P)" "has no saved base"
for a in $A3 $A2 $A1; do q destroy $a --force --delete-branch >/dev/null; done
q remove $P >/dev/null
expect "no instances left" "$(incus list "ab-$P-" --format csv)" ""

summary
