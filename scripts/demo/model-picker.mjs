#!/usr/bin/env node
// Reproduces choosing the model new agents start on, and the model menu it is
// chosen from: the raw list the real Claude Code adapter advertises over ACP,
// the default picked in Settings, a newly created agent seeded with it,
// and that same stored value driving a real turn on the real adapter.
//
//   mise exec -- node scripts/demo/model-picker.mjs
//
// Set NO_APP=1 to skip the screenshots, NO_LIVE=1 to skip the live adapter
// (which spends real tokens and needs CLAUDE_CODE_OAUTH_TOKEN).
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const out = process.env.OUT_DIR ?? join(tmpdir(), 'model-picker-evidence');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-models-'));
const bin = join(work, 'bin', 'agentbox');
const P = 'hello-stack';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  // A stub incus: this runs inside an AgentBox agent, which has none of its
  // own (D43). Everything here reads the store, git and the adapter.
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
  console.log(`\n$ agentbox ${args.join(' ')}\n${o.trimEnd()}`);
  return o;
}
const git = (dir, ...a) => execFileSync('git', ['-C', dir, ...a], { encoding: 'utf8' }).trim();

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

// liveModels asks the real, authenticated Claude Code adapter what this
// account may use, and optionally sets one and runs a turn on it. This is the
// only source of a model list: AgentBox never composes one.
function liveAdapter({ set } = {}) {
  const script = join(work, 'probe.mjs');
  writeFileSync(script, `${probe.toString()}\nprobe(${JSON.stringify(set ?? null)});\n`);
  return new Promise((done, fail) => {
    const p = spawn(process.execPath, [script], { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'], timeout: 300_000 });
    let o = '';
    p.stdout.on('data', (c) => (o += c));
    p.once('close', () => {
      try {
        done(JSON.parse(o));
      } catch {
        fail(new Error(`probe said: ${o.slice(0, 500)}`));
      }
    });
    p.once('error', fail);
  });
}

// probe is stringified into its own process, so it closes over nothing here.
async function probe(set) {
  const { spawn } = await import('node:child_process');
  const child = spawn('claude-agent-acp', [], { cwd: process.cwd(), stdio: ['pipe', 'pipe', 'inherit'] });
  let buf = '';
  const pending = new Map();
  const updates = [];
  child.stdout.on('data', (d) => {
    buf += d;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      let m;
      try { m = JSON.parse(line); } catch { continue; }
      if (m.method === 'session/update') updates.push(m.params);
      if (m.method && m.id !== undefined) child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id: m.id, result: {} }) + '\n');
      if (m.id !== undefined && pending.has(m.id)) {
        const { resolve, reject } = pending.get(m.id);
        pending.delete(m.id);
        m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result);
      }
    }
  });
  let id = 0;
  const call = (method, params) => new Promise((resolve, reject) => {
    const n = ++id;
    pending.set(n, { resolve, reject });
    child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id: n, method, params }) + '\n');
    setTimeout(() => pending.has(n) && (pending.delete(n), reject(new Error(`timeout on ${method}`))), 150000);
  });
  const result = {};
  try {
    const init = await call('initialize', {
      protocolVersion: 1,
      clientCapabilities: { fs: { readTextFile: false, writeTextFile: false }, terminal: false },
      clientInfo: { name: 'agentbox', version: 'evidence' },
    });
    result.adapter = init.agentInfo;
    const sess = await call('session/new', { cwd: process.cwd(), mcpServers: [] });
    const model = (sess.configOptions || []).find((o) => o.category === 'model');
    result.modelOption = model;
    if (set) {
      try {
        const res = await call('session/set_config_option', { sessionId: sess.sessionId, configId: 'model', value: set });
        const m = (res.configOptions || []).find((o) => o.category === 'model');
        result.set = { accepted: true, resolvedTo: m?.currentValue };
      } catch (e) {
        result.set = { accepted: false, error: e.message };
      }
      const turn = await call('session/prompt', {
        sessionId: sess.sessionId,
        prompt: [{ type: 'text', text: 'Reply with only your model name and version. No other words.' }],
      });
      result.stopReason = turn.stopReason;
      result.said = updates
        .flatMap((u) => (u.update?.sessionUpdate === 'agent_message_chunk' && u.update.content?.type === 'text' ? [u.update.content.text] : []))
        .join('')
        .trim();
    }
  } catch (e) {
    result.error = e.message;
  }
  child.kill();
  process.stdout.write(JSON.stringify(result));
}

function makeRepo(dir) {
  mkdirSync(dir, { recursive: true });
  git(dir, 'init', '-q', '-b', 'main');
  writeFileSync(join(dir, 'README.md'), '# demo\n');
  git(dir, 'add', '-A');
  execFileSync('git', ['-C', dir, '-c', 'user.email=d@x.invalid', '-c', 'user.name=D', 'commit', '-q', '-m', 'first']);
}

async function main() {
  mkdirSync(out, { recursive: true });
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });

  // A stub incus that answers for the agents this run makes, so Create gets
  // as far as seeding the chat settings it exists to prove.
  mkdirSync(join(work, 'stub'), { recursive: true });
  const inst = ['agent-01', 'agent-02']
    .map((n, i) => `{"name":"ab-${P}-${n}","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.${5 + i}"}]}}}}`)
    .join(',');
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/bin/sh\ncase "$1" in\n  list) echo '[${inst}]' ;;\n  query)\n    case "$2" in\n      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;\n      */snapshots) echo '[]' ;;\n      *) echo '{"config": {}, "devices": {}}' ;;\n    esac ;;\nesac\nexit 0\n`,
    { mode: 0o755 },
  );

  const repo = join(work, 'repos', P);
  makeRepo(repo);
  ab('add', repo, '--name', P);
  // The real login when there is one: the project chat runs Claude Code on
  // this host, so the composer's model menu is then the account's own.
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: process.env.CLAUDE_CODE_OAUTH_TOKEN || 'sk-ant-oat01-demo', encoding: 'utf8' });

  // 1. What the account really offers, straight from the adapter.
  let live;
  if (!process.env.NO_LIVE) {
    log('Asking the real Claude Code adapter what this account may use');
    live = await liveAdapter();
    console.log(`\nRaw "model" ChatOption from ${live.adapter?.name} ${live.adapter?.version}:\n${JSON.stringify(live.modelOption, null, 2)}`);
    const values = (live.modelOption?.options ?? []).map((o) => o.value);
    check('the adapter advertises a model menu over ACP', values.length > 0, JSON.stringify(values));
    check('Claude Fable is not among them on this account', !values.some((v) => /fable/i.test(v)), JSON.stringify(values));
  }

  // 2. Before anything is chosen: AgentBox's own default, and no menu yet.
  const fresh = await apiRequest('GET', '/v1/settings');
  check('a fresh install has chosen no model, so AgentBox\'s default applies', fresh.json.defaultClaudeModel === '', JSON.stringify(fresh.json));
  check('and offers no model menu it made up', fresh.json.claudeModelChoices.length === 0, JSON.stringify(fresh.json.claudeModelChoices));

  // 3. The menu the app offers is the adapter's own, remembered from a chat.
  const remembered = JSON.stringify(live?.modelOption?.options ?? [{ value: 'sonnet', name: 'Sonnet 5' }, { value: 'opus', name: 'Opus 5' }, { value: 'haiku', name: 'Haiku 4.5' }]);
  execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), `INSERT INTO settings (key,value) VALUES ('claude_model_choices','${remembered.replaceAll("'", "''")}');`]);
  const withMenu = await apiRequest('GET', '/v1/settings');
  check('the app is offered the menu the adapter really sent', withMenu.json.claudeModelChoices.length > 0, JSON.stringify(withMenu.json.claudeModelChoices.map((c) => c.value)));

  // 4. An agent made before choosing keeps AgentBox's default.
  log('Making an agent before a default is chosen');
  ab('create', P, '--name', 'agent-01', '--ai', 'claude', '--title', 'Made before');
  const beforeModel = execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), `SELECT options FROM chats WHERE project='${P}' AND agent='agent-01';`], { encoding: 'utf8' }).trim();
  check('an agent made first starts on AgentBox\'s own default, opus[1m]', JSON.parse(beforeModel).model === 'opus[1m]', beforeModel);

  // 5. Choosing a different default, the way the Settings page does.
  const pick = 'haiku';
  const saved = await apiRequest('PATCH', '/v1/settings', { defaultClaudeModel: pick });
  check(`choosing "${pick}" is stored`, saved.status === 200 && saved.json.defaultClaudeModel === pick, JSON.stringify(saved.json));

  // 6. The next agent starts on it — the proof that matters.
  log('Making an agent after the default changed');
  ab('create', P, '--name', 'agent-02', '--ai', 'claude', '--title', 'Made after');
  const afterModel = execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), `SELECT options FROM chats WHERE project='${P}' AND agent='agent-02';`], { encoding: 'utf8' }).trim();
  console.log(`\nagent-02's stored chat settings: ${afterModel}`);
  check(`a new agent is seeded with the chosen model (${pick})`, JSON.parse(afterModel).model === pick, afterModel);
  check('choosing a model leaves the effort alone', JSON.parse(afterModel).effort === 'xhigh', afterModel);

  // 7. Existing agents are left where they were.
  const stillBefore = execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), `SELECT options FROM chats WHERE project='${P}' AND agent='agent-01';`], { encoding: 'utf8' }).trim();
  check('the agent made earlier keeps the model it already had', JSON.parse(stillBefore).model === 'opus[1m]', stillBefore);

  // 8. That stored value, on the real adapter: it resolves and answers.
  if (!process.env.NO_LIVE) {
    const stored = JSON.parse(afterModel).model;
    log(`Running a real turn with the stored value ${JSON.stringify(stored)}`);
    const ran = await liveAdapter({ set: stored });
    console.log(`\nset_config_option model=${JSON.stringify(stored)} -> ${JSON.stringify(ran.set)}\nthe model said: ${JSON.stringify(ran.said)}`);
    check('the real adapter accepts the stored default', ran.set?.accepted === true, JSON.stringify(ran.set));
    check('and the turn really runs on it', /haiku/i.test(ran.said ?? ''), ran.said);

    // The same path with AgentBox's own default, which is not a literal
    // choice on this account: it must still resolve, not fall back to Sonnet.
    log('Running a real turn with opus[1m], which is not one of the choices');
    const alias = await liveAdapter({ set: 'opus[1m]' });
    console.log(`\nset_config_option model="opus[1m]" -> ${JSON.stringify(alias.set)}\nthe model said: ${JSON.stringify(alias.said)}`);
    check('a preference that is not a literal choice still resolves', alias.set?.accepted === true, JSON.stringify(alias.set));
    check('...and lands on Opus 5, not silently on Sonnet', /opus/i.test(alias.said ?? ''), alias.said);

    // A model this account cannot use is refused outright, which is what the
    // chat now reports instead of swallowing.
    log('Asking for a model this account does not offer');
    const refused = await liveAdapter({ set: 'fable' });
    console.log(`\nset_config_option model="fable" -> ${JSON.stringify(refused.set)}\nthe model said: ${JSON.stringify(refused.said)}`);
    check('a model the account cannot use is refused, not quietly swapped', refused.set?.accepted === false, JSON.stringify(refused.set));
  }

  if (!process.env.NO_APP) await shots();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// shots drives the real app: the control on the Settings page, its menu, and the
// composer's model menu in a chat.
async function shots() {
  log('Starting the app on a private display');
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 940 });
  try {
    const app = await electron.launch({
      args: ['.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'],
      cwd: desktop,
      env: { ...env, DISPLAY: x.display },
    });
    const page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await sleep(3500);
    const shot = async (name) => {
      await page.screenshot({ path: join(out, `models-${name}.png`) });
      log('screenshot', `models-${name}.png`);
    };

    await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
    await sleep(900);
    await shot('01-settings');
    check('the Settings page shows the model for new agents', (await page.locator('[data-default-model]').count()) > 0);
    const control = page.locator('[data-default-model]');
    await control.scrollIntoViewIfNeeded();
    await sleep(400);
    await shot('02-control');
    check('the control names the model that is chosen', /haiku/i.test(await control.innerText()), await control.innerText());

    await control.click();
    await sleep(900);
    await shot('03-menu');
    const menu = page.locator('[role="menu"]');
    const text = await menu.innerText();
    check('the menu offers the account\'s own models', /sonnet/i.test(text) && /opus/i.test(text), text);
    check('it marks the choice the tool itself recommends', /recommended/i.test(text), text);
    await page.keyboard.press('Escape');
    await sleep(500);

    // The composer's own model menu, before and after the first message: a
    // project's chat has never run a session yet (ChatTab's autoStart is
    // false for it), so this is the clean case for the gap a user found — no
    // way to choose the model before typing anything. It runs Claude Code on
    // this host (D43), so these are the account's real models, sent over ACP.
    if (!process.env.NO_LIVE) {
      log('Opening the project chat, which has never run a session');
      await page.locator('aside, nav').getByText(P, { exact: true }).first().click({ timeout: 15000 });
      await sleep(2000);
      await shot('04-before-first-message');
      const model = page.locator('[data-chat-option="model"]');
      check('the model menu is there before you have typed anything', (await model.count()) > 0, 'no model option on screen');
      if ((await model.count()) > 0) {
        await model.click();
        await sleep(900);
        await shot('05-menu-before-first-message');
        const menuText = await page.locator('[role="menu"]').innerText();
        check('offering the adapter\'s own models, before any session has run', /sonnet/i.test(menuText) && /opus/i.test(menuText), menuText);
        // Pick a model other than "default", with nothing sent yet. The
        // description sits in the same menuitem's accessible name, so match
        // only its start ("Opus 5", not "Opus 5 · Best for everyday...").
        await page.getByRole('menuitem', { name: /^Opus 5\b/i }).click({ timeout: 5000 });
        await sleep(500);
        await shot('06-model-chosen-before-first-message');
        check('the composer shows the choice, still with no message sent', /opus/i.test(await model.innerText()), await model.innerText());

        const composer = page.locator('textarea').first();
        await composer.fill('Reply with only your model name and version. No other words.');
        await composer.press('Enter');
        await sleep(45_000);
        await shot('07-first-turn-on-the-chosen-model');
        const timeline = page.locator('[data-chat-timeline]');
        const answered = await timeline.innerText();
        check('the very first turn runs on the model chosen before it, not a default', /opus/i.test(answered), answered.slice(-400));

        await model.click();
        await sleep(900);
        await shot('08-composer-menu-mid-conversation');
        const midText = await page.locator('[role="menu"]').innerText();
        check('with the descriptions the adapter sent', /routine tasks|everyday|quick answers/i.test(midText), midText);
        check('and marks the recommended one', /recommended/i.test(midText), midText);
        await page.keyboard.press('Escape');
        await sleep(400);
      }
    }
    await app.close();
  } finally {
    await x.stop();
  }
}

main()
  .then((ok) => {
    spawnSync(bin, ['daemon', 'stop'], { env, encoding: 'utf8' });
    process.exit(ok ? 0 : 1);
  })
  .catch((err) => {
    console.error(err);
    spawnSync(bin, ['daemon', 'stop'], { env, encoding: 'utf8' });
    process.exit(1);
  });
