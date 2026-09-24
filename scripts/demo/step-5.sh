#!/usr/bin/env bash
# Reproduces the Step 5 evidence: the daemon starts on demand, jobs outlive the
# CLI (Ctrl-C detaches), cancelling rolls back, events stream, the in-agent API
# only answers about its own agent, and a restart recovers an interrupted job.
# Throwaway state; no AI tool is used.
set -uo pipefail
cd "$(dirname "$0")/../.."

work=$(mktemp -d)
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data"
go build -o "$work/agentbox" ./cmd/agentbox || exit 1
AB="$work/agentbox"
source scripts/demo/lib.sh
P=hello-stack
REPO="$work/repos/$P"
RUN="$XDG_DATA_HOME/agentbox/run"
until_log() { for _ in $(seq 1 400); do grep -q "$2" "$1" 2>/dev/null && return; sleep 0.05; done; }

section "Setup"
fixture_repo hello-stack "$REPO"

section "1. The first command starts the daemon"
host "ls $RUN 2>&1"
ab add "$REPO"
host "stat -c '%a %n' $RUN/agentbox.sock; head -1 $XDG_DATA_HOME/agentbox/daemon.log"
expect_match "the daemon started on demand" "$(cat "$XDG_DATA_HOME/agentbox/daemon.log")" "listening on"
expect "the API socket is private" "$(stat -c %a "$RUN/agentbox.sock")" "600"

section "2. The event stream runs in the background from here on"
"$AB" events >"$work/events.log" 2>&1 &
EVENTS=$!
sleep 1

section "3. Ctrl-C detaches from a create; the job finishes in the daemon"
printf '\n$ agentbox create %s --ai none   (SIGINT as soon as it starts creating the instance)\n' "$P"
"$AB" create $P --ai none >"$work/create.log" 2>&1 &
pid=$!
until_log "$work/create.log" "Creating instance"
kill -INT $pid
wait $pid
rc=$?
sed "s#$work#\$TMP#g" "$work/create.log"
echo "(exit $rc)"
JOB=$(grep -oE 'agentbox jobs [0-9a-f]{8}' "$work/create.log" | head -1 | awk '{print $3}')
ab jobs
ab jobs "$JOB"
expect "Ctrl-C exits with 130 and detaches" "$rc" "130"
expect "the detached job succeeded anyway" "$(q jobs | awk -v j="$JOB" '$1 == j { print $4 }')" "succeeded"
expect "the agent exists and is running" "$(q list | awk -v a="$P/agent-01" '$1 == a { print $3 }')" "running"

section "4. Cancelling a running job rolls it back"
"$AB" create $P --ai none --name cancelled >"$work/cancel.log" 2>&1 &
pid=$!
until_log "$work/cancel.log" "Creating instance"
JOB2=$(q jobs | awk '$4 == "running" { print $1; exit }')
ab jobs cancel "$JOB2"
wait $pid
echo "(create exited with $?)"
sed "s#$work#\$TMP#g" "$work/cancel.log"
host "incus list ab-$P-cancelled --format csv; git -C $REPO branch --list agentbox/cancelled"
expect "the job is cancelled" "$(q jobs | awk -v j="$JOB2" '$1 == j { print $4 }')" "cancelled"
expect "no instance is left" "$(incus list "ab-$P-cancelled" --format csv)" ""
expect "no branch is left" "$(git -C "$REPO" branch --list agentbox/cancelled)" ""

section "5. The in-agent API only answers about its own agent"
ab exec $P/agent-01 -- 'ls -l /run/agentbox.sock && agentbox whoami'
ab exec $P/agent-01 -- 'curl -s --unix-socket /run/agentbox.sock http://agentbox/v1/projects'
ab exec $P/agent-01 -- 'curl -s -X POST --unix-socket /run/agentbox.sock http://agentbox/v1/agents -d "{\"project\": \"hello-stack\"}"'
expect_match "whoami inside the agent names it" "$(q exec $P/agent-01 -- agentbox whoami)" "^$P/agent-01"
expect_match "listing projects from inside is refused" "$(q exec $P/agent-01 -- 'curl -s --unix-socket /run/agentbox.sock http://agentbox/v1/projects')" "not available inside an agent"
expect_match "creating agents from inside is refused" "$(q exec $P/agent-01 -- 'curl -s -X POST --unix-socket /run/agentbox.sock http://agentbox/v1/agents -d "{}"')" "not available inside an agent"

section "6. The same API from the host, with curl"
host "curl -s --unix-socket $RUN/agentbox.sock http://agentbox/v1/agents | jq -c '.[] | {ref, state, ip, source}'"
host "curl -s --unix-socket $RUN/agentbox.sock 'http://agentbox/v1/usage?interval=500ms' | jq -c '{host: .host | {cpu, cores, memUsed}, agents: [.agents[] | {ref, memory}]}'"

section "7. Stopping and starting an agent shows up on the event stream"
ab stop $P/agent-01
ab start $P/agent-01
sleep 4
kill $EVENTS 2>/dev/null
wait $EVENTS 2>/dev/null
host "grep -v '  usage  ' $work/events.log | sed 's#$work#\$TMP#g'"
host "grep -m 2 '  usage  ' $work/events.log"
EV=$(cat "$work/events.log")
expect_match "events: the create job succeeded" "$EV" "job    create hello-stack succeeded"
expect_match "events: job log lines" "$EV" "log    \[[0-9a-f]{8}\] ==> Creating instance"
expect_match "events: the cancelled job" "$EV" "job    create hello-stack cancelled"
expect_match "events: the agent stopped" "$EV" "agent  hello-stack/agent-01 stopped"
expect "events: the agent is running again after it stopped" "$(awk '/agent-01 stopped/ { s = 1 } s && /agent-01 running/ { print "yes"; exit }' "$work/events.log")" "yes"
expect_match "events: resource samples" "$EV" "usage  host cpu"

section "8. The daemon is killed mid-job: after a restart the job is failed and destroy cleans up"
"$AB" create $P --ai none --name interrupted >"$work/killed.log" 2>&1 &
pid=$!
until_log "$work/killed.log" "Creating instance"
host "pkill -9 -f '$AB daemon'"
wait $pid
echo "(create exited with $?)"
sed "s#$work#\$TMP#g" "$work/killed.log"
sleep 1
ab jobs
ab list
JOB3=$(q jobs | awk '$2 == "create" && $4 == "failed" { print $1; exit }')
ab jobs "$JOB3"
expect_match "the interrupted job is failed after the restart" "$(q jobs "$JOB3")" "daemon stopped while this job was running"
expect "the half-made agent shows as incomplete" "$(q list | awk -v a="$P/interrupted" '$1 == a { print $3 }')" "incomplete"
ab destroy $P/interrupted --force --delete-branch
expect "destroy cleans it up" "$(incus list "ab-$P-interrupted" --format csv)$(git -C "$REPO" branch --list agentbox/interrupted)" ""

section "9. Running as a service, and stopping"
host "$AB daemon install --print | sed 's#$work#\$TMP#g'"
ab destroy $P/agent-01 --force --delete-branch
ab remove $P
ab daemon stop
expect "the daemon stopped and removed its socket" "$(test -e "$RUN/agentbox.sock" && echo still-there || echo gone)" "gone"

summary
