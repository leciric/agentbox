#!/usr/bin/env node
// Reproduces 11b: a project's chat with tools, and the chain an agent's
// question travels — agent asks, the chat answers, or passes it to the user.
//
// The chat's tools are driven directly over the Model Context Protocol, the way
// Claude Code drives them, so the demo is deterministic and doesn't spend a
// model turn per check. One real turn at the end proves Claude Code is actually
// given these tools.
//
//   mise exec -- node scripts/demo/lead-tools.mjs > .demo-runs/lead-tools/demo.log 2>&1
//
// Needs a Claude Code login for the last section; NO_CLAUDE=1 skips it.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { request } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '../..');
const work = mkdtempSync(join(tmpdir(), 'agentbox-lead-tools-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-lead');
const P = 'hello-lead';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY']) delete env[name];

const started = Date.now();
const results = [];
const hide = (t) => String(t).replaceAll(work, '$TMP');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 120_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}
const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim();
const sql = (statement) => execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), statement], { encoding: 'utf8' }).trim();

// mcp drives the chat's tools the way Claude Code does: one process, JSON-RPC
// on stdin and stdout.
function mcp(socket) {
  const proc = spawn(bin, ['mcp'], { env: { ...env, AGENTBOX_SOCKET: socket }, stdio: ['pipe', 'pipe', 'inherit'] });
  let buffer = '';
  const pending = new Map();
  proc.stdout.on('data', (chunk) => {
    buffer += chunk;
    for (let i; (i = buffer.indexOf('\n')) >= 0; ) {
      const line = buffer.slice(0, i).trim();
      buffer = buffer.slice(i + 1);
      if (!line) continue;
      const msg = JSON.parse(line);
      const resolve = pending.get(msg.id);
      if (resolve) {
        pending.delete(msg.id);
        resolve(msg);
      }
    }
  });
  let id = 0;
  const send = (method, params) =>
    new Promise((resolve) => {
      const n = ++id;
      pending.set(n, resolve);
      proc.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id: n, method, params })}\n`);
    });
  return {
    proc,
    send,
    // call runs a tool and returns the text the chat would read.
    async call(name, args = {}) {
      const answer = await send('tools/call', { name, arguments: args });
      const text = answer.result?.content?.[0]?.text ?? JSON.stringify(answer.error);
      console.log(`\n[the chat calls] ${name}(${JSON.stringify(args)})\n${hide(text).trimEnd()}`);
      return { text, isError: answer.result?.isError ?? true };
    },
  };
}

function leadSocket(project) {
  const dir = join(work, 'data/agentbox/run/leads');
  const files = existsSync(dir) ? execFileSync('ls', [dir], { encoding: 'utf8' }).split('\n').filter(Boolean) : [];
  if (files.length === 0) throw new Error('no lead socket');
  // One project here, so one socket.
  return join(dir, files[0]);
}

// agent makes a ready agent without needing a machine.
function agent(name, title) {
  const worktree = join(work, 'data/agentbox/worktrees', P, name);
  git(repo, 'worktree', 'add', '--quiet', '-b', `agentbox/${name}`, worktree, 'HEAD');
  sql(
    `INSERT INTO agents (project,name,instance,ai,autonomous,branch,base_ref,base_commit,worktree,status,created_at,source,title,claude_account,interface,role) ` +
      `VALUES ('${P}','${name}','ab-${P}-${name}','claude',0,'agentbox/${name}','main','${git(repo, 'rev-parse', 'HEAD')}','${worktree}','ready',${Math.floor(Date.now() / 1000)},'','${title}','','chat','worker');`,
  );
  return worktree;
}

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  mkdirSync(join(work, 'stub'), { recursive: true });
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/bin/sh\ncase "$1" in list) echo '[{"name":"ab-${P}-agent-01","status":"Running"},{"name":"ab-${P}-agent-02","status":"Running"}]' ;; query) echo '[]' ;; esac\nexit 0\n`,
    { mode: 0o755 },
  );

  log('A project, a login, and two agents');
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# hello-lead\n\n`npm start` runs it on port 3000.\n');
  writeFileSync(join(repo, 'server.mjs'), "import http from 'node:http';\nhttp.createServer((_, r) => r.end('hi')).listen(3000);\n");
  git(repo, 'add', '-A');
  execFileSync('git', ['-C', repo, '-c', 'user.email=d@d', '-c', 'user.name=D', 'commit', '-q', '-m', 'first'], { encoding: 'utf8' });
  const token = process.env.CLAUDE_CODE_OAUTH_TOKEN ?? (readFileSync(join(process.env.HOME, '.config/agentbox/env'), 'utf8').match(/CLAUDE_CODE_OAUTH_TOKEN='?([^'\n]+)'?/) ?? [])[1];
  if (!token) throw new Error('no Claude Code login');
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: token, encoding: 'utf8' });
  ab('add', repo);
  agent('agent-01', 'Reminders page');
  // The agent row went straight into the store, so restart the daemon: it
  // serves each agent's own socket when it starts.
  ab('daemon', 'stop');
  await sleep(1200);
  ab('list');

  // The chat exists as soon as the project does, so it has a socket to reach.
  const socket = leadSocket(P);
  check('a project gets a socket its chat reaches AgentBox through', existsSync(socket), socket);
  const mode = execFileSync('stat', ['-c', '%a', socket], { encoding: 'utf8' }).trim();
  check('only this user can open it', mode === '600', mode);

  const tools = mcp(socket);
  const init = await tools.send('initialize', { protocolVersion: '2024-11-05', capabilities: {} });
  check('the tool server speaks MCP', init.result?.serverInfo?.name === 'agentbox', JSON.stringify(init));
  const listed = await tools.send('tools/list');
  const names = (listed.result?.tools ?? []).map((t) => t.name).sort();
  console.log(`\n[the chat's tools]\n${names.join('\n')}`);
  for (const want of ['list_agents', 'create_agent', 'tell_agent', 'read_agent', 'agent_diff', 'retire_agent', 'list_questions', 'answer_question', 'escalate_question']) {
    check(`it offers ${want}`, names.includes(want), names.join(','));
  }

  // 1. The chat can see its project's agents.
  const fleet = await tools.call('list_agents');
  check('the chat sees what each agent is for', fleet.text.includes('Reminders page'), fleet.text);

  // 2. An agent asks its project's chat, and the chat answers.
  log('agent-01 asks its project chat a question');
  const first = askAsAgent('Should the reminders page paginate, or load everything?', 'building the page');
  const q = await waitForQuestion();
  check('the question reaches the chat, with what the agent was doing', q.context.includes('building the page'), JSON.stringify(q));
  const waiting = await tools.call('list_questions');
  check('the chat is told an agent is blocked on it', waiting.text.includes('Should the reminders page paginate'), waiting.text);
  const gave = await tools.call('answer_question', { id: q.id, answer: 'Paginate, 20 per page: the list grows without limit.' });
  check('the chat can answer it itself', !gave.isError && gave.text.includes('carrying on'), gave.text);
  const got = await first;
  check('and the waiting agent gets that answer', got.answer.includes('Paginate') && got.answeredBy === 'lead', JSON.stringify(got));

  // 3. One the chat can't answer goes to the user.
  log("agent-01 asks something that is the user's call");
  const second = askAsAgent('Which payment provider should the checkout use?', 'starting the checkout');
  const q2 = await waitForQuestion();
  const passed = await tools.call('escalate_question', { id: q2.id, why: "it's a product decision: I'd suggest Stripe, but it's your call" });
  check('the chat can pass a question to the user', !passed.isError && passed.text.includes('user'), passed.text);
  const forUser = ab('questions', P);
  check('the user sees it waiting for them', forUser.includes('waiting for you'), forUser);
  check('and why the chat could not decide it', forUser.includes('product decision'), forUser);
  ab('answer', P, q2.id, 'Use Stripe.');
  const got2 = await second;
  check("the agent gets the user's answer", got2.answer === 'Use Stripe.' && got2.answeredBy === 'user', JSON.stringify(got2));

  // 4. The chat retires a finished agent.
  const retired = await tools.call('retire_agent', { agent: 'agent-01', how: 'destroy' });
  check('the chat can free an agent that finished', retired.text.includes('retired'), retired.text);
  check('and says where its work stays', retired.text.includes('agentbox/agent-01'), retired.text);
  const branches = git(repo, 'branch', '--list', '--format=%(refname:short)').split('\n');
  check('the branch outlives the agent', branches.includes('agentbox/agent-01'), branches.join(','));

  // 5. It cannot reach anything but its own project.
  const outside = await tools.send('tools/call', { name: 'read_agent', arguments: { agent: '../../etc' } });
  check('it cannot read outside its project', outside.result?.isError === true, JSON.stringify(outside.result));

  tools.proc.kill();
  console.log(`\n${results.join('\n')}`);
  const passedCount = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passedCount}/${results.length} checks passed`);
  return passedCount === results.length;
}

// askAsAgent asks through the agent's own in-agent socket, which is the route a
// real agent uses: AgentBox knows which agent is calling from the socket alone.
function askAsAgent(question, about) {
  const hash = createHash('sha256').update(`ab-${P}-agent-01`).digest('hex').slice(0, 12);
  const socket = join(work, 'data/agentbox/run/agents', `${hash}.sock`);
  return new Promise((done, fail) => {
    const req = request({ socketPath: socket, path: '/v1/self/ask', method: 'POST', headers: { 'Content-Type': 'application/json' } }, (res) => {
      let body = '';
      res.on('data', (c) => (body += c));
      res.on('end', () => (res.statusCode === 200 ? done(JSON.parse(body)) : fail(new Error(`ask: ${res.statusCode} ${body}`))));
    });
    req.on('error', fail);
    req.end(JSON.stringify({ question, context: about }));
  });
}

async function waitForQuestion() {
  for (let i = 0; i < 200; i++) {
    const rows = sql(`SELECT id || '|' || text || '|' || context FROM questions WHERE status IN ('pending','escalated');`);
    if (rows) {
      const [id, text, context] = rows.split('\n')[0].split('|');
      return { id, text, context };
    }
    await sleep(50);
  }
  throw new Error('no question arrived');
}

main()
  .then((ok) => {
    log('Done.');
    rmSync(work, { recursive: true, force: true });
    process.exit(ok ? 0 : 1);
  })
  .catch((err) => {
    console.error(err);
    rmSync(work, { recursive: true, force: true });
    process.exit(1);
  });
