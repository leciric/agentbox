#!/usr/bin/env node
// Reproduces choosing an agent's model, effort and permissions when it is
// created — through the project chat's MCP tool, the command line and the app's
// New agent dialog — and the fallback behind them: what you chose, then what
// new agents start on, then AgentBox's own default, field by field.
//
//   mise exec -- node scripts/demo/agent-options.mjs
//
// Real daemon, real lead socket, real `agentbox mcp`, real Electron app, real
// Claude Code adapter; a stub incus, because this runs inside an AgentBox agent
// and those have no Incus of their own (D43).
//
// An agent's own chat runs inside its machine, which doesn't exist here, so the
// turns that prove a stored setting really drives a session are run through the
// one chat that does run on this host: the project's own. Its chat row is given
// the settings an agent was created with, which is exactly what that agent's
// machine would have read. Every such step says so where it happens.
//
// Set NO_APP=1 to skip the app, NO_LIVE=1 to skip everything that spends a real
// turn (which needs a Claude Code login).
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const out = process.env.OUT_DIR ?? join(tmpdir(), 'agent-options-evidence');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-options-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-stack');
const P = 'hello-stack';
const db = join(work, 'data/agentbox/state.db');

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY']) delete env[name];

const started = Date.now();
const results = [];
const hide = (t) => String(t).replaceAll(work, '$TMP');
const log = (...p) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...p.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 300_000 });
  const o = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${o.trimEnd()}`);
  return o;
}
const git = (dir, ...a) => execFileSync('git', ['-C', dir, ...a], { encoding: 'utf8' }).trim();
// sql retries while the store is locked. `agentbox daemon stop` returns before
// the process has let go of the database file, and a daemon shutting down with
// a turn still draining (D47) can hold it for a moment longer, so a write that
// follows a stop has to wait for the handle rather than assume it is free.
function sql(statement) {
  for (let attempt = 0; ; attempt++) {
    try {
      return execFileSync('sqlite3', [db, statement], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
    } catch (err) {
      const locked = `${err.stderr ?? ''}`.includes('database is locked');
      if (!locked || attempt >= 40) throw err;
      execFileSync('sleep', ['0.25']);
    }
  }
}
// options reads what an agent's chat was actually seeded with.
const options = (agent) => JSON.parse(sql(`SELECT options FROM chats WHERE project='${P}' AND agent='${agent}';`) || '{}');
const autonomousFlag = (agent) => sql(`SELECT autonomous FROM agents WHERE project='${P}' AND name='${agent}';`);

function apiRequest(method, path, body) {
  return new Promise((done, fail) => {
    const data = body ? JSON.stringify(body) : undefined;
    const req = http.request(
      {
        socketPath: join(work, 'data/agentbox/run/agentbox.sock'),
        path,
        method,
        headers: data ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) } : {},
      },
      (res) => {
        let o = '';
        res.on('data', (c) => (o += c));
        res.on('end', () => done({ status: res.statusCode, json: o ? JSON.parse(o) : undefined }));
      },
    );
    req.on('error', fail);
    if (data) req.write(data);
    req.end();
  });
}

// mcp drives the project chat's tools the way Claude Code does.
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
    async call(name, args = {}) {
      const answer = await send('tools/call', { name, arguments: args });
      const text = answer.result?.content?.[0]?.text ?? JSON.stringify(answer.error);
      console.log(`\n[the chat calls] ${name}(${JSON.stringify(args)})\n${hide(text).trimEnd()}`);
      return { text, isError: answer.result?.isError ?? true };
    },
  };
}

function leadSocket() {
  const dir = join(work, 'data/agentbox/run/leads');
  const files = existsSync(dir) ? execFileSync('ls', [dir], { encoding: 'utf8' }).split('\n').filter(Boolean) : [];
  if (files.length === 0) throw new Error('no lead socket');
  return join(dir, files[0]);
}

// ---------------------------------------------------------------- the lead
// An agent's chat runs inside its machine. There is none here, so the project's
// own chat — which runs on this host — stands in for one: it is given the chat
// settings an agent was created with, and the permission flag chosen for it.
// Everything below this line is AgentBox's own chat code driving the real
// adapter, not a probe alongside it.

async function chatThread() {
  return (await apiRequest('GET', `/v1/projects/${P}/chat`)).json;
}

// asAgent points the project's chat at one agent's stored settings, then
// restarts the daemon so nothing cached survives.
async function asAgent(agent, { autonomous } = {}) {
  const stored = JSON.stringify(options(agent)).replaceAll("'", "''");
  ab('daemon', 'stop');
  // Wait for the daemon to really be gone, not just for stop to have returned.
  for (let i = 0; i < 60 && existsSync(join(work, 'data/agentbox/run/agentbox.sock')); i++) await sleep(250);
  sql(`INSERT INTO chats (project,agent,session_id,options) VALUES ('${P}','lead','','${stored}')
       ON CONFLICT(project,agent) DO UPDATE SET session_id='', options=excluded.options;`);
  sql(`DELETE FROM chat_items WHERE project='${P}' AND agent='lead';`);
  if (autonomous !== undefined) sql(`UPDATE agents SET autonomous=${autonomous ? 1 : 0} WHERE project='${P}' AND name='lead';`);
  // The socket goes with the daemon and comes back with the next one, so wait
  // for a daemon that really answers rather than writing into the one that
  // just left. A stop that raced an in-flight session gets another go.
  for (let attempt = 0; attempt < 3; attempt++) {
    ab('list');
    for (let i = 0; i < 40; i++) {
      try {
        if ((await apiRequest('GET', '/v1/version')).status === 200) return options(agent);
      } catch {
        /* not up yet */
      }
      await sleep(500);
    }
    ab('daemon', 'stop');
    await sleep(2000);
  }
  throw new Error('the daemon would not come back up');
}

// say sends one message and waits for the turn to end, or for the tool to stop
// and ask permission — which is the thing being measured, so it counts as done.
async function say(text, { timeout = 240_000 } = {}) {
  const sent = await apiRequest('POST', `/v1/projects/${P}/chat/messages`, { text });
  const turn = sent.json?.id;
  const deadline = Date.now() + timeout;
  let thread;
  while (Date.now() < deadline) {
    thread = await chatThread();
    const items = thread?.items ?? [];
    const waiting = items.some((i) => i.kind === 'permission' && !i.permission?.outcome);
    const ended = items.some((i) => i.id === turn && i.result);
    if (waiting || ended) break;
    await sleep(1500);
  }
  const said = (thread?.items ?? [])
    .filter((i) => i.kind === 'assistant')
    .map((i) => i.text)
    .join('\n')
    .trim();
  console.log(`\n[the chat says] ${hide(said).slice(0, 400)}`);
  return { thread, said };
}

function makeRepo(dir) {
  mkdirSync(dir, { recursive: true });
  git(dir, 'init', '-q', '-b', 'main');
  writeFileSync(join(dir, 'README.md'), '# demo\n');
  git(dir, 'add', '-A');
  execFileSync('git', ['-C', dir, '-c', 'user.email=d@x.invalid', '-c', 'user.name=D', 'commit', '-q', '-m', 'first']);
}

function claudeToken() {
  if (process.env.CLAUDE_CODE_OAUTH_TOKEN) return process.env.CLAUDE_CODE_OAUTH_TOKEN;
  const file = join(process.env.HOME, '.config/agentbox/env');
  if (!existsSync(file)) return '';
  return (readFileSync(file, 'utf8').match(/CLAUDE_CODE_OAUTH_TOKEN='?([^'\n]+)'?/) ?? [])[1] ?? '';
}

async function main() {
  mkdirSync(out, { recursive: true });
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });

  // A stub incus that answers for every agent this run makes, so Create gets as
  // far as seeding the chat settings this is about.
  mkdirSync(join(work, 'stub'), { recursive: true });
  const names = ['agent-01', 'agent-02', 'agent-03', 'agent-04', 'agent-05', 'agent-06', 'agent-07', 'agent-08'];
  const inst = names
    .map((n, i) => `{"name":"ab-${P}-${n}","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.${5 + i}"}]}}}}`)
    .join(',');
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/bin/sh\ncase "$1" in\n  list) echo '[${inst}]' ;;\n  query)\n    case "$2" in\n      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;\n      */snapshots) echo '[]' ;;\n      *) echo '{"config": {}, "devices": {}}' ;;\n    esac ;;\nesac\nexit 0\n`,
    { mode: 0o755 },
  );

  makeRepo(repo);
  ab('add', repo, '--name', P);
  const token = claudeToken();
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: token || 'sk-ant-oat01-demo', encoding: 'utf8' });
  const live = !process.env.NO_LIVE && !!token;
  if (!live) log('No Claude Code login (or NO_LIVE=1): the real turns are skipped');

  // 1. Nothing chosen anywhere: AgentBox's own defaults, and no menu invented.
  const fresh = (await apiRequest('GET', '/v1/settings')).json;
  check('a fresh install has chosen no model and no effort', fresh.defaultClaudeModel === '' && fresh.defaultClaudeEffort === '', JSON.stringify(fresh));
  check('and offers no menu it made up', fresh.claudeModelChoices.length === 0 && fresh.claudeEffortChoices.length === 0, JSON.stringify(fresh));

  log('An agent made with nothing chosen at all');
  ab('create', P, '--name', 'agent-01', '--ai', 'claude', '--title', 'Nothing chosen');
  const plain = options('agent-01');
  check("with no setting, an agent takes AgentBox's built-in default", plain.model === 'opus[1m]' && plain.effort === 'xhigh', JSON.stringify(plain));

  // 2. The menus, remembered from a real session rather than composed here.
  if (live) {
    log('Starting the project chat once, so Claude Code sends its own menus');
    await apiRequest('POST', `/v1/projects/${P}/chat/start`, {});
    for (let i = 0; i < 40; i++) {
      const s = (await apiRequest('GET', '/v1/settings')).json;
      if (s.claudeModelChoices.length && s.claudeEffortChoices.length) break;
      await sleep(1000);
    }
  } else {
    // Shaped like the menus the adapter really sends, names and all, so the
    // app is exercised on the same thing a live run gives it.
    const models = '[{"value":"default","name":"Default (recommended)"},{"value":"opus[1m]","name":"Opus 5"},{"value":"sonnet","name":"Sonnet 5"},{"value":"haiku","name":"Haiku 4.5"}]';
    const efforts = '[{"value":"default","name":"Default"},{"value":"low","name":"Low"},{"value":"medium","name":"Medium"},{"value":"high","name":"High"},{"value":"xhigh","name":"Xhigh"},{"value":"max","name":"Max"}]';
    sql(`INSERT INTO settings (key,value) VALUES ('claude_model_choices','${models}'),('claude_effort_choices','${efforts}');`);
  }
  const menus = (await apiRequest('GET', '/v1/settings')).json;
  console.log(`\nremembered menus:\n  models : ${menus.claudeModelChoices.map((c) => c.value).join(', ')}\n  efforts: ${menus.claudeEffortChoices.map((c) => c.value).join(', ')}`);
  check('the effort levels are remembered the way the models are', menus.claudeEffortChoices.length > 0, JSON.stringify(menus.claudeEffortChoices));
  check('and they are the adapter\'s own, not a list AgentBox holds', menus.claudeEffortChoices.some((c) => c.value === 'xhigh'), JSON.stringify(menus.claudeEffortChoices.map((c) => c.value)));

  // 3. The settings for new agents, and an agent that takes them.
  const set = await apiRequest('PATCH', '/v1/settings', { defaultClaudeModel: 'sonnet', defaultClaudeEffort: 'low' });
  check('the Settings page can choose both', set.status === 200 && set.json.defaultClaudeModel === 'sonnet' && set.json.defaultClaudeEffort === 'low', JSON.stringify(set.json));
  log('An agent made with nothing chosen for it, but settings set');
  ab('create', P, '--name', 'agent-02', '--ai', 'claude', '--title', 'Takes the settings');
  const took = options('agent-02');
  check('with nothing chosen for it, an agent takes the settings', took.model === 'sonnet' && took.effort === 'low', JSON.stringify(took));

  // 4. The command line, field by field: what is chosen wins, what is not falls back.
  log('The command line, choosing a model only');
  ab('create', P, '--name', 'agent-03', '--ai', 'claude', '--title', 'Model only', '--model', 'haiku');
  const cliModel = options('agent-03');
  check('--model wins over the setting, and leaves the effort on it', cliModel.model === 'haiku' && cliModel.effort === 'low', JSON.stringify(cliModel));

  log('The command line, choosing an effort only, and asking first');
  ab('create', P, '--name', 'agent-04', '--ai', 'claude', '--title', 'Effort only', '--effort', 'max', '--autonomous=false');
  const cliEffort = options('agent-04');
  check('--effort wins on its own, and leaves the model on the setting', cliEffort.model === 'sonnet' && cliEffort.effort === 'max', JSON.stringify(cliEffort));
  check('--autonomous=false is stored as a real choice, not lost as a zero value', autonomousFlag('agent-04') === '0', autonomousFlag('agent-04'));

  // 5. The project chat's own tool: what a lead can choose.
  const tools = mcp(leadSocket());
  await tools.send('initialize', { protocolVersion: '2024-11-05', capabilities: {} });
  const listed = await tools.send('tools/list');
  const create = (listed.result?.tools ?? []).find((t) => t.name === 'create_agent');
  const props = create?.inputSchema?.properties ?? {};
  console.log(`\n[create_agent's new parameters]\n${JSON.stringify({ model: props.model, effort: props.effort, permissions: props.permissions }, null, 2)}`);
  check('the chat can choose a model, an effort and permissions', !!(props.model && props.effort && props.permissions), Object.keys(props).join(','));
  check('the model parameter names this account\'s own models, not a list AgentBox invented', /haiku/.test(props.model?.description ?? ''), props.model?.description);
  check('it says what happens when the model is left out', /leave this out/i.test(props.model?.description ?? '') && /settings/.test(props.model?.description ?? ''), props.model?.description);
  check('and that a model it will not accept fails rather than working', /refused|won't accept/i.test(props.model?.description ?? ''), props.model?.description);
  check('the effort parameter offers the levels the adapter advertised', (props.effort?.enum ?? []).includes('xhigh'), JSON.stringify(props.effort?.enum));
  check('permissions is the two things it can be', JSON.stringify(props.permissions?.enum) === '["autonomous","ask"]', JSON.stringify(props.permissions?.enum));

  log('The project chat creates an agent with a model, an effort and permissions');
  const made = await tools.call('create_agent', {
    title: 'Rename a config key',
    task: 'Rename the timeout key in config.json to requestTimeoutMs, and update every use.',
    model: 'haiku',
    effort: 'low',
    permissions: 'ask',
  });
  check('the tool creates it', !made.isError && made.text.includes('Creating an agent'), made.text);
  check('and tells the chat what it chose, rather than leaving it to be found later', made.text.includes('on haiku') && made.text.includes('low effort') && made.text.includes('stopping to ask'), made.text);
  await sleep(4000);
  const viaMCP = options('agent-05');
  check('the agent the chat made starts on the model it chose', viaMCP.model === 'haiku', JSON.stringify(viaMCP));
  check('at the effort it chose', viaMCP.effort === 'low', JSON.stringify(viaMCP));
  check('and asks before it changes anything, as the chat asked', autonomousFlag('agent-05') === '0', autonomousFlag('agent-05'));

  log('The project chat creates one with nothing chosen');
  const plainMCP = await tools.call('create_agent', { title: 'Small fix', task: 'Fix the typo in the README heading.' });
  check('a tool call that chooses nothing still works', !plainMCP.isError, plainMCP.text);
  await sleep(4000);
  const viaMCPPlain = options('agent-06');
  check('and that agent takes the settings, not the last agent\'s choices', viaMCPPlain.model === 'sonnet' && viaMCPPlain.effort === 'low', JSON.stringify(viaMCPPlain));
  check('while staying autonomous, which is what the chat has always made', autonomousFlag('agent-06') === '1', autonomousFlag('agent-06'));

  // 6. What is refused, and where.
  log('Choices that cannot apply');
  const badEffort = await tools.call('create_agent', { title: 'Bad effort', task: 'x', effort: 'ultra' });
  check('an effort Claude Code has never offered is refused when the agent is made', badEffort.isError && /ultra/.test(badEffort.text), badEffort.text);
  check('and the refusal says what it does offer', /xhigh/.test(badEffort.text), badEffort.text);

  const badPermissions = await tools.call('create_agent', { title: 'Bad permissions', task: 'x', permissions: 'sometimes' });
  check('a permission mode that is not one of the two is refused', badPermissions.isError, badPermissions.text);

  const codex = ab('create', P, '--name', 'agent-99', '--ai', 'codex', '--title', 'Codex', '--model', 'haiku');
  check('a Claude Code model asked for on a Codex agent is refused, not stored unread', /Claude Code settings/.test(codex), codex);
  const none = ab('create', P, '--name', 'agent-99', '--ai', 'none', '--title', 'Shell only', '--effort', 'low');
  check('and on a shell-only agent too', /no AI tool/.test(none), none);

  const badSetting = await apiRequest('PATCH', '/v1/settings', { defaultClaudeEffort: 'ultra' });
  check('the setting for new agents refuses an unknown effort too', badSetting.status === 400, JSON.stringify(badSetting.json));

  // A model is deliberately not pre-checked: the adapter resolves a preference
  // itself, so a literal check would reject the aliases that do work (D45).
  // It is stored as asked, and fails later where the account's own answer is.
  const badModel = await tools.call('create_agent', { title: 'Bad model', task: 'x', model: 'gpt-4o' });
  check('a model is stored as asked, with no menu check to reject a working alias', !badModel.isError, badModel.text);
  await sleep(4000);
  const storedBad = options('agent-07');
  check('and it is stored exactly as asked, not quietly swapped for something else', storedBad.model === 'gpt-4o', JSON.stringify(storedBad));

  tools.proc.kill();

  // 7. The app's New agent dialog.
  if (!process.env.NO_APP) await dialog();

  // 8. The turns: a stored choice really driving a real session.
  if (live) await runsOnThem(storedBad);

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  console.log(`screenshots in ${out}`);
  return passed === results.length;
}

// dialog drives the real app: the New agent dialog's model and effort, and the
// two controls the Settings page holds for what new agents start on.
async function dialog() {
  log('Starting the app on a private display');
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 940 });
  let app;
  try {
    app = await electron.launch({
      args: ['.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'],
      cwd: desktop,
      env: { ...env, DISPLAY: x.display },
    });
    const page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await sleep(3500);
    const shot = async (name) => {
      await page.screenshot({ path: join(out, `options-${name}.png`) });
      log('screenshot', `options-${name}.png`);
    };

    await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
    await sleep(900);
    const effortControl = page.locator('[data-default-effort]');
    await effortControl.scrollIntoViewIfNeeded();
    await sleep(400);
    await shot('01-settings-what-new-agents-start-on');
    check('Settings chooses the effort new agents start on, beside the model', await effortControl.isVisible(), '');
    check('and shows the one that is chosen', (await effortControl.inputValue()) === 'low', await effortControl.inputValue());

    await page.getByRole('button', { name: 'New agent' }).first().click();
    await sleep(900);
    await page.getByRole('button', { name: 'More options' }).click();
    await sleep(600);
    await shot('02-dialog-more-options');

    const model = page.locator('#agent-model');
    const effort = page.locator('#agent-effort');
    check('the dialog offers a model', await model.isVisible(), '');
    check('and an effort', await effort.isVisible(), '');
    const fallback = await model.locator('option').first().textContent();
    check('with what it falls back to named, so nothing is chosen blind', /Sonnet/i.test(fallback ?? ''), fallback ?? '');
    const offered = await model.locator('option').allTextContents();
    check('offering the account\'s own models', offered.some((t) => /Haiku/i.test(t)), offered.join(' | '));

    await page.locator('#agent-title').fill('From the dialog');
    await page.locator('#agent-name').fill('agent-08');
    await model.selectOption('haiku');
    await effort.selectOption('max');
    await sleep(400);
    await shot('03-dialog-model-and-effort-chosen');

    await page.getByRole('button', { name: 'Create agent' }).click();
    await sleep(9000);
    await shot('04-created');
    const fromDialog = options('agent-08');
    check('the agent the dialog made starts on what was picked in it', fromDialog.model === 'haiku' && fromDialog.effort === 'max', JSON.stringify(fromDialog));

    // A Codex agent is offered neither, rather than being shown a choice that
    // would be refused.
    await page.getByRole('button', { name: 'New agent' }).first().click();
    await sleep(900);
    await page.locator('[data-ai="codex"]').click();
    await page.getByRole('button', { name: 'More options' }).click();
    await sleep(700);
    await shot('05-codex-has-no-model');
    check('a Codex agent is offered no Claude Code settings at all', !(await page.locator('#agent-model').isVisible()), '');
  } finally {
    await app?.close().catch(() => {});
    await x.stop();
  }
}

// runsOnThem proves a setting stored at creation really drives a session: the
// model and effort an agent was made with, the permission mode its flag asks
// for, and the loud failure when the model is one the account cannot run.
//
// The agent's own machine doesn't exist here (D43), so the project's chat —
// the one that runs on this host — is given that agent's stored row and its
// permission flag. From there on it is AgentBox's own chat code and the real
// adapter, with no probe in between.
async function runsOnThem(storedBad) {
  log("Running an agent's stored settings through a real session");
  const haiku = await asAgent('agent-05');
  const first = await say('Reply with only your model name and version. No other words.');
  const applied = Object.fromEntries((first.thread?.session?.options ?? []).map((o) => [o.id, o.value]));
  console.log(`\nstored at creation: ${JSON.stringify(haiku)}\nthe live session reports: ${JSON.stringify(applied)}`);
  check('the model stored at creation is the one the session runs on', applied.model === haiku.model, JSON.stringify(applied));
  check('and the turn really answers as that model, not as a default', /haiku/i.test(first.said), first.said);
  // The caveat the design rests on, live: the adapter sends the effort levels
  // "available for this model", and Haiku 4.5 sends none. So the effort stored
  // with this agent has nothing to apply to — which is why an effort chosen at
  // creation is checked against the levels Claude Code has *ever* named rather
  // than promised to any one model.
  check('a model with no effort levels offers none, so the stored effort simply does not apply', applied.effort === undefined, JSON.stringify(applied));

  log('And an agent whose model does have effort levels');
  const sonnet = await asAgent('agent-04');
  const second = await say('Reply with only your model name and version. No other words.');
  const both = Object.fromEntries((second.thread?.session?.options ?? []).map((o) => [o.id, o.value]));
  console.log(`\nstored at creation: ${JSON.stringify(sonnet)}\nthe live session reports: ${JSON.stringify(both)}`);
  check('the effort stored at creation is the one the session runs at', both.effort === sonnet.effort, JSON.stringify(both));
  check('alongside the model stored with it', both.model === sonnet.model, JSON.stringify(both));
  check('and that turn answers as that model', /sonnet/i.test(second.said), second.said);

  // The permission flag chosen at creation, both ways. agent-04 was made with
  // --autonomous=false, which is the flag being read here.
  log('An agent made to ask first');
  await asAgent('agent-04', { autonomous: false });
  const asked = await say("List this project's agents. Use the tool, and say nothing else.");
  const askedMode = Object.fromEntries((asked.thread?.session?.options ?? []).map((o) => [o.id, o.value])).mode;
  const waiting = (asked.thread?.items ?? []).find((i) => i.kind === 'permission' && !i.permission?.outcome);
  console.log(`\nsession mode: ${askedMode}\npermission asked for: ${JSON.stringify(waiting?.permission?.title)}`);
  check('an agent made to ask first is left on the tool\'s asking mode', askedMode === 'default', askedMode);
  check('and really stops to ask before it changes anything', !!waiting, JSON.stringify(asked.thread?.items?.map((i) => i.kind)));
  check('with something to answer, rather than a dead end', (waiting?.permission?.options ?? []).length > 0, JSON.stringify(waiting?.permission?.options));

  log('And the same agent made autonomous instead');
  await asAgent('agent-04', { autonomous: true });
  const free = await say("List this project's agents. Use the tool, and say nothing else.");
  const freeMode = Object.fromEntries((free.thread?.session?.options ?? []).map((o) => [o.id, o.value])).mode;
  const stopped = (free.thread?.items ?? []).some((i) => i.kind === 'permission' && !i.permission?.outcome);
  console.log(`\nsession mode: ${freeMode}`);
  check('an agent made autonomous runs on full access instead', freeMode === 'bypassPermissions', freeMode);
  check('and never stops to ask', !stopped, JSON.stringify(free.thread?.items?.map((i) => i.kind)));

  // The model that was stored without a pre-check: it fails where the account's
  // own answer is, rather than the agent quietly running on something else.
  //
  // Which failure you get depends on how far the model gets. Since the stored
  // model is written into the tool's own configuration before the adapter
  // starts (D46), a model this account cannot run now reaches the tool and is
  // rejected by it, so the turn fails naming the model, instead of
  // set_config_option refusing it and AgentBox reporting the swap. Both are
  // visible and both name the model; this one doesn't run on a substitute at
  // all, so there is no "instead" to report.
  log('The model this account cannot run, stored at creation');
  await asAgent('agent-07', { autonomous: true });
  const bad = await say('Say hi.');
  const items = bad.thread?.items ?? [];
  const named = items.filter((i) => i.text?.includes(storedBad.model));
  const failed = items.some((i) => i.kind === 'error') || items.some((i) => i.result?.state === 'failed');
  console.log(`\nhow it failed: ${JSON.stringify(named.map((i) => `${i.kind}: ${i.text?.slice(0, 120)}`), null, 1)}`);
  check('a model the account cannot run fails visibly, naming it', named.length > 0, JSON.stringify(items.map((i) => `${i.kind}:${i.text?.slice(0, 60)}`)));
  check('and the turn fails rather than answering as a model nobody chose', failed, JSON.stringify(items.map((i) => i.kind)));
}

main().then(
  (ok) => process.exit(ok ? 0 : 1),
  (err) => {
    console.error(err);
    process.exit(1);
  },
);
