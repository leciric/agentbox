// Step 4: snapshot before a risky change, break things, restore, fork, pause and top.
export default {
  name: 'step-4-snapshots',
  out: 'docs/implementation/evidence/step-4/media',
  height: 820,
  rcTimeout: 300_000,
  rc: String.raw`
export XDG_CONFIG_HOME="$RECORD_WORK/config" XDG_DATA_HOME="$RECORD_WORK/data"
mkdir -p "$RECORD_WORK/bin"
if [ -n "$AGENTBOX_BIN" ]; then cp "$AGENTBOX_BIN" "$RECORD_WORK/bin/agentbox"; else go build -o "$RECORD_WORK/bin/agentbox" ./cmd/agentbox; fi
export PATH="$RECORD_WORK/bin:$PATH"
repo="$RECORD_WORK/repos/snap-demo"
mkdir -p "$RECORD_WORK/repos" && cp -r testdata/fixtures/hello-stack "$repo"
git -C "$repo" init -q -b main && git -C "$repo" add -A && git -C "$repo" -c commit.gpgsign=false commit -qm fixture
agentbox add "$repo" >/dev/null
agentbox create snap-demo --ai none >/dev/null 2>&1
agentbox exec snap-demo/agent-01 -- "docker compose up -d --wait && docker compose exec -T postgres psql -U postgres -qc 'create table doses(mg int); insert into doses values (500)' && echo 'reminders: on' >> message.txt && git -c user.name=agent -c user.email=agent@x commit -qam 'reminders' && echo 'draft: snooze button' >> message.txt" >/dev/null 2>&1
`,
  steps: [
    { comment: 'agent-01 has a commit, an uncommitted edit and a database table' },
    { run: `agentbox exec snap-demo/agent-01 -- "git log --oneline -1; git status --short; docker compose exec -T postgres psql -U postgres -tAc 'select mg from doses'"` },
    { run: 'agentbox snapshot snap-demo/agent-01 before-migration --consistent' },
    { comment: 'the agent runs a bad migration and deletes a file' },
    { run: `agentbox exec snap-demo/agent-01 -- "docker compose exec -T postgres psql -U postgres -qc 'drop table doses' && rm server.mjs && ls"` },
    { screenshot: 'broken' },
    { run: 'agentbox restore snap-demo/agent-01 before-migration', timeout: 180_000 },
    { run: `agentbox exec snap-demo/agent-01 -- "docker compose up -d --wait >/dev/null 2>&1; ls; git status --short; docker compose exec -T postgres psql -U postgres -tAc 'select mg from doses'"`, timeout: 180_000 },
    { screenshot: 'restored' },
    { comment: 'try a second approach from the same snapshot' },
    { run: 'agentbox fork snap-demo/agent-01@before-migration', timeout: 180_000 },
    { run: 'agentbox pause snap-demo/agent-01' },
    { run: 'agentbox top' },
    { screenshot: 'top' },
  ],
  teardown: String.raw`
export XDG_CONFIG_HOME="$RECORD_WORK/config" XDG_DATA_HOME="$RECORD_WORK/data"
ab="$RECORD_WORK/bin/agentbox"
"$ab" destroy snap-demo/agent-02 --force --delete-branch
"$ab" destroy snap-demo/agent-01 --force --delete-branch
"$ab" remove snap-demo
`,
};
