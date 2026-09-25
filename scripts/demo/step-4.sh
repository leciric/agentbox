#!/usr/bin/env bash
# Reproduces the Step 4 evidence: snapshot and restore (machine, database, branch
# and uncommitted files), fork, pause/resume, top and resource limits.
# Throwaway state; no AI tool is used.
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
REPO="$work/repos/$P"
GIT='git -c user.name=agent -c user.email=agent@agentbox.invalid'
PSQL='docker compose exec -T postgres psql -U postgres'

section "Setup: a project and one agent"
fixture_repo hello-stack "$REPO"
ab add "$REPO"
ab create $P --ai none
ab snapshots $A1
expect_match "a new agent starts with an initial snapshot" "$(q snapshots $A1)" "initial"

section "1. Work in progress: a database table, a commit, an uncommitted edit, an untracked file"
ab exec $A1 -- "docker compose up -d --wait >/dev/null 2>&1 && $PSQL -qc 'create table doses(mg int); insert into doses values (500)'"
ab exec $A1 -- "echo 'reminders: on' >> message.txt && $GIT commit -qam 'agent-01: reminders' && echo 'draft: snooze button' >> message.txt && echo 'todo: tests' > NOTES.md"
ab exec $A1 -- "git log --oneline -2; git status --short; $PSQL -tAc 'select mg from doses'"

section "2. Snapshot before a risky change"
host "time $AB snapshot $A1 before-migration --consistent"
ab snapshots $A1

section "3. The agent breaks things"
ab exec $A1 -- "$PSQL -qc 'drop table doses' && rm server.mjs NOTES.md && $GIT commit -qam 'agent-01: bad migration' && echo broken > message.txt"
ab exec $A1 -- "ls; git log --oneline -1; $PSQL -tAc 'select mg from doses' 2>&1 | tail -1"

section "4. Restore: machine, database, branch and files come back"
host "time $AB restore $A1 before-migration"
ab exec $A1 -- "docker compose up -d --wait >/dev/null 2>&1; ls; git log --oneline -2; git status --short; tail -2 message.txt; $PSQL -tAc 'select mg from doses'"
host "git -C $REPO for-each-ref refs/agentbox"
expect "deleted server.mjs is back" "$(q exec $A1 -- 'test -e server.mjs && echo yes')" "yes"
expect "the uncommitted edit is back" "$(q exec $A1 -- 'tail -1 message.txt')" "draft: snooze button"
expect "the untracked file is back" "$(q exec $A1 -- 'cat NOTES.md')" "todo: tests"
expect "the branch is back at the snapshot" "$(git -C "$REPO" log -1 --format=%s agentbox/agent-01)" "agent-01: reminders"
expect "the database table is back" "$(q exec $A1 -- "$PSQL -tAc 'select mg from doses'")" "500"
expect_match "the broken state is kept on a pre-restore ref" "$(git -C "$REPO" for-each-ref refs/agentbox/pre-restore)" "refs/agentbox/pre-restore/agent-01/"

section "5. Fork: a second agent starts from the same snapshot"
host "time $AB fork $A1@before-migration"
ab exec $A2 -- "docker compose up -d --wait >/dev/null 2>&1; git log --oneline -1; git status --short; tail -1 message.txt; $PSQL -tAc 'select mg from doses'"
expect "the fork is on its own branch at the snapshot's commit" "$(git -C "$REPO" log -1 --format=%s agentbox/agent-02)" "agent-01: reminders"
expect "the fork has the uncommitted edit" "$(q exec $A2 -- 'tail -1 message.txt')" "draft: snooze button"
expect "the fork has the untracked file" "$(q exec $A2 -- 'cat NOTES.md')" "todo: tests"
expect "the fork has the database" "$(q exec $A2 -- "$PSQL -tAc 'select mg from doses'")" "500"
ab exec $A2 -- "echo 'approach B' >> message.txt && $GIT commit -qam 'agent-02: approach B'"
host "git -C $REPO log --oneline --graph --all"
expect "agent-01 is unaffected by the fork's commit" "$(q exec $A1 -- 'tail -1 message.txt')" "draft: snooze button"

section "6. Pause, resume and top"
q exec $A1 -- "tmux new-window -d -t main -n burn 'timeout 300 sh -c \"while :; do :; done\"'"
sleep 2
host "$AB top --interval 2s"
CPU_BUSY=$(q top --interval 2s | awk -v a="$A1" '$1 == a { gsub("%", "", $3); print $3 }')
ab pause $A1
ab list
host "$AB top --interval 2s"
CPU_PAUSED=$(q top --interval 2s | awk -v a="$A1" '$1 == a { gsub("%", "", $3); print $3 }')
expect_match "list shows the agent as paused" "$(q list | awk -v a="$A1" '$1 == a { print $3 }')" "paused"
ab resume $A1
q exec $A1 -- 'tmux kill-window -t main:burn'
expect "a busy agent shows CPU use (over 50%)" "$(awk -v c="$CPU_BUSY" 'BEGIN { print (c > 50) ? "yes" : "no (" c "%)" }')" "yes"
expect "a paused agent uses no CPU" "$CPU_PAUSED" "0"

section "7. Resource limits"
ab create $P --ai none --cpu 2 --memory 2GiB
host "incus config get ab-$P-agent-03 limits.cpu; incus config get ab-$P-agent-03 limits.memory"
ab exec $A3 -- 'nproc'
expect "limits.cpu is set" "$(incus config get ab-$P-agent-03 limits.cpu)" "2"
expect "limits.memory is set" "$(incus config get ab-$P-agent-03 limits.memory)" "2GiB"
expect "the agent sees 2 CPUs" "$(q exec $A3 -- nproc)" "2"

section "8. Deleting snapshots and agents cleans up"
ab snapshot rm $A1 before-migration
ab snapshots $A1
expect "the snapshot is gone" "$(q snapshots $A1 | grep -c before-migration)" "0"
for a in $A3 $A2 $A1; do ab destroy $a --force --delete-branch; done
ab remove $P
host "git -C $REPO for-each-ref refs/agentbox; incus list ab-$P- --format csv"
expect "no snapshot refs left" "$(git -C "$REPO" for-each-ref refs/agentbox)" ""
expect "no instances left" "$(incus list "ab-$P-" --format csv)" ""

summary
