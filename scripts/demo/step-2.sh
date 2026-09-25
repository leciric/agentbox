#!/usr/bin/env bash
# Reproduces the Step 2 evidence: real agents on Incus, driven by the agentbox CLI.
# Needs scripts/host-setup.sh and `agentbox image build`. State is throwaway.
# Claude's login is the host session's current short-lived access token, stored
# in that throwaway state; the host's refresh token never leaves the host.
set -uo pipefail
cd "$(dirname "$0")/../.."

work=$(mktemp -d)
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data"
go build -o "$work/agentbox" ./cmd/agentbox || exit 1
AB="$work/agentbox"
A1=hello-stack/agent-01
A2=hello-stack/agent-02
results=()

section() { printf '\n\n######## %s\n' "$*"; }
ab() {
  printf '\n$ agentbox %s\n' "$*"
  "$AB" "$@" 2>&1 | sed "s#$work#\$TMP#g"
  local rc=${PIPESTATUS[0]}
  [[ $rc -eq 0 ]] || echo "(exit $rc)"
}
q() { "$AB" "$@" 2>/dev/null; }
host() { printf '\n$ %s\n' "$*" | sed "s#$work#\$TMP#g"; eval "$*" 2>&1 | sed "s#$work#\$TMP#g"; }
expect() { if [[ "$2" == "$3" ]]; then results+=("PASS  $1"); else results+=("FAIL  $1 (got: '$2', want: '$3')"); fi; }
expect_match() { if [[ "$2" =~ $3 ]]; then results+=("PASS  $1"); else results+=("FAIL  $1 (got: '$2')"); fi; }
ip_of() { incus query "/1.0/instances/$1/state" | jq -r '.network.eth0.addresses[] | select(.family=="inet") | .address'; }
wait_http() { for _ in $(seq 1 60); do curl -sf --max-time 2 "$1" >/dev/null && return; sleep 1; done; }

section "Setup: a project with a gitignored .env"
repo="$work/repos/hello-stack"
mkdir -p "$work/repos"
cp -r testdata/fixtures/hello-stack "$repo"
git -C "$repo" init -q -b main
git -C "$repo" add -A
git -C "$repo" -c commit.gpgsign=false commit -qm "hello-stack fixture"
echo "APP_SECRET=only-on-the-host" >"$repo/.env"
ab add "$repo"

section "AgentBox's own Claude Code login (the secret is never printed)"
# Stand-in for `agentbox auth claude`, which runs the interactive `claude setup-token`.
printf '\n$ jq -r .claudeAiOauth.accessToken ~/.claude/.credentials.json | agentbox auth claude --token-stdin\n'
jq -r .claudeAiOauth.accessToken ~/.claude/.credentials.json | "$AB" auth claude --token-stdin 2>&1 | sed "s#$work#\$TMP#g"
ab auth status

section "1. Two agents on the same project"
host "time $AB create hello-stack --ai claude"
host "time $AB create hello-stack --ai claude --autonomous"
ab create hello-stack --ai codex
ab list
WT1=$(q path $A1)
WT2=$(q path $A2)
IP1=$(ip_of ab-hello-stack-agent-01)
IP2=$(ip_of ab-hello-stack-agent-02)
expect "both agents running" "$(q list | grep -c running)" "2"
expect_match "a Codex agent needs an AgentBox Codex login first" "$("$AB" create hello-stack --ai codex 2>&1)" "agentbox auth codex"

section "2. What each agent received: brief, env files, login, tools"
ab exec $A1 -- 'sed -n 1,9p ~/.claude/CLAUDE.md'
ab exec $A1 -- 'cat .env && git status --short --ignored .env'
ab exec $A1 -- 'echo "CLAUDE_CODE_OAUTH_TOKEN set: ${CLAUDE_CODE_OAUTH_TOKEN:+yes}"; ls -A ~/.claude'
ab exec $A2 -- 'claude --version; codex --version; docker --version; node --version; pnpm --version'
expect "gitignored .env copied into the worktree" "$(q exec $A1 -- cat .env)" "APP_SECRET=only-on-the-host"
expect_match "brief installed for Claude Code" "$(q exec $A1 -- cat '~/.claude/CLAUDE.md')" 'Branch: `agentbox/agent-01`'
expect_match "brief installed for Codex" "$(q exec $A2 -- cat '~/.codex/AGENTS.md')" 'Branch: `agentbox/agent-02`'

section "3. Claude Code runs logged in, in each agent's tmux session"
sleep 15
ab exec $A1 -- 'tmux list-windows -t main -F "#{window_index}:#{window_name}"'
ab exec $A1 -- 'tmux capture-pane -p -t main:claude | grep -v "^\s*$" | head -20'
ab exec $A2 -- 'tmux capture-pane -p -t main:claude | grep -v "^\s*$" | head -20'
pane1=$(q exec $A1 -- tmux capture-pane -p -t main:claude)
pane2=$(q exec $A2 -- tmux capture-pane -p -t main:claude)
expect_match "Claude Code is up in agent-01" "$pane1" "Claude Code"
if grep -qE "Select login method|Please run /login|Invalid API key|OAuth token has expired" <<<"$pane1$pane2"; then
  results+=("FAIL  Claude Code asks for a login")
else
  results+=("PASS  Claude Code does not ask for a login")
fi
expect_match "agent-02 runs Claude Code with permissions bypassed (--autonomous)" "$pane2" "[Bb]ypass"

section "4. agentbox shell attaches a real terminal to that session"
printf '\n$ agentbox shell %s   (in a pseudo-terminal; Ctrl-b d sent after 5s)\n' "$A1"
TERM=xterm-256color timeout 15 script -qfc "$AB shell $A1" /dev/null < <(sleep 5; printf '\002d') >"$work/shell.raw" 2>&1
sed -E 's/\x1b\[[0-9;?]*[ -\/]*[@-~]//g; s/\x1b[()][A-Z0-9]//g; s/\x1b[=>]//g; s/\r/\n/g' "$work/shell.raw" | grep -E "detached|Claude Code" | sort -u | head -5
expect_match "shell attached to tmux and detached cleanly" "$(cat "$work/shell.raw")" "detached"

section "5. The same port in both agents, reachable from the host"
for a in $A1 $A2; do q exec $a -- "tmux new-window -d -t main -n dev 'pnpm dev'"; done
wait_http "http://$IP1:3000"
wait_http "http://$IP2:3000"
host "curl -s http://$IP1:3000"
host "curl -s http://$IP2:3000"
expect_match "agent-01 serves :3000" "$(curl -s "http://$IP1:3000")" '"host":"ab-hello-stack-agent-01"'
expect_match "agent-02 serves :3000 at the same time" "$(curl -s "http://$IP2:3000")" '"host":"ab-hello-stack-agent-02"'

section "6. Agent-led setup, in parallel: agent-01 brings up the database from the README; agent-02 commits"
ab exec $A1 -- "timeout 600 claude -p --model haiku --dangerously-skip-permissions 'Get this project running the way its README says (the dev server may already be running). Then create a table visits (n int) in the database with one row where n = 1. Finally append a line \"db ok\" to message.txt and commit only message.txt with the message \"agent-01: database is up\". Reply with a short summary of what you did.'" >"$work/a1.log" 2>&1 &
ab exec $A2 -- "timeout 600 claude -p --model haiku --dangerously-skip-permissions 'Append a line \"hello from agent-02\" to message.txt and commit only message.txt with the message \"agent-02: hello\". Reply with one short sentence.'" >"$work/a2.log" 2>&1 &
wait
cat "$work/a1.log" "$work/a2.log"
ab exec $A1 -- 'docker compose ps --format "{{.Service}} {{.State}}" && docker compose exec -T postgres psql -U postgres -tAc "select n from visits"'
expect_match "agent-01 started Postgres itself" "$(q exec $A1 -- 'docker compose ps --format "{{.Service}} {{.State}}"')" "postgres running"
expect "agent-01 created the table" "$(q exec $A1 -- 'docker compose exec -T postgres psql -U postgres -tAc "select n from visits"')" "1"
expect_match "agent-01 committed on agentbox/agent-01" "$(git -C "$repo" log -1 --format=%s agentbox/agent-01)" "database is up"
expect_match "agent-02 committed on agentbox/agent-02" "$(git -C "$repo" log -1 --format=%s agentbox/agent-02)" "agent-02: hello"

section "7. Review from the host"
ab diff $A1 --stat
ab diff $A2
host "git -C $repo log --oneline --graph --all"
host "stat -c '%U %n' $WT1/message.txt $WT2/message.txt"
expect "files written by agents are owned by $(id -un)" "$(stat -c %U "$WT1/message.txt")" "$(id -un)"
expect "main is untouched" "$(git -C "$repo" rev-list --count main)" "1"

section "8. Stop and start: the tmux session and AI tool come back"
printf '\nagent-02 ip before stop: %s\n' "$IP2"
ab stop $A2
ab list
ab start $A2
printf '\nagent-02 ip after start: %s\n' "$(ip_of ab-hello-stack-agent-02)"
ab exec $A2 -- 'tmux list-windows -t main -F "#{window_index}:#{window_name}"'
expect "tmux windows restored after start" "$(q exec $A2 -- 'tmux list-windows -t main -F "#{window_name}"' | tr '\n' ' ')" "shell claude "

section "9. Ctrl-C during create rolls everything back"
printf '\n$ agentbox create hello-stack --ai none --name interrupted   (SIGINT once it starts creating the instance)\n'
"$AB" create hello-stack --ai none --name interrupted >"$work/interrupted.log" 2>&1 &
pid=$!
for _ in $(seq 1 200); do grep -q "Creating instance" "$work/interrupted.log" && break; sleep 0.05; done
sleep 0.3
kill -INT $pid
wait $pid
echo "(exit $?)"
sed "s#$work#\$TMP#g" "$work/interrupted.log"
host "incus list ab-hello-stack-interrupted --format csv; git -C $repo branch --list agentbox/interrupted; ls $XDG_DATA_HOME/agentbox/worktrees/hello-stack"
expect "no instance left" "$(incus list ab-hello-stack-interrupted --format csv)" ""
expect "no branch left" "$(git -C "$repo" branch --list agentbox/interrupted)" ""
expect "no worktree left" "$(test -e "$XDG_DATA_HOME/agentbox/worktrees/hello-stack/interrupted" && echo exists)" ""
expect "not in the agent list" "$(q list | grep -c interrupted)" "0"

section "10. Destroy protects work"
q exec $A2 -- 'echo wip > wip.txt'
ab destroy $A2
expect_match "destroy refuses uncommitted work" "$("$AB" destroy $A2 2>&1)" "uncommitted changes"
ab destroy $A2 --force
host "git -C $repo branch --list 'agentbox/*'"
expect_match "branch kept after destroy" "$(git -C "$repo" branch --list agentbox/agent-02)" "agentbox/agent-02"
ab remove hello-stack
ab destroy $A1 --force --delete-branch
ab remove hello-stack
host "incus list ab-hello-stack --format csv"
expect "no agent instances left" "$(incus list ab-hello-stack --format csv)" ""

section "Summary"
printf '%s\n' "${results[@]}"
# Since Step 5 the CLI starts a daemon for this throwaway state, and Ctrl-C
# (section 9) detaches instead of rolling back; see scripts/demo/step-5.sh.
"$AB" daemon stop >/dev/null 2>&1
rm -rf "$work"
