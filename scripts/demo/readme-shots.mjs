#!/usr/bin/env node
// Takes the screenshots in the README (.github/assets/) from the real app, on a
// throwaway AgentBox whose state is synthetic: a made-up project, "reminders",
// with a lead, four agents, a chat each, a token ledger, two Claude accounts'
// limits and a project memory, all written straight into its state.db. Nothing
// talks to Anthropic or GitHub, and a stub incus says the machines are running.
//
// One agent's Desktop tab is this machine's own display: when the script runs
// inside an AgentBox agent, its VNC server (127.0.0.1:5900) and Chromium's
// DevTools (127.0.0.1:9222) are bridged to the sockets the daemon reads an
// agent's desktop from. Start the browser first (`agentbox browser start`);
// without it, that shot is skipped.
//
//   mise exec -- node scripts/demo/readme-shots.mjs            # writes .demo-runs/readme/*.png
//
// Then optimise them into .github/assets/: pngquant --quality 70-90 --strip.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { join, resolve } from 'node:path';
import zlib from 'node:zlib';
import { DatabaseSync } from 'node:sqlite';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const out = join(root, '.demo-runs/readme');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

// A neutral home, since paths show in the app (made with sudo if missing);
// short, because the daemon's unix socket has to fit in 107 bytes.
const work = '/home/demo';
const bin = join(work, 'bin', 'agentbox');
const data = join(work, 'data', 'agentbox');
const socket = join(data, 'run', 'agentbox.sock');
const P = 'reminders';
const repo = join(work, 'src', P);
// What only exists because there are no real machines here: the setup
// checklist isn't done, each agent's chat can't reach a real adapter, and
// idle stub machines count as finished ones still holding a machine. None of
// it is part of what a shot is meant to show, so each banner goes: the
// smallest element holding both its text and its button.
const tidy = () => {
  const banners = [
    ['Finish setup', 'Finish setup'],
    ['isn\u2019t set up yet', 'Open Settings'],
    ["isn't set up yet", 'Open Settings'],
    ['still holding a machine', 'Free their machines'],
    ['closed the connection', 'Start again'],
  ];
  for (const [text, button] of banners) {
    const hits = [...document.querySelectorAll('body *')].filter((el) => el.textContent.includes(text) && el.textContent.includes(button));
    hits.sort((a, b) => a.textContent.length - b.textContent.length)[0]?.remove();
  }
};
const W = 1440;
const H = 960;

const env = {
  ...process.env,
  HOME: work,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  ANTHROPIC_BASE_URL: 'http://127.0.0.1:1',
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'GH_TOKEN', 'GITHUB_TOKEN']) delete env[name];

const log = (...parts) => console.log('·', ...parts);
const ab = (...args) => spawnSync(bin, args, { env, encoding: 'utf8', input: '', timeout: 300_000 });
const git = (dir, ...args) =>
  execFileSync('git', ['-C', dir, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', '-c', 'commit.gpgsign=false', ...args], {
    encoding: 'utf8',
  }).trim();

const now = Date.now();
const ago = (minutes) => now - minutes * 60_000;
const iso = (ms) => new Date(ms).toISOString();
const seconds = (ms) => Math.floor(ms / 1000);

// The agents, and what the stub incus says about their machines. agent-01 is
// the one whose desktop is this machine's.
const agents = [
  { name: 'agent-01', ai: 'claude', title: 'Paginate the reminders list', created: 95 },
  { name: 'agent-02', ai: 'claude', title: 'Keep the digest email in step', created: 60 },
  { name: 'agent-03', ai: 'codex', title: 'Fix the flaky e2e run', created: 45 },
  { name: 'agent-04', ai: 'opencode', title: 'Dark mode for the settings page', created: 20 },
].map((a) => ({ ...a, instance: `ab-${P}-${a.name}` }));

function stubIncus() {
  mkdirSync(join(work, 'stub'), { recursive: true });
  const instances = JSON.stringify(
    agents.map((a) => ({ name: a.instance, status: 'Running', config: {}, expanded_config: {}, state: { network: {} } })),
  );
  const devices = JSON.stringify({ config: {}, devices: { 'agentbox-vnc': { type: 'proxy' }, 'agentbox-cdp': { type: 'proxy' } } });
  // An agent's chat is an ACP adapter run with `incus exec`: this one answers
  // the handshake and nothing else, so the chat shows as connected and idle.
  writeFileSync(
    join(work, 'stub', 'acp.mjs'),
    `import { createInterface } from 'node:readline';
const reply = (id, result) => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id, result }) + '\\n');
createInterface({ input: process.stdin }).on('line', (line) => {
  const m = JSON.parse(line);
  if (m.id === undefined || !m.method) return;
  if (m.method === 'initialize') reply(m.id, { protocolVersion: 1, agentCapabilities: { loadSession: true }, authMethods: [] });
  else if (m.method === 'session/new') reply(m.id, { sessionId: 'readme-session' });
  else reply(m.id, {});
});
`,
  );
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/bin/sh
case "$*" in
  exec*"mise exec"*) exec node ${join(work, 'stub', 'acp.mjs')} ;;
esac
case "$1" in
  list) cat <<'JSON'
${instances}
JSON
  ;;
  query) case "$2" in
    /1.0/instances/${agents[0].instance}) echo '${devices}' ;;
    /1.0/instances/*/snapshots) echo '[]' ;;
    /1.0/instances/*) echo '{"config": {}, "devices": {}}' ;;
    *) echo '{}' ;;
  esac ;;
esac
exit 0
`,
    { mode: 0o755 },
  );
}

// The project: a small reminders service, enough for the app's repository views.
function makeRepo() {
  mkdirSync(join(repo, 'src'), { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# reminders\n\nA small reminders service: an API, a web page and a daily digest email.\n');
  writeFileSync(join(repo, 'package.json'), '{\n  "name": "reminders",\n  "private": true,\n  "scripts": { "dev": "node src/server.mjs", "test": "node --test" }\n}\n');
  writeFileSync(join(repo, 'src', 'server.mjs'), "import http from 'node:http';\nhttp.createServer((_, res) => res.end('ok')).listen(3000);\n");
  git(repo, 'add', '-A');
  git(repo, 'commit', '-qm', 'first');
}

const item = (id, turn, kind, text, at, extra = {}) => ({ id, turn, kind, text, createdAt: iso(at), updatedAt: iso(at), ...extra });
const ended = (at) => ({ result: { state: 'completed', stopReason: 'end_turn', endedAt: iso(at) } });
const tool = (id, turn, at, t, extra = {}) => ({
  id,
  turn,
  kind: 'tool',
  tool: { callId: id, status: 'completed', ...t },
  createdAt: iso(at),
  updatedAt: iso(at),
  ...extra,
});

function leadConversation() {
  return [
    item('u1', 'u1', 'user', 'The reminders list is unusable past a hundred rows, and the e2e run has been flaky all week. Get people on both.', ago(96), ended(ago(94))),
    item(
      'a1',
      'u1',
      'assistant',
      "On it. I've made two agents:\n\n- **agent-01** paginates the list, twenty a page, with the page in the query string so a link to page three stays page three.\n- **agent-03** finds out why `e2e/reminders.spec.ts` fails one run in five, on Codex so it doesn't share agent-01's Claude limits.\n\nProject memory says the snapshot tests of the list were rewritten once before and broke the digest, so I've told agent-01 to leave `digest.ts` alone and say if it has to touch it.",
      ago(95),
    ),
    item('u2', 'u2', 'user', 'Good. And the settings page still has no dark mode.', ago(22), ended(ago(20))),
    item('a2', 'u2', 'assistant', "I've given that to **agent-04**, on OpenCode. It will keep screenshots of both themes in Media so you can compare them without starting it.", ago(21)),
    item('n1', 'u3', 'notice', 'agent-01 (Paginate the reminders list) finished. It has changed 4 file(s), +186/-24.', ago(9), { hidden: true }),
    item('u3', 'u3', 'user', 'agent-01 (Paginate the reminders list) finished.', ago(9), { hidden: true, ...ended(ago(8)) }),
    item(
      'a3',
      'u3',
      'assistant',
      "**agent-01 is done** and [#58](https://github.com/example/reminders/pull/58) is open, checks passing. It kept a recording of paging through 240 reminders.\n\nThe digest email still counts every reminder, so I've asked **agent-02** to keep it in step with the new page size. It has a question about time zones that is yours to answer — it's in the rail on the right.",
      ago(8),
    ),
  ];
}

function agentConversation() {
  const T = 't1';
  return [
    item('w1', T, 'user', 'Paginate the reminders list: twenty a page, with the page in the query string. Leave digest.ts alone. Prove it works with a recording.', ago(94), ended(ago(10))),
    {
      id: 'p1',
      turn: T,
      kind: 'plan',
      plan: [
        { content: 'Read how the list is loaded today', status: 'completed' },
        { content: 'Add ?page= to the list route and the API call', status: 'completed' },
        { content: 'Pager component, with the page kept in the URL', status: 'completed' },
        { content: 'Rewrite the two snapshot tests of the unpaginated list', status: 'completed' },
        { content: 'Record paging through 240 reminders', status: 'completed' },
      ],
      createdAt: iso(ago(93)),
      updatedAt: iso(ago(12)),
    },
    tool('t1', T, ago(92), { name: 'Read', title: 'Read src/web/ReminderList.tsx', kind: 'read', paths: ['src/web/ReminderList.tsx'] }),
    tool('t2', T, ago(91), { name: 'Grep', title: 'grep "limit|offset" src/api', kind: 'search' }),
    item('m1', T, 'assistant', 'The API already takes `limit` and `offset`, so nothing changes on the server. The page just asks for everything. Adding a `page` search param and a pager.', ago(90)),
    tool('t3', T, ago(80), {
      name: 'Edit',
      title: 'Edit src/web/ReminderList.tsx',
      kind: 'edit',
      paths: ['src/web/ReminderList.tsx'],
      diffs: [
        {
          path: 'src/web/ReminderList.tsx',
          oldText:
            "export function ReminderList() {\n  const { data } = useQuery(['reminders'], () => api.reminders());\n  return <List items={data ?? []} />;\n}\n",
          newText:
            "const PAGE = 20;\n\nexport function ReminderList() {\n  const [params, setParams] = useSearchParams();\n  const page = Math.max(1, Number(params.get('page')) || 1);\n  const { data } = useQuery(['reminders', page], () =>\n    api.reminders({ limit: PAGE, offset: (page - 1) * PAGE }),\n  );\n  return (\n    <>\n      <List items={data?.items ?? []} />\n      <Pager page={page} total={data?.total ?? 0} size={PAGE} onChange={(p) => setParams({ page: String(p) })} />\n    </>\n  );\n}\n",
        },
      ],
    }),
    tool('t4', T, ago(40), {
      name: 'Bash',
      title: 'npm test',
      kind: 'execute',
      command: 'npm test',
      output: '▶ ReminderList\n  ✔ shows the first twenty (12ms)\n  ✔ keeps the page in the query string (9ms)\n  ✔ clamps a page past the end (4ms)\n▶ Pager (3 tests)\nℹ tests 31\nℹ pass 31\nℹ fail 0',
    }),
    tool('t5', T, ago(20), { name: 'Bash', title: 'agentbox media record stop', kind: 'execute', command: 'agentbox media record stop', output: 'saved paging-through-240.webm (38s)' }),
    tool('t6', T, ago(14), { name: 'Bash', title: 'gh pr create --fill', kind: 'execute', command: 'gh pr create --fill', output: 'https://github.com/example/reminders/pull/58' }),
    item(
      'm2',
      T,
      'assistant',
      "Done. The list shows twenty a page and the page lives in the query string, so a link to page three is a link to page three.\n\n- **4 files**, +186/−24. Nothing server side.\n- The two snapshot tests of the old list needed rewriting, which is worth a look.\n- `paging-through-240.webm` in Media shows it working.\n\nPull request: #58.",
      ago(11),
    ),
  ];
}

function agentEvents() {
  const ev = (agent, title, kind, at, rest = {}) => ({ project: P, agent, ref: `${P}/${agent}`, title, kind, at: iso(at), ...rest });
  return [
    ...agents.map((a) => ev(a.name, a.title, 'created', ago(a.created), { summary: a.title })),
    ev('agent-01', agents[0].title, 'finished', ago(9), {
      summary:
        'Added pagination to the reminders list: twenty a page, with the page in the query string.\n\nThe API already took `limit` and `offset`, so nothing changed server side. The two snapshot tests of the old list needed rewriting.',
      changes: { files: 4, insertions: 186, deletions: 24, dirty: false },
      pr: { number: 58, title: 'Paginate the reminders list', state: 'open', checks: 'passing', url: 'https://github.com/example/reminders/pull/58' },
    }),
    ev('agent-02', agents[1].title, 'asked', ago(3), { question: 'q1' }),
  ];
}

// Tokens: [agent, minutes ago, kind, model, calls, average context, context at the end, output].
const price = {
  'claude-sonnet-5': { in: 3, out: 15, read: 0.3, write: 3.75 },
  'claude-haiku-4-5': { in: 1, out: 5, read: 0.1, write: 1.25 },
  'claude-opus-5-5': { in: 5, out: 25, read: 0.5, write: 6.25 },
  'gpt-5.5-codex': { in: 1.25, out: 10, read: 0.125, write: 0 },
};
const turns = [
  ['lead', 280, 'turn', 'claude-opus-5-5', 10, 60_000, 72_000, 6_000],
  ['agent-01', 270, 'turn', 'claude-sonnet-5', 120, 70_000, 110_000, 30_000],
  ['agent-01', 230, 'turn', 'claude-haiku-4-5', 90, 40_000, 0, 9_000],
  ['agent-01', 200, 'turn', 'claude-sonnet-5', 160, 120_000, 170_000, 42_000],
  ['agent-03', 190, 'turn', 'gpt-5.5-codex', 140, 90_000, 130_000, 35_000],
  ['agent-01', 150, 'compaction', 'claude-sonnet-5', 1, 190_000, 190_000, 4_000],
  ['agent-02', 120, 'turn', 'claude-sonnet-5', 80, 50_000, 88_000, 20_000],
  ['agent-03', 100, 'turn', 'gpt-5.5-codex', 110, 100_000, 150_000, 28_000],
  ['lead', 95, 'turn', 'claude-opus-5-5', 8, 80_000, 90_000, 5_000],
  ['agent-01', 60, 'turn', 'claude-sonnet-5', 140, 90_000, 140_000, 38_000],
  ['agent-04', 20, 'turn', 'claude-sonnet-5', 60, 40_000, 70_000, 16_000],
  ['agent-02', 15, 'turn', 'claude-sonnet-5', 70, 70_000, 96_000, 18_000],
  ['lead', 8, 'turn', 'claude-opus-5-5', 6, 95_000, 101_000, 4_000],
];

function memories() {
  const m = (id, kind, title, content, importance, minutes) => ({ id, kind, title, content, importance, at: ago(minutes) });
  return [
    m('m1', 'project', 'The list API already paginates', 'GET /api/reminders takes limit and offset and returns { items, total }. The web page used to ask for everything; since #58 it asks for twenty at a time.', 4, 10),
    m('m2', 'decision', 'Twenty a page, and the page lives in the URL', 'Chosen so a link to page three stays page three, and the back button works. Rejected: infinite scroll, which the digest email could not mirror.', 4, 9),
    m('m3', 'discovery', 'The e2e run is flaky because of the clock', 'e2e/reminders.spec.ts creates a reminder "due in 1 minute" and asserts it is upcoming; when the run crosses a minute boundary it is already due. Freeze the clock in the fixture instead of retrying.', 5, 30),
    m('m4', 'issue', 'The digest email still counts every reminder', 'digest.ts builds its own query and never learned about pagination. agent-02 is on it; the send time in other time zones is a question for the user.', 4, 3),
    m('m5', 'discovery', 'Postgres in an agent: use the compose file', 'docker compose up -d db starts Postgres 17 on 5432 with the seed in db/seed.sql. The tests expect DATABASE_URL=postgres://reminders@localhost/reminders.', 3, 180),
    m('m6', 'episodic', 'Snapshot tests of the list were rewritten twice', 'Both times the digest broke, because it imports the same fixtures. Check digest.test.ts whenever the list fixtures change.', 3, 240),
  ];
}

function tasks() {
  const t = (id, agent, status, goal, minutes, parent = null) => ({ id, agent, status, goal, at: ago(minutes), parent });
  return [
    t('task-1', '', 'open', 'Make the reminders list usable past a hundred rows', 96),
    t('task-2', 'agent-01', 'done', 'Paginate the reminders list', 95, 'task-1'),
    t('task-3', 'agent-02', 'blocked', 'Keep the digest email in step with the page size', 60, 'task-1'),
    t('task-4', 'agent-03', 'in_progress', 'Fix the flaky e2e run', 45),
    t('task-5', 'agent-04', 'in_progress', 'Dark mode for the settings page', 20),
  ];
}

function openState() {
  for (let i = 0; ; i++) {
    try {
      const db = new DatabaseSync(join(data, 'state.db'));
      db.prepare('SELECT count(*) FROM projects').get();
      return db;
    } catch (err) {
      if (i > 60) throw err;
      execFileSync('sleep', ['0.5']);
    }
  }
}

function seed() {
  const db = openState();
  const addAgent = db.prepare(
    `INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at, source, title, claude_account, interface, role, github_account, finish_notice)
     VALUES (?, ?, ?, ?, 1, ?, 'main', '', ?, 'ready', ?, '', ?, ?, 'chat', ?, '', '')`,
  );
  const trees = join(data, 'worktrees', P);
  mkdirSync(trees, { recursive: true });
  git(repo, 'worktree', 'add', '--quiet', '--detach', join(trees, 'lead'), 'main');
  addAgent.run(P, 'lead', `ab-${P}-lead`, 'claude', '', join(trees, 'lead'), seconds(ago(100)), 'Project chat', '', 'lead');
  for (const a of agents) {
    const tree = join(trees, a.name);
    git(repo, 'worktree', 'add', '--quiet', '-b', `agentbox/${a.name}`, tree, 'main');
    addAgent.run(P, a.name, a.instance, a.ai, `agentbox/${a.name}`, tree, seconds(ago(a.created)), a.title, a.name === 'agent-02' ? 'work' : '', 'worker');
  }
  // agent-01's work, so its diff and changed files are real.
  const t1 = join(trees, 'agent-01');
  mkdirSync(join(t1, 'src', 'web'), { recursive: true });
  writeFileSync(join(t1, 'src', 'web', 'Pager.tsx'), 'export function Pager() {\n  return null;\n}\n');
  git(t1, 'add', '-A');
  git(t1, 'commit', '-qm', 'feat: paginate the reminders list');

  const addItem = db.prepare(`INSERT INTO chat_items (project, agent, id, position, data) VALUES (?, ?, ?, ?, ?)`);
  leadConversation().forEach((it, i) => addItem.run(P, 'lead', it.id, i, JSON.stringify(it)));
  agentConversation().forEach((it, i) => addItem.run(P, 'agent-01', it.id, i, JSON.stringify(it)));

  const addEvent = db.prepare(`INSERT INTO agent_events (id, project, agent, created_at, data) VALUES (?, ?, ?, ?, ?)`);
  agentEvents().forEach((ev, i) => addEvent.run(`e${i}`, P, ev.agent, Date.parse(ev.at), JSON.stringify({ id: `e${i}`, ...ev })));
  db.prepare(
    `INSERT INTO questions (id, project, agent, text, context, status, answer, answered_by, escalation, created_at, answered_at)
     VALUES ('q1', ?, 'agent-02', ?, ?, 'escalated', '', '', ?, ?, 0)`,
  ).run(
    P,
    'The digest sends at 08:00 UTC. Paginating it changes what "today\'s reminders" means for anyone in another time zone. Leave the send time alone, or move it to each user\'s local morning?',
    'keeping the digest email in step with the paginated list',
    "It changes behaviour for existing users, which isn't mine to decide.",
    ago(3),
  );

  const addTurn = db.prepare(
    `INSERT INTO token_usage (project, agent, ai, session_id, turn, kind, model, at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, context_tokens) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
  );
  turns.forEach(([agent, minutes, kind, model, calls, avg, context, output], i) => {
    const p = price[model];
    const read = calls * avg;
    const write = Math.round(avg * 0.02 + 10_000);
    const input = calls * 2;
    const cost = (input * p.in + output * p.out + read * p.read + write * p.write) / 1e6;
    const ai = model.startsWith('gpt') ? 'codex' : 'claude';
    addTurn.run(P, agent, ai, `session-${agent}`, `turn-${i}`, kind, model, ago(minutes), input, output, read, write, Number(cost.toFixed(4)), context);
  });
  const unix = seconds(now);
  for (const [account, five, week, status, minutes] of [
    ['default', 0.64, 0.38, 'allowed', 4],
    ['work', 0.21, 0.17, 'allowed', 15],
  ]) {
    const reading = JSON.stringify({
      status,
      resetsAt: unix + 2 * 3600,
      rateLimitType: 'five_hour',
      unifiedWindows: { five_hour: { utilization: five, resetsAt: unix + 2 * 3600 }, seven_day: { utilization: week, resetsAt: unix + 70 * 3600 } },
    });
    db.prepare(`INSERT INTO claude_limits (account, reading, at) VALUES (?, ?, ?)`).run(account, reading, ago(minutes));
  }

  const addMemory = db.prepare(`INSERT INTO memories (id, project, kind, title, content, importance, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`);
  for (const x of memories()) addMemory.run(x.id, P, x.kind, x.title, x.content, x.importance, x.at, x.at);
  db.prepare(`INSERT OR REPLACE INTO working_memory (project, data, updated_at) VALUES (?, ?, ?)`).run(
    P,
    JSON.stringify({
      goal: 'Make the reminders app hold up past a hundred reminders',
      currentTask: 'Digest email in step with the paginated list',
      activeAgents: ['agent-02', 'agent-03', 'agent-04'],
      blockers: ['Digest send time across time zones: waiting on the user'],
    }),
    ago(3),
  );
  const addTask = db.prepare(`INSERT INTO tasks (id, project, agent, parent_task_id, status, goal, created_at, updated_at, closed_at) VALUES (?,?,?,?,?,?,?,?,?)`);
  for (const t of tasks()) addTask.run(t.id, P, t.agent, t.parent, t.status, t.goal, t.at, t.at, t.status === 'done' ? ago(9) : 0);
  db.prepare(`INSERT INTO task_dependencies (project, task_id, depends_on_id, created_at) VALUES (?, 'task-3', 'task-2', ?)`).run(P, ago(60));
  db.prepare(
    `INSERT INTO agent_reports (id, project, agent, created_at, task, status, summary, discoveries, decisions, remaining_issues) VALUES ('r1', ?, 'agent-01', ?, 'Paginate the reminders list', 'done', ?, ?, ?, '[]')`,
  ).run(
    P,
    ago(9),
    'The list shows twenty a page, with the page in the query string. #58 is open, checks passing.',
    JSON.stringify(['The list API already took limit and offset; only the page asked for everything.']),
    JSON.stringify(['Twenty a page, in the URL, so links and the back button keep working.']),
  );
  seedMedia(db);
  db.close();
}

// Bridges this machine's display to agent-01's desktop sockets: the VNC
// server, and Chromium's DevTools for the list of open pages.
function bridgeDesktop() {
  const sum = createHash('sha256').update(agents[0].instance).digest('hex').slice(0, 12);
  const base = join(data, 'run', 'agents', sum);
  mkdirSync(join(data, 'run', 'agents'), { recursive: true });
  const procs = [];
  for (const [service, port] of [
    ['vnc', 5900],
    ['cdp', 9222],
  ]) {
    rmSync(`${base}.${service}`, { force: true });
    procs.push(spawn('socat', [`UNIX-LISTEN:${base}.${service},fork,mode=600`, `TCP:127.0.0.1:${port}`], { stdio: 'ignore' }));
  }
  process.once('exit', () => procs.forEach((p) => p.kill()));
}

// A page for agent-01's browser to show: the paginated list it built.
function servePage() {
  const rows = Array.from({ length: 20 }, (_, i) => {
    const n = 41 + i;
    const titles = ['Renew the passport', 'Water the plants', 'Call the dentist', 'Pay the electricity bill', 'Book the car service', 'Return the library books', 'Back up the laptop', 'Buy a birthday card'];
    return `<li><input type="checkbox"${i % 5 === 1 ? ' checked' : ''}><span>${titles[i % titles.length]}</span><time>${['Today', 'Tomorrow', 'Fri', 'Mon', 'Next week'][i % 5]}</time><em>#${n}</em></li>`;
  }).join('');
  const html = `<!doctype html><meta charset="utf-8"><title>Reminders · page 3</title><style>
body{font:15px system-ui,sans-serif;background:#f6f7f9;margin:0;color:#1d2330}
header{background:#1d2330;color:#fff;padding:14px 28px;font-weight:600;display:flex;gap:16px;align-items:center}
header small{opacity:.6;font-weight:400}main{max-width:760px;margin:24px auto;padding:0 20px}
ul{list-style:none;padding:0;margin:0;background:#fff;border-radius:10px;box-shadow:0 1px 3px #0002}
li{display:flex;gap:12px;align-items:center;padding:10px 16px;border-top:1px solid #eee}li:first-child{border:0}
li span{flex:1}li time{color:#667;font-size:13px}li em{color:#aab;font-style:normal;font-size:12px;width:36px;text-align:right}
nav{display:flex;gap:6px;justify-content:center;margin:18px 0}nav a{padding:6px 12px;border-radius:6px;background:#fff;color:#1d2330;text-decoration:none;box-shadow:0 1px 2px #0002}
nav a.on{background:#4f6bed;color:#fff}</style>
<header>Reminders <small>240 reminders · page 3 of 12</small></header><main><ul>${rows}</ul>
<nav><a href="?page=2">‹</a><a href="?page=1">1</a><a href="?page=2">2</a><a class="on">3</a><a href="?page=4">4</a><a>…</a><a href="?page=12">12</a><a href="?page=4">›</a></nav></main>`;
  const server = http.createServer((_, res) => res.end(html)).listen(3000, '127.0.0.1');
  process.once('exit', () => server.close());
}

// A minimal RGB PNG encoder, so the Media shot has real image files to show
// without needing a browser or an image library: draw(x, y) returns [r, g, b].
function makePng(width, height, draw) {
  const raw = Buffer.alloc(height * (1 + width * 3));
  let p = 0;
  for (let y = 0; y < height; y++) {
    raw[p++] = 0; // no filter
    for (let x = 0; x < width; x++) {
      const [r, g, b] = draw(x, y);
      raw[p++] = r;
      raw[p++] = g;
      raw[p++] = b;
    }
  }
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(zlib.crc32(body) >>> 0);
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // color type: RGB
  const sig = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  return Buffer.concat([sig, chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}

// agent-01's Media tab: the recording it says it kept, and two screenshots of
// the paginated list, drawn synthetically in the same style as servePage().
function seedMedia(db) {
  const banner = (accent) => (_x, y) => (y < 56 ? accent : Math.floor(y / 22) % 2 === 0 ? [255, 255, 255] : [241, 243, 246]);
  const addMedia = db.prepare(
    `INSERT INTO media (id, project, agent, kind, name, file, mime, size, sha256, source, text, meta, created_at, orphaned_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
  );
  const write = (id, filename, buf) => {
    const dir = join(data, 'media', P, 'agent-01', id);
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, filename), buf);
    return { file: join(id, filename), sha256: createHash('sha256').update(buf).digest('hex') };
  };
  const items = [
    {
      id: 'media-list-1',
      kind: 'screenshot',
      name: 'list-page-1.png',
      buf: makePng(480, 300, banner([29, 35, 48])),
      mime: 'image/png',
      meta: { target: 'browser', width: 480, height: 300 },
      at: ago(93),
    },
    {
      id: 'media-list-3',
      kind: 'screenshot',
      name: 'list-page-3.png',
      buf: makePng(480, 300, banner([79, 107, 237])),
      mime: 'image/png',
      meta: { target: 'browser', width: 480, height: 300 },
      at: ago(21),
    },
    {
      id: 'media-paging',
      kind: 'recording',
      name: 'paging-through-240.webm',
      buf: Buffer.alloc(4096),
      mime: 'video/webm',
      meta: { target: 'browser', duration: 38 },
      at: ago(20),
    },
  ];
  for (const it of items) {
    const { file, sha256 } = write(it.id, it.name, it.buf);
    addMedia.run(it.id, P, 'agent-01', it.kind, it.name, file, it.mime, it.buf.length, sha256, 'agent', '', JSON.stringify(it.meta), it.at);
  }
}

async function main() {
  spawnSync('sudo', ['rm', '-rf', work]);
  execFileSync('sudo', ['install', '-d', '-o', String(process.getuid()), '-g', String(process.getgid()), work]);
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });
  log('building agentbox and the app');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  stubIncus();
  // Two Claude accounts to hold limits, with made-up tokens that never leave
  // the machine (ANTHROPIC_BASE_URL points nowhere).
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: 'sk-ant-oat01-demo-default\n' });
  spawnSync(bin, ['auth', 'claude', '--token-stdin', '--account', 'work'], { env, input: 'sk-ant-oat01-demo-work\n' });
  makeRepo();
  const added = ab('add', repo, '--name', P);
  if (added.status !== 0) throw new Error(added.stderr);
  ab('daemon', 'stop');
  seed();

  const desktopUp = spawnSync('curl', ['-sf', 'http://127.0.0.1:9222/json/version']).status === 0;
  if (desktopUp) {
    servePage();
    bridgeDesktop();
    spawnSync('agentbox', ['browser', 'open', 'http://localhost:3000/?page=3']);
  } else log('no browser on this machine: skipping the Desktop shot');

  const xvfb = await startXvfb({ width: W, height: H });
  const app = await electron.launch({ args: [desktop, '--disable-gpu', '--no-sandbox'], cwd: desktop, env: { ...env, DISPLAY: xvfb.display } });
  const page = await app.firstWindow();
  await page.setViewportSize({ width: W, height: H }).catch(() => {});
  await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
  const shot = async (name) => {
    await page.waitForTimeout(900);
    await page.evaluate(tidy);
    await page.screenshot({ path: join(out, `${name}.png`) });
    log('shot', name);
  };
  const tab = (name) => page.getByRole('tab', { name }).first().click();
  const openProject = () => page.locator(`[data-project="${P}"]`).first().click();
  const openAgent = (title) => page.locator(`button:has-text("${title}")`).first().click();
  const attempt = async (name, fn) => {
    try {
      await fn();
    } catch (err) {
      log('FAILED', name, err.message.split('\n')[0]);
      await page.screenshot({ path: join(out, `failed-${name}.png`) }).catch(() => {});
    }
  };

  await attempt('lead-chat', async () => {
    await openProject();
    await page.getByText('agent-01 is done').first().waitFor({ timeout: 15_000 });
    // The lead's first start installs its adapter on this machine.
    await page.getByText(/Starting Claude Code|Installing/).first().waitFor({ state: 'hidden', timeout: 180_000 }).catch(() => {});
    await shot('lead-chat');
  });
  await attempt('agent-chat', async () => {
    await openAgent(agents[0].title);
    await tab(/Chat/);
    await page.getByText('Pull request: #58').first().waitFor({ timeout: 15_000 });
    await page.locator('[data-chat-fold]').first().click().catch(() => {});
    await shot('agent-chat');
  });
  await attempt('agent-media', async () => {
    await openAgent(agents[0].title);
    await tab(/Media/);
    await page.locator('[data-media]').first().waitFor({ timeout: 15_000 });
    await shot('agent-media');
  });
  if (desktopUp)
    await attempt('agent-desktop', async () => {
      await openAgent(agents[0].title);
      await tab(/Desktop|Browser/);
      await page.waitForTimeout(4000);
      await shot('agent-desktop');
    });
  await attempt('tokens', async () => {
    await openProject();
    await tab(/Tokens/);
    await page.locator('[data-token-agent]').first().waitFor({ timeout: 15_000 });
    await page.getByText(/over time/i).first().evaluate((el) => el.scrollIntoView({ block: 'center' })).catch(() => {});
    await shot('tokens');
  });
  await attempt('memory', async () => {
    await openProject();
    await tab(/Memory/);
    await tab(/Memories/);
    await page.getByText('The e2e run is flaky because of the clock').first().waitFor({ timeout: 15_000 });
    await shot('memory');
  });
  await attempt('tasks', async () => {
    await tab(/Tasks/);
    await page.getByText('Keep the digest email in step with the page size').first().waitFor({ timeout: 15_000 });
    await shot('memory-tasks');
  });
  await attempt('agents', async () => {
    await openProject();
    await tab(/^Agents/);
    await page.waitForTimeout(1500);
    await shot('project-agents');
  });
  await attempt('home', async () => {
    await page.getByLabel('AgentBox home').first().click();
    await page.waitForTimeout(1500);
    await shot('home');
  });
  await attempt('light', async () => {
    await new Promise((ok, fail) => {
      const req = http.request({ socketPath: socket, path: '/v1/theme', method: 'PATCH', headers: { 'content-type': 'application/json' } }, (res) => {
        res.resume();
        res.on('end', ok);
      });
      req.on('error', fail);
      req.end(JSON.stringify({ appearance: 'light' }));
    });
    await openProject();
    await tab(/Chat/);
    await page.getByText('agent-01 is done').first().waitFor({ timeout: 15_000 });
    await shot('lead-chat-light');
  });

  if (process.env.KEEP) {
    log(`app left open on ${xvfb.display}; state in ${work}. Ctrl-C to stop.`);
    await new Promise(() => {});
  }
  await app.close();
  ab('daemon', 'stop');
  xvfb.stop();
  process.exit(0);
}

main().catch((err) => {
  console.error(err);
  ab('daemon', 'stop');
  process.exit(1);
});
