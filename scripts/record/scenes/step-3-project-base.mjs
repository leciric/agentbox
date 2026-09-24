// Step 3: save a set-up agent as its project's base, then start a new agent from it.
export default {
  name: 'step-3-project-base',
  out: 'docs/implementation/evidence/step-3/media',
  rcTimeout: 300_000,
  rc: String.raw`
export XDG_CONFIG_HOME="$RECORD_WORK/config" XDG_DATA_HOME="$RECORD_WORK/data"
mkdir -p "$RECORD_WORK/bin"
if [ -n "$AGENTBOX_BIN" ]; then cp "$AGENTBOX_BIN" "$RECORD_WORK/bin/agentbox"; else go build -o "$RECORD_WORK/bin/agentbox" ./cmd/agentbox; fi
export PATH="$RECORD_WORK/bin:$PATH"
repo="$RECORD_WORK/repos/base-demo"
mkdir -p "$RECORD_WORK/repos" && cp -r testdata/fixtures/hello-stack "$repo"
git -C "$repo" init -q -b main && git -C "$repo" add -A && git -C "$repo" -c commit.gpgsign=false commit -qm fixture
agentbox add "$repo" >/dev/null
agentbox create base-demo --ai none >/dev/null 2>&1
agentbox exec base-demo/agent-01 -- 'docker compose up -d --wait && mise use -g node@22 && echo "Postgres: docker compose up -d --wait" > ~/SETUP-NOTES.md' >/dev/null 2>&1
`,
  steps: [
    { comment: 'agent-01 already set the project up: Postgres image, Node 22, notes' },
    { run: `agentbox exec base-demo/agent-01 -- 'docker images --format "{{.Repository}}:{{.Tag}}"; mise ls node'` },
    { comment: 'save its machine as the starting point for new base-demo agents' },
    { run: 'agentbox base save base-demo/agent-01', timeout: 180_000 },
    { run: 'agentbox base show base-demo' },
    { screenshot: 'saved' },
    { run: 'agentbox create base-demo --ai none', timeout: 180_000 },
    { screenshot: 'created-from-base' },
    { comment: 'the new agent already has the image, Node 22 and the notes' },
    { run: `agentbox exec base-demo/agent-02 -- 'docker images --format "{{.Repository}}:{{.Tag}}"; mise ls node; cat ~/SETUP-NOTES.md'` },
    { run: `time agentbox exec base-demo/agent-02 -- 'docker compose up -d --wait'`, timeout: 180_000 },
    { screenshot: 'agent-02-ready' },
  ],
  teardown: String.raw`
export XDG_CONFIG_HOME="$RECORD_WORK/config" XDG_DATA_HOME="$RECORD_WORK/data"
ab="$RECORD_WORK/bin/agentbox"
"$ab" destroy base-demo/agent-02 --force --delete-branch
"$ab" destroy base-demo/agent-01 --force --delete-branch
"$ab" base rm base-demo
"$ab" remove base-demo
`,
};
