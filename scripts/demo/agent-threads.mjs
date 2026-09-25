#!/usr/bin/env node
// Stands a throwaway AgentBox up with a project whose agents have reported
// things — two finished, one asking — and starts the desktop app on it, so the
// rail of agent threads beside the project's chat can be looked at and
// photographed without waiting for real machines to do real work.
//
//   mise exec -- node scripts/demo/agent-threads.mjs
//
// It needs no Incus, no GitHub and no Claude Code login: the state is written
// straight into the throwaway database, and a stub incus says the two machines
// are running. The app is launched on DISPLAY (this machine's virtual desktop
// when AgentBox is running one), and left open until you interrupt it.
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { DatabaseSync } from 'node:sqlite';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const work = mkdtempSync(join(tmpdir(), 'agentbox-threads-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'reminders');
const P = 'reminders';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART']) delete env[name];

const log = (...parts) => console.log('·', ...parts);
const ab = (...args) => spawnSync(bin, args, { env, encoding: 'utf8', timeout: 300_000 });
const git = (dir, ...args) =>
  execFileSync('git', ['-C', dir, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', ...args], { encoding: 'utf8' }).trim();

const now = Date.now();
const ago = (minutes) => now - minutes * 60_000;
const iso = (ms) => new Date(ms).toISOString();
// The agents table keeps seconds; questions and agent_events keep milliseconds.
const seconds = (ms) => Math.floor(ms / 1000);

// The two agents the demo project has, and the machines the stub incus reports.
const agents = [
  { name: 'agent-01', title: 'Paginate the reminders list', instance: `ab-${P}-agent-01` },
  { name: 'agent-02', title: 'Digest email', instance: `ab-${P}-agent-02` },
];

function stubIncus() {
  mkdirSync(join(work, 'stub'), { recursive: true });
  const instances = JSON.stringify(
    agents.map((a) => ({ name: a.instance, status: 'Running', config: {}, expanded_config: {}, state: { network: {} } })),
  );
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/bin/sh\ncase "$1" in\n  list) cat <<'JSON'\n${instances}\nJSON\n  ;;\n  query) echo '{"config": {}, "devices": {}}' ;;\nesac\nexit 0\n`,
    { mode: 0o755 },
  );
}

function makeRepo() {
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# reminders\n\nA small reminders service.\n');
  writeFileSync(join(repo, 'server.mjs'), "import http from 'node:http';\nhttp.createServer((_, res) => res.end('ok')).listen(3000);\n");
  git(repo, 'add', '-A');
  git(repo, 'commit', '-qm', 'first');
}

// chatItem is one entry of the lead's conversation, in the shape package chat
// stores: see api.ChatItem.
const chatItem = (id, turn, kind, text, at, extra = {}) => ({
  id,
  turn,
  kind,
  text,
  createdAt: iso(at),
  updatedAt: iso(at),
  ...extra,
});

const ended = (at) => ({ result: { state: 'completed', stopReason: 'end_turn', endedAt: iso(at) } });

// The lead's conversation: what the user and the lead said, plus the hidden
// pair a finish leaves behind — the notice the lead is told, and the turn it
// starts. Neither shows in the timeline; the lead's answer to it does.
function leadConversation() {
  return [
    chatItem('u1', 'u1', 'user', 'The reminders list is unusable past a hundred rows. Get someone on it.', ago(64), ended(ago(63))),
    chatItem(
      'a1',
      'u1',
      'assistant',
      "I've made **agent-01** for it, on `agentbox/agent-01`, with the task of paginating the list twenty a page and keeping the page in the query string.",
      ago(63),
    ),
    chatItem('n1', 'u1', 'notice', 'agent-01 (Paginate the reminders list) finished. It has changed 4 file(s), +186/-24.', ago(9), { hidden: true }),
    chatItem('u2', 'u2', 'user', 'agent-01 (Paginate the reminders list) finished. It has changed 4 file(s), +186/-24.', ago(9), {
      hidden: true,
      ...ended(ago(8)),
    }),
    chatItem(
      'a2',
      'u2',
      'assistant',
      "agent-01 is done and its pull request is open. I've asked agent-02 to keep the digest email in step with the new page size — it's the only other place that counts reminders.",
      ago(8),
    ),
  ];
}

// The events the rail is made of: each agent created, then finished, and one
// question still waiting for you.
function agentEvents() {
  const ev = (agent, title, kind, at, rest = {}) => ({
    project: P,
    agent,
    ref: `${P}/${agent}`,
    title,
    kind,
    at: iso(at),
    ...rest,
  });
  return [
    ev('agent-01', agents[0].title, 'created', ago(63), { summary: 'Paginate the reminders list, twenty a page, with the page in the query string.' }),
    ev('agent-02', agents[1].title, 'created', ago(40), { summary: 'Keep the digest email in step with the new page size.' }),
    ev('agent-01', agents[0].title, 'finished', ago(9), {
      summary:
        'Added pagination to the reminders list: twenty a page, with the page in the query string so a link to page three is a link to page three.\n\nThe API already took `limit` and `offset`, so nothing changed server side. The two snapshot tests of the old unpaginated list needed rewriting, which is worth a look.',
      changes: { files: 4, insertions: 186, deletions: 24, dirty: false },
      pr: { number: 58, title: 'Paginate the reminders list', state: 'open', checks: 'passing', url: 'https://github.com/leciric/reminders/pull/58' },
    }),
    ev('agent-02', agents[1].title, 'finished', ago(4), {
      summary:
        'The digest email now asks for the same twenty rows the page does, and says "and 14 more" instead of silently cutting the list.\n\nI left the daily send time alone — nothing asked for it and it is covered by a test I would have had to rewrite.',
      changes: { files: 2, insertions: 41, deletions: 9, dirty: true },
    }),
    ev('agent-02', agents[1].title, 'asked', ago(2), { question: 'q1' }),
  ];
}

// openState waits for the daemon to have let go of the database. `daemon stop`
// answers before the process is gone, and SQLite locks are held until it is.
function openState() {
  for (let i = 0; ; i++) {
    try {
      const db = new DatabaseSync(join(work, 'data', 'agentbox', 'state.db'));
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
     VALUES (?, ?, ?, 'claude', 1, ?, 'main', '', ?, 'ready', ?, '', ?, '', 'chat', ?, '', '')`,
  );

  // The lead: an agent row with no machine, whose worktree is the project read
  // on a detached HEAD.
  const leadTree = join(work, 'data', 'agentbox', 'worktrees', P, 'lead');
  mkdirSync(join(work, 'data', 'agentbox', 'worktrees', P), { recursive: true });
  git(repo, 'worktree', 'add', '--quiet', '--detach', leadTree, 'main');
  addAgent.run(P, 'lead', `ab-${P}-lead`, '', leadTree, seconds(ago(70)), 'Project chat', 'lead');

  for (const [i, a] of agents.entries()) {
    const tree = join(work, 'data', 'agentbox', 'worktrees', P, a.name);
    git(repo, 'worktree', 'add', '--quiet', '-b', `agentbox/${a.name}`, tree, 'main');
    addAgent.run(P, a.name, a.instance, `agentbox/${a.name}`, tree, seconds(ago(65 - i * 20)), a.title, 'worker');
  }

  const addItem = db.prepare(`INSERT INTO chat_items (project, agent, id, position, data) VALUES (?, 'lead', ?, ?, ?)`);
  leadConversation().forEach((it, i) => addItem.run(P, it.id, i, JSON.stringify(it)));

  const addEvent = db.prepare(`INSERT INTO agent_events (id, project, agent, created_at, data) VALUES (?, ?, ?, ?, ?)`);
  agentEvents().forEach((ev, i) => addEvent.run(`e${i}`, P, ev.agent, Date.parse(ev.at), JSON.stringify({ id: `e${i}`, ...ev })));

  db.prepare(
    `INSERT INTO questions (id, project, agent, text, context, status, answer, answered_by, escalation, created_at, answered_at)
     VALUES ('q1', ?, 'agent-02', ?, ?, 'escalated', '', '', ?, ?, 0)`,
  ).run(
    P,
    'The digest currently sends at 08:00. Paginating it changes what "today\'s reminders" means for anyone in another timezone. Should I leave the send time alone, or move it to the user\'s local morning?',
    'keeping the digest in step with the paginated list',
    "It changes behaviour for existing users, which isn't mine to decide.",
    ago(2),
  );
  db.close();
}

function main() {
  log('building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  stubIncus();

  log('making a project');
  makeRepo();
  ab('add', repo);
  ab('daemon', 'stop');

  log('seeding what its agents reported');
  seed();

  log('building the app');
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });

  log(`starting the app on DISPLAY=${env.DISPLAY ?? '(none)'}; state is in ${work}`);
  const app = spawnSync('npx', ['electron', '.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'], { cwd: desktop, env, stdio: 'inherit' });
  ab('daemon', 'stop');
  if (!process.env.KEEP) rmSync(work, { recursive: true, force: true });
  process.exit(app.status ?? 0);
}

main();
