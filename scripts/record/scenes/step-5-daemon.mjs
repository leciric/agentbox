// Step 5: the daemon. Ctrl-C detaches from a create, the job is followed and
// finishes, agent changes arrive on the event stream, and inside the agent the
// API only knows about that agent.
export default {
  name: 'step-5-daemon',
  out: 'docs/implementation/evidence/step-5/media',
  height: 820,
  rcTimeout: 120_000,
  rc: String.raw`
export XDG_CONFIG_HOME="$RECORD_WORK/config" XDG_DATA_HOME="$RECORD_WORK/data"
mkdir -p "$RECORD_WORK/bin"
if [ -n "$AGENTBOX_BIN" ]; then cp "$AGENTBOX_BIN" "$RECORD_WORK/bin/agentbox"; else go build -o "$RECORD_WORK/bin/agentbox" ./cmd/agentbox; fi
export PATH="$RECORD_WORK/bin:$PATH"
repo="$RECORD_WORK/repos/daemon-demo"
mkdir -p "$RECORD_WORK/repos" && cp -r testdata/fixtures/hello-stack "$repo"
git -C "$repo" init -q -b main && git -C "$repo" add -A && git -C "$repo" -c commit.gpgsign=false commit -qm fixture
cd "$RECORD_WORK/repos"
`,
  steps: [
    { comment: 'the first command starts the daemon on demand' },
    { run: 'agentbox add daemon-demo' },
    { comment: 'Ctrl-C only detaches: the create keeps running in the daemon' },
    { run: 'agentbox create daemon-demo --ai none', wait: false, hold: 100 },
    { waitFor: /Creating instance/, timeout: 60_000, hold: 150 },
    { keys: ['Control+c'] },
    { prompt: true, timeout: 30_000 },
    { pause: 1_500 },
    { run: 'agentbox jobs' },
    { run: "agentbox jobs $(agentbox jobs | awk 'NR == 2 { print $1 }')", timeout: 120_000, hold: 2_000 },
    { screenshot: 'detached-job' },
    { run: 'clear', hold: 200 },
    { comment: 'the event stream: agent state changes and resource samples, live' },
    { run: `(timeout 14 agentbox events | grep -v '  log  ' | cut -c1-150) & sleep 1; agentbox stop daemon-demo/agent-01; agentbox start daemon-demo/agent-01; wait`, timeout: 60_000, hold: 2_000 },
    { screenshot: 'events' },
    { run: 'clear', hold: 200 },
    { comment: 'inside the agent, the API only answers about this agent' },
    { run: 'agentbox exec daemon-demo/agent-01 -- agentbox whoami' },
    { run: "agentbox exec daemon-demo/agent-01 -- 'curl -s --unix-socket /run/agentbox.sock http://agentbox/v1/projects'", hold: 2_500 },
    { screenshot: 'in-agent-api' },
  ],
  teardown: String.raw`
export XDG_CONFIG_HOME="$RECORD_WORK/config" XDG_DATA_HOME="$RECORD_WORK/data"
ab="$RECORD_WORK/bin/agentbox"
"$ab" destroy daemon-demo/agent-01 --force --delete-branch
"$ab" remove daemon-demo
"$ab" daemon stop
`,
};
