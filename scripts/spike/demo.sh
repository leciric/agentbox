#!/usr/bin/env bash
# Step 0 spike: runs the demo from docs/roadmap/00-spike-isolation.md plus the
# checks behind each question. Needs CLAUDE_CODE_OAUTH_TOKEN exported; it is
# never printed.
set -uo pipefail
cd "$(dirname "$0")"

A=./agent.sh
SPIKE_HOME="$HOME/.local/share/agentbox-spike"
REPO="$SPIKE_HOME/repos/hello-stack"
WT1="$SPIKE_HOME/worktrees/agent-01"
WT2="$SPIKE_HOME/worktrees/agent-02"
WT3="$SPIKE_HOME/worktrees/agent-03"
ME=$(id -un)
tmp=$(mktemp -d)
results=()

section() { printf '\n\n######## %s\n' "$*"; }
show() { printf '\n$ %s\n' "$*"; eval "$*" 2>&1; }
agent() { local a=$1; shift; printf '\n[%s]$ %s\n' "$a" "$*"; $A run "$a" "$*" 2>&1; }
expect() { # name got want
  if [[ "$2" == "$3" ]]; then results+=("PASS  $1"); else results+=("FAIL  $1 (got: '$2', want: '$3')"); fi
}
expect_match() { # name got regex
  if [[ "$2" =~ $3 ]]; then results+=("PASS  $1"); else results+=("FAIL  $1 (got: '$2')"); fi
}
wait_http() { for _ in $(seq 1 60); do curl -sf "$1" >/dev/null && return; sleep 1; done; }
wait_boot() { incus exec "$1" -- systemctl is-system-running --wait >/dev/null 2>&1 || true; sleep 2; }
psql_q() { $A run "$1" "docker compose exec -T postgres psql -U postgres -tAc '$2'" 2>/dev/null; }

[[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]] || { echo "export CLAUDE_CODE_OAUTH_TOKEN first" >&2; exit 1; }

section "Setup: two agents on the same repository"
show "git -C $REPO log --oneline"
show "time $A create $REPO agent-01"
show "time $A create $REPO agent-02"
show "incus list ab-spike-agent -c ns4S"
IP1=$($A ip agent-01)
IP2=$($A ip agent-02)

section "1. Same dev-server port (3000) in both agents"
for a in agent-01 agent-02; do agent $a "tmux new-window -d -t main -n dev 'pnpm dev'"; done
wait_http "http://$IP1:3000"
wait_http "http://$IP2:3000"
show "curl -s http://$IP1:3000"
show "curl -s http://$IP2:3000"
agent agent-01 "tmux list-windows -t main -F '#{window_index}:#{window_name}' && tmux capture-pane -p -t main:dev | grep listening"
expect_match "agent-01 serves :3000" "$(curl -s "http://$IP1:3000")" '"host":"ab-spike-agent-01"'
expect_match "agent-02 serves :3000 at the same time" "$(curl -s "http://$IP2:3000")" '"host":"ab-spike-agent-02"'

section "2. Same database port (5432) in both agents, independent data"
for a in agent-01 agent-02; do agent $a "time docker compose up -d --wait 2>&1 | tail -2"; done
for a in agent-01 agent-02; do
  agent $a "docker compose exec -T postgres psql -U postgres -qc \"create table owner(name text); insert into owner values ('$a')\""
done
for a in agent-01 agent-02; do
  agent $a "docker compose exec -T postgres psql -U postgres -tAc 'select name from owner'"
  agent $a "ss -ltn | grep -E ':(3000|5432) '"
done
expect "agent-01 database has only its own row" "$(psql_q agent-01 'select string_agg(name, chr(44)) from owner')" "agent-01"
expect "agent-02 database has only its own row" "$(psql_q agent-02 'select string_agg(name, chr(44)) from owner')" "agent-02"

section "3. Claude Code, authenticated, editing and committing in both agents at once"
agent agent-01 'echo "token in env: ${CLAUDE_CODE_OAUTH_TOKEN:+yes}"; echo "host credentials file present: $(test -f ~/.claude/.credentials.json && echo yes || echo no)"'
for a in agent-01 agent-02; do
  agent $a "time claude -p --model haiku --dangerously-skip-permissions 'Append a new line containing exactly \"hello from $a\" to message.txt, then commit only message.txt with the commit message \"$a says hello\". Reply with only the short commit hash.'" >"$tmp/claude-$a.log" 2>&1 &
done
wait
cat "$tmp/claude-agent-01.log" "$tmp/claude-agent-02.log"

section "4. The host sees each agent's commits; ownership of agent-written files"
show "git -C $REPO log --oneline --graph --all"
show "git -C $REPO show agentbox/agent-01:message.txt"
show "git -C $REPO show agentbox/agent-02:message.txt"
agent agent-01 "touch by-agent-user && sudo touch by-container-root"
show "stat -c '%U (uid %u)  %n' $WT1/message.txt $WT1/by-agent-user $WT1/by-container-root"
expect_match "agent-01 committed on its branch" "$(git -C "$REPO" log -1 --format=%s agentbox/agent-01)" "agent-01"
expect_match "agent-02 committed on its branch" "$(git -C "$REPO" log -1 --format=%s agentbox/agent-02)" "agent-02"
expect "main branch untouched" "$(git -C "$REPO" rev-list --count main)" "1"
expect "file written by the agent user is owned by $ME on the host" "$(stat -c %U "$WT1/by-agent-user")" "$ME"
expect "file written by container root is unprivileged on the host (raw.idmap)" "$(stat -c %u "$WT1/by-container-root")" "1000000"
agent agent-01 "sudo rm by-agent-user by-container-root"

section "5. Snapshot, break, restore (agent-01)"
show "time $A snapshot agent-01 before-break"
agent agent-01 "docker compose exec -T postgres psql -U postgres -qc 'drop table owner' && rm server.mjs && echo broken > message.txt && git commit -qam 'break everything' && git log --oneline -1 && ls"
show "time $A restore agent-01 before-break"
agent agent-01 "docker compose up -d --wait >/dev/null 2>&1; ls; cat message.txt; git log --oneline -2; docker compose exec -T postgres psql -U postgres -tAc 'select name from owner'"
show "git -C $REPO for-each-ref refs/agentbox"
expect "restore brings back deleted file" "$(test -f "$WT1/server.mjs" && echo yes)" "yes"
expect "restore brings back file contents" "$(tail -1 "$WT1/message.txt")" "hello from agent-01"
expect "restore brings back the database table" "$(psql_q agent-01 'select name from owner')" "agent-01"
expect_match "restore resets the branch to the snapshot" "$(git -C "$REPO" log -1 --format=%s agentbox/agent-01)" "agent-01"

section "Q3. Why raw.idmap: the same ownership test with shift=true"
show "IDMAP=shift $A create $REPO agent-03"
agent agent-03 "sudo touch by-container-root"
show "stat -c '%U (uid %u)  %n' $WT3/by-container-root"
expect "shift=true lets container root write files as host root (the risk)" "$(stat -c %u "$WT3/by-container-root")" "0"
agent agent-03 "sudo rm by-container-root"
show "$A destroy agent-03"

section "Q4. Relative worktree links need git >= 2.48"
agent agent-02 "git --version"
agent agent-02 "cd /tmp && rm -rf rel && git init -q rel && git -C rel config core.repositoryformatversion 1 && git -C rel config extensions.relativeWorktrees true && git -C rel status"

section "Q5. Codex inside an agent (access token only; no refresh token leaves the host)"
printf '\n$ jq -r .tokens.access_token ~/.codex/auth.json | incus exec ab-spike-agent-02 -- runuser -l %s -c "codex login --with-access-token"\n' "$ME"
jq -r .tokens.access_token ~/.codex/auth.json | incus exec ab-spike-agent-02 -- runuser -l "$ME" -c 'codex login --with-access-token' 2>&1 | tail -3
agent agent-02 "codex login status"
agent agent-02 "timeout 120 codex exec --skip-git-repo-check 'Reply with exactly: pong' 2>&1 | tail -4"

section "Q6. Docker inside the agent"
agent agent-01 "docker info --format 'server={{.ServerVersion}} storage={{.Driver}} cgroup={{.CgroupVersion}}'"
agent agent-01 "docker run --rm hello-world | head -2"

section "Q7. An Incus snapshot alone does not include the worktree"
show "incus snapshot create ab-spike-agent-02 q7"
agent agent-02 "echo written-after-snapshot > q7.txt"
show "incus snapshot restore ab-spike-agent-02 q7; incus start ab-spike-agent-02 2>/dev/null; true"
wait_boot ab-spike-agent-02
show "cat $WT2/q7.txt"
expect "worktree file survives an Incus-only restore" "$(cat "$WT2/q7.txt" 2>/dev/null)" "written-after-snapshot"
rm -f "$WT2/q7.txt"

section "Q8. tmux session persistence"
agent agent-01 "tmux list-windows -t main -F '#{window_index}:#{window_name}'"
show "incus restart ab-spike-agent-01"
wait_boot ab-spike-agent-01
agent agent-01 "tmux list-sessions 2>&1 || true"

section "Q9. Cost per agent"
show "incus list ab-spike -c nsmMDu"
show "incus info ab-spike-agent-02 | sed -n '/^Resources:/,/^  Network usage:/p'"
show "incus storage info default | head -12"

section "Q10. Later steps: /dev/kvm passthrough and headless Chromium"
show "incus config device add ab-spike-agent-02 kvm unix-char source=/dev/kvm path=/dev/kvm mode=0666"
agent agent-02 "ls -l /dev/kvm && python3 -c 'import fcntl, os; fd = os.open(\"/dev/kvm\", os.O_RDWR); print(\"KVM_GET_API_VERSION =\", fcntl.ioctl(fd, 0xAE00))'"
agent agent-02 "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq chromium >/dev/null 2>&1; chromium --headless --no-sandbox --disable-gpu --window-size=640,240 --screenshot=/tmp/shot.png 'data:text/html,<body style=\"font:32px sans-serif;padding:40px\">Chromium inside ab-spike-agent-02</body>' 2>/dev/null; ls -l /tmp/shot.png"
incus file pull ab-spike-agent-02/tmp/shot.png "$tmp/chromium-headless.png" 2>/dev/null && cp "$tmp/chromium-headless.png" ../../docs/implementation/evidence/step-0/chromium-headless.png
expect "KVM usable inside the agent" "$($A run agent-02 "python3 -c 'import fcntl, os; print(fcntl.ioctl(os.open(\"/dev/kvm\", os.O_RDWR), 0xAE00))'" 2>/dev/null)" "12"

section "Summary"
printf '%s\n' "${results[@]}"
rm -rf "$tmp"
