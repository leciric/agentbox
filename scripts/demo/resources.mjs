#!/usr/bin/env node
// Reproduces capping what an agent's machine may take: the defaults a fresh
// installation seeds, the per-agent choice at creation, changing an agent's
// limits while it runs, the refusal that stops the kernel killing its work, a
// fork inheriting its source's limits, a project base carrying none, and the
// three places in the app where limits are set.
//
//   mise exec -- node scripts/demo/resources.mjs
//
// Real daemon, real command line, real Electron app. The `incus` on PATH is a
// stub, because this runs inside an AgentBox agent and those have no Incus of
// their own (D43) — but a *stateful* one: it keeps each instance's
// configuration, copies it on `incus copy`, and applies `config set`/`config
// unset`, so what an agent ends up capped at here is what AgentBox's own
// commands really produced. Every command it is given is logged, and the log
// is part of the evidence.
//
// Set NO_APP=1 to skip the screenshots.
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const out = process.env.OUT_DIR ?? join(tmpdir(), 'resources-evidence');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-resources-'));
const bin = join(work, 'bin', 'agentbox');
const P = 'hello-stack';
const stubState = join(work, 'stub-state');
const stubLog = join(work, 'incus.log');

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  STUB_STATE: stubState,
  STUB_LOG: stubLog,
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
  console.log(`\n$ agentbox ${args.map((a) => (a === '' ? "''" : /\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${o.trimEnd()}`);
  return o;
}
const git = (dir, ...a) => execFileSync('git', ['-C', dir, ...a], { encoding: 'utf8' }).trim();

// config reads what the stub incus really holds for an instance: the proof
// that AgentBox's commands landed, rather than that it meant to send them.
const config = (instance) => JSON.parse(readFileSync(join(stubState, `${instance}.json`), 'utf8')).config;
const incusCalls = () => readFileSync(stubLog, 'utf8').trim().split('\n');
const limitCalls = () => incusCalls().filter((line) => line.includes('limits.'));

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

// stubIncus is a stateful stand-in for the incus command line. It is written
// out as its own Node program, so it closes over nothing here.
function stubIncus() {
  const fs = require('node:fs');
  const path = require('node:path');
  const state = process.env.STUB_STATE;
  const args = process.argv.slice(2);
  fs.mkdirSync(state, { recursive: true });
  fs.appendFileSync(process.env.STUB_LOG, `${args.join(' ')}\n`);

  const file = (name) => path.join(state, `${name}.json`);
  const read = (name) => {
    try {
      return JSON.parse(fs.readFileSync(file(name), 'utf8'));
    } catch {
      return null;
    }
  };
  const write = (name, inst) => fs.writeFileSync(file(name), JSON.stringify(inst, null, 2));
  const names = () => fs.readdirSync(state).filter((f) => f.endsWith('.json')).map((f) => f.slice(0, -5));
  // An instance's usage, made up but stable: enough for `top` to have numbers
  // and for a memory limit below what it uses to be refusable.
  const usage = (name) => ({
    network: { eth0: { addresses: [{ family: 'inet', address: `10.0.0.${5 + (names().indexOf(name) % 200)}` }] } },
    cpu: { usage: Date.now() * 1e6 },
    memory: { usage: Number(process.env.STUB_MEMORY ?? 5 * 1024 ** 3) },
    processes: 42,
  });
  const full = (name) => {
    const inst = read(name);
    return { name, status: inst.status, config: inst.config, expanded_config: inst.config, devices: inst.devices, state: usage(name) };
  };
  const done = (text) => {
    if (text !== undefined) process.stdout.write(`${text}\n`);
    process.exit(0);
  };
  const fail = (message) => {
    process.stderr.write(`Error: ${message}\n`);
    process.exit(1);
  };

  switch (args[0]) {
    case 'list':
      return done(JSON.stringify(names().map(full)));
    case 'query': {
      const url = args[1];
      if (url.startsWith('/1.0/storage-pools/')) return done(JSON.stringify({ space: { used: 40 * 1024 ** 3, total: 200 * 1024 ** 3 } }));
      const instances = url.match(/^\/1\.0\/instances\/([^/?]+)(\/snapshots)?/);
      if (!instances) return done('{}');
      const [, name, snapshots] = instances;
      const inst = read(name);
      if (snapshots) return done(JSON.stringify((inst?.snapshots ?? []).map((s) => `/1.0/instances/${name}/snapshots/${s}`)));
      if (!inst) return fail(`Instance not found: ${name}`);
      return done(JSON.stringify(full(name)));
    }
    case 'copy': {
      const [source, target] = args.slice(1);
      const from = read(source.split('/')[0]);
      if (!from) return fail(`Instance not found: ${source}`);
      // The behaviour the base scrub exists for: a copy carries the source's
      // configuration keys, limits included.
      write(target, { status: 'Stopped', config: { ...from.config }, devices: { ...from.devices }, snapshots: [] });
      return done();
    }
    case 'config': {
      const [what, ...rest] = args.slice(1);
      if (what === 'set') {
        const [name, ...pairs] = rest;
        const inst = read(name) ?? { status: 'Stopped', config: {}, devices: {}, snapshots: [] };
        for (const pair of pairs) {
          const at = pair.indexOf('=');
          const [key, value] = [pair.slice(0, at), pair.slice(at + 1)];
          if (value === '' && key.startsWith('limits.')) return fail(`Invalid value for config key "${key}": empty`);
          inst.config[key] = value;
        }
        return write(name, inst), done();
      }
      if (what === 'unset') {
        const [name, key] = rest;
        const inst = read(name);
        if (inst) delete inst.config[key], write(name, inst);
        return done();
      }
      if (what === 'device') {
        const [action, name, device] = rest;
        const inst = read(name);
        if (inst && action === 'remove') delete inst.devices[device];
        if (inst && action === 'add') inst.devices[device] = { type: rest[3] };
        if (inst) write(name, inst);
        return done();
      }
      return done();
    }
    case 'snapshot': {
      const [action, name, snapshot] = args.slice(1);
      const inst = read(name);
      if (!inst) return fail(`Instance not found: ${name}`);
      inst.snapshots = action === 'create' ? [...(inst.snapshots ?? []), snapshot] : (inst.snapshots ?? []).filter((s) => s !== snapshot);
      return write(name, inst), done();
    }
    case 'start':
    case 'stop':
    case 'pause':
    case 'resume': {
      const name = args.find((a) => !a.startsWith('-') && a !== args[0]);
      const inst = read(name);
      if (inst) inst.status = { start: 'Running', stop: 'Stopped', pause: 'Frozen', resume: 'Running' }[args[0]];
      if (inst) write(name, inst);
      return done();
    }
    case 'rename': {
      const [from, to] = args.slice(1);
      const inst = read(from);
      if (inst) write(to, inst), fs.rmSync(file(from));
      return done();
    }
    case 'delete': {
      for (const name of args.slice(1).filter((a) => !a.startsWith('-'))) fs.rmSync(file(name), { force: true });
      return done();
    }
    case 'exec':
      // Whatever AgentBox writes into a machine succeeds, and its stdin is
      // drained so incus.WriteFile doesn't block.
      process.stdin.resume();
      process.stdin.on('end', () => process.exit(0));
      process.stdin.on('error', () => process.exit(0));
      return;
    default:
      return done();
  }
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
  mkdirSync(join(work, 'stub'), { recursive: true });
  mkdirSync(stubState, { recursive: true });
  writeFileSync(stubLog, '');
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  writeFileSync(join(work, 'stub', 'incus'), `#!/usr/bin/env node\n${stubIncus.toString()}\nstubIncus();\n`, { mode: 0o755 });
  // The base image the first agent is copied from.
  writeFileSync(join(stubState, 'agentbox-base.json'), JSON.stringify({ status: 'Stopped', config: {}, devices: {}, snapshots: ['ready'] }));

  const repo = join(work, 'repos', P);
  makeRepo(repo);
  ab('add', repo, '--name', P);
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: 'sk-ant-oat01-demo', encoding: 'utf8' });

  const cores = Number(execFileSync('nproc', { encoding: 'utf8' }).trim());

  // 1. A fresh installation caps agents without anyone asking.
  const fresh = (await apiRequest('GET', '/v1/settings')).json;
  console.log(`\nGET /v1/settings on a fresh install:\n${JSON.stringify({ defaultCPU: fresh.defaultCPU, defaultCPUAllowance: fresh.defaultCPUAllowance, defaultMemory: fresh.defaultMemory, hostCores: fresh.hostCores, hostMemory: fresh.hostMemory }, null, 2)}`);
  const expected = String(Math.min(Math.max(2, cores - 2), cores));
  check(`a fresh install caps new agents at ${expected} of this host's ${cores} cores`, fresh.defaultCPU === expected, JSON.stringify(fresh.defaultCPU));
  check('and sets no memory ceiling or CPU share, which would cost more than they save', fresh.defaultMemory === '' && fresh.defaultCPUAllowance === '', JSON.stringify(fresh));
  check('the app is told what the host has, so it can show what a limit is carved out of', fresh.hostCores === cores && fresh.hostMemory > 0, JSON.stringify({ hostCores: fresh.hostCores, hostMemory: fresh.hostMemory }));

  // 2. The default really reaches the machine.
  log('Creating an agent with nobody choosing anything');
  ab('create', P, '--name', 'agent-01', '--ai', 'none', '--title', 'Capped by default');
  const a1 = config(`ab-${P}-agent-01`);
  console.log(`\nab-${P}-agent-01's Incus configuration:\n${JSON.stringify(a1, null, 2)}`);
  check("the agent's machine really has the default core count", a1['limits.cpu'] === expected, JSON.stringify(a1));
  check('and a CPU priority below Incus’ default of 10, so the desktop wins a tie', a1['limits.cpu.priority'] === '5', JSON.stringify(a1));
  const firstSet = limitCalls()[0];
  console.log(`\nthe incus command that did it:\n  incus ${firstSet}`);
  check('set as a count, never a range: a count floats, a range pins every agent to the same cores', !/limits\.cpu=[^ ]*[-,]/.test(firstSet), firstSet);

  // 3. A choice for one agent, including the choice of no limit at all.
  log('Creating an agent with its own limits');
  ab('create', P, '--name', 'agent-02', '--ai', 'none', '--title', 'Chosen', '--cpu', '2', '--memory', '4GiB', '--cpu-allowance', '50%');
  const a2 = config(`ab-${P}-agent-02`);
  check('an agent created with --cpu/--memory/--cpu-allowance gets exactly those', a2['limits.cpu'] === '2' && a2['limits.memory'] === '4GiB' && a2['limits.cpu.allowance'] === '50%', JSON.stringify(a2));

  log('Creating an agent that is deliberately uncapped');
  ab('create', P, '--name', 'agent-03', '--ai', 'none', '--title', 'Uncapped', '--cpu', '');
  const a3 = config(`ab-${P}-agent-03`);
  check('--cpu "" is a choice, not a fallback: the agent gets every core', a3['limits.cpu'] === undefined, JSON.stringify(a3));
  check('...while still losing a contended host to the desktop', a3['limits.cpu.priority'] === '5', JSON.stringify(a3));

  // 4. Values that don't mean what they say are refused where they are typed.
  const pinned = ab('create', P, '--name', 'agent-99', '--ai', 'none', '--cpu', '0-3');
  check('a pinned range is refused, and says why', /pin the agent to those exact cores/.test(pinned), pinned);
  const nonsense = ab('create', P, '--name', 'agent-99', '--ai', 'none', '--memory', 'lots');
  check('so is a memory limit that is not a size', /a size like 8GiB/.test(nonsense), nonsense);

  // 5. Changing an agent's limits while it runs.
  const shown = ab('limits', `${P}/agent-01`);
  check('agentbox limits shows what an agent is capped at', shown.includes(`${expected} cores`) && shown.includes('no memory limit'), shown);
  const before = limitCalls().length;
  const changed = ab('limits', `${P}/agent-01`, '--cpu', '3', '--memory', '12GiB');
  const a1After = config(`ab-${P}-agent-01`);
  check('agentbox limits changes them', a1After['limits.cpu'] === '3' && a1After['limits.memory'] === '12GiB', JSON.stringify(a1After));
  check('and says so in one line', /3 cores, 12GiB of memory/.test(changed), changed);
  const applied = limitCalls().slice(before);
  console.log(`\nthe incus commands the change ran:\n${applied.map((c) => `  incus ${c}`).join('\n')}`);
  check('applied live: one config set, and nothing restarted', applied.length === 1 && applied[0].startsWith('config set'), JSON.stringify(applied));
  check('nothing stopped or started the machine to do it', !incusCalls().slice(-4).some((c) => /^(stop|start|restart) /.test(c)), JSON.stringify(incusCalls().slice(-4)));

  // Removing a limit is an unset, because Incus refuses an empty one — the
  // stub refuses it too, so this would fail if AgentBox sent `limits.cpu=`.
  const uncapped = ab('limits', `${P}/agent-01`, '--cpu', '');
  check('--cpu "" removes the limit', config(`ab-${P}-agent-01`)['limits.cpu'] === undefined && /every core/.test(uncapped), uncapped);

  // 6. The refusal: a memory ceiling below what the agent is already using.
  // The stub reports 5 GiB in use, which is what Incus would report.
  const refused = ab('limits', `${P}/agent-01`, '--memory', '2GiB');
  check('a memory ceiling below what the agent is using is refused', /is using 5\.0 GiB/.test(refused), refused);
  check('...naming what would happen: the kernel would kill its processes', /kill processes inside the agent/.test(refused), refused);
  check('...and what to do instead', /agentbox stop/.test(refused), refused);
  check('nothing was applied on the way to refusing', config(`ab-${P}-agent-01`)['limits.memory'] === '12GiB', JSON.stringify(config(`ab-${P}-agent-01`)));
  const above = ab('limits', `${P}/agent-01`, '--memory', '8GiB');
  check('a ceiling above what it uses goes through', config(`ab-${P}-agent-01`)['limits.memory'] === '8GiB', above);

  // 7. A fork is capped like the agent it came from, not like a new agent.
  log('Forking the agent with its own limits');
  ab('fork', `${P}/agent-02`, '--name', 'agent-04');
  const fork = config(`ab-${P}-agent-04`);
  console.log(`\nthe fork's configuration:\n${JSON.stringify(fork, null, 2)}`);
  check('a fork inherits its source’s limits, not the installation’s defaults', fork['limits.cpu'] === '2' && fork['limits.memory'] === '4GiB' && fork['limits.cpu.allowance'] === '50%', JSON.stringify(fork));

  // 8. A project base carries no limits, so the next agent gets the defaults.
  log('Saving agent-02’s machine as the project base');
  ab('base', 'save', `${P}/agent-02`);
  const base = config(`ab-${P}-base`);
  console.log(`\nthe saved base's configuration:\n${JSON.stringify(base, null, 2)}`);
  check('the base carries none of the limits the agent it was saved from had', !Object.keys(base).some((k) => k.startsWith('limits.')), JSON.stringify(base));
  log('Creating an agent from that base');
  ab('create', P, '--name', 'agent-05', '--ai', 'none', '--title', 'From the base');
  const a5 = config(`ab-${P}-agent-05`);
  check('so an agent made from it gets the installation’s defaults, not the base’s', a5['limits.cpu'] === expected && a5['limits.memory'] === undefined, JSON.stringify(a5));

  // 9. Changing what new agents get.
  const saved = await apiRequest('PATCH', '/v1/settings', { defaultCPU: '3', defaultMemory: '6GiB' });
  check('the installation’s defaults can be changed', saved.status === 200 && saved.json.defaultCPU === '3' && saved.json.defaultMemory === '6GiB', JSON.stringify(saved.json));
  const badDefault = await apiRequest('PATCH', '/v1/settings', { defaultCPU: '0-3' });
  check('a default that would pin every agent to the same cores is refused', badDefault.status >= 400 && /pin the agent/.test(JSON.stringify(badDefault.json)), JSON.stringify(badDefault));
  ab('create', P, '--name', 'agent-06', '--ai', 'none', '--title', 'After the change');
  const a6 = config(`ab-${P}-agent-06`);
  check('the next agent is capped at the new default', a6['limits.cpu'] === '3' && a6['limits.memory'] === '6GiB', JSON.stringify(a6));
  check('agents that already existed keep what they have', config(`ab-${P}-agent-02`)['limits.cpu'] === '2', JSON.stringify(config(`ab-${P}-agent-02`)));
  // A chosen empty default survives a daemon restart: it is a choice, not an
  // unset key waiting to be seeded again.
  await apiRequest('PATCH', '/v1/settings', { defaultCPU: '' });
  ab('daemon', 'stop');
  await sleep(1500);
  ab('list', P); // autostarts a fresh daemon, which seeds again if it can
  const afterRestart = (await apiRequest('GET', '/v1/settings')).json;
  check('a choice of "no CPU limit for new agents" survives a restart, rather than being seeded again', afterRestart.defaultCPU === '', JSON.stringify(afterRestart.defaultCPU));
  await apiRequest('PATCH', '/v1/settings', { defaultCPU: '3' });

  // 10. What an agent has, next to what it is using.
  const top = ab('top');
  check('agentbox top shows CPU against the agent’s own limit and against the host', /OF LIMIT/.test(top) && /OF HOST/.test(top), top);
  check('...and memory as used / limit, naming an agent that has none', /no limit/.test(top), top);
  const agent02 = (await apiRequest('GET', `/v1/agents/${P}/agent-02`)).json;
  check('the API reports an agent’s effective limits', agent02.limits.cpu === '2' && agent02.limits.memory === '4GiB' && agent02.limits.allowance === '50%', JSON.stringify(agent02.limits));

  writeFileSync(join(out, 'incus-calls.log'), incusCalls().join('\n'));
  log(`wrote every incus command this run made to ${join(out, 'incus-calls.log')}`);

  if (!process.env.NO_APP) await shots();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// shots drives the real app through the three places limits are set.
async function shots() {
  log('Starting the app on a private display');
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 1000 });
  try {
    const app = await electron.launch({
      args: ['.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'],
      cwd: desktop,
      env: { ...env, DISPLAY: x.display },
    });
    const page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await sleep(4000);
    const shot = async (name) => {
      await page.screenshot({ path: join(out, `resources-${name}.png`) });
      log('screenshot', `resources-${name}.png`);
    };

    // 1. The overview: what new agents get.
    const cpu = page.locator('[data-setting="default-cpu"]');
    await cpu.scrollIntoViewIfNeeded();
    await sleep(500);
    await shot('01-settings');
    check('the overview has a "Resources for new agents" section', (await cpu.count()) > 0);
    check('the CPU box shows the number that is stored, not the word "default"', (await cpu.inputValue()) === '3', await cpu.inputValue());
    const panel = page.locator('[data-setting="default-cpu"]').locator('xpath=ancestor::div[contains(@class,"panel")]');
    const panelText = await panel.innerText();
    check('with the host’s totals beside them', /cores and/.test(panelText), panelText);
    check('and one line saying what each does', /make -j/.test(panelText) && /killing processes/.test(panelText), panelText);

    await cpu.fill('5');
    await cpu.press('Enter');
    await sleep(1200);
    await shot('02-settings-changed');
    const settings = (await apiRequest('GET', '/v1/settings')).json;
    check('typing a new count on the overview stores it', settings.defaultCPU === '5', JSON.stringify(settings.defaultCPU));

    // 2. The New agent dialog, under More options.
    await page.getByRole('button', { name: /new agent/i }).first().click({ timeout: 15000 });
    await sleep(1200);
    await page.getByRole('button', { name: /more options/i }).click({ timeout: 8000 });
    await sleep(700);
    const dialogCPU = page.locator('#agent-cpu');
    await dialogCPU.scrollIntoViewIfNeeded();
    await sleep(400);
    await shot('03-new-agent-dialog');
    check('the New agent dialog offers the same three, under More options', (await dialogCPU.count()) > 0 && (await page.locator('#agent-memory').count()) > 0 && (await page.locator('#agent-cpu-allowance').count()) > 0);
    check('prefilled with what new agents get', (await dialogCPU.inputValue()) === '5', await dialogCPU.inputValue());
    await dialogCPU.fill('1');
    await sleep(300);
    await shot('04-new-agent-dialog-chosen');
    // Shell only: this is about the limits, and nothing here should wait on a
    // Claude Code session starting.
    await page.locator('[data-ai="none"]').click({ timeout: 8000 });
    await page.getByRole('button', { name: /create agent/i }).click({ timeout: 8000 });
    await sleep(15_000);
    await shot('05-new-agent-created');
    const agents = (await apiRequest('GET', `/v1/agents?project=${P}`)).json;
    check('an agent created from the dialog gets the count typed into it', agents.some((a) => a.limits.cpu === '1'), JSON.stringify(agents.map((a) => [a.name, a.limits.cpu])));

    // 3. The agent's Overview tab: what it has, and an edit that applies live.
    await page.locator(`[data-agent="${P}/agent-02"]`).first().click({ timeout: 15000 });
    await sleep(1500);
    await page.getByRole('tab', { name: 'Overview' }).click({ timeout: 8000 });
    await sleep(1500);
    await shot('06-agent-overview');
    const limits = page.locator('[data-agent-limits]');
    check('an agent’s Overview tab says what its machine is capped at', (await limits.count()) > 0);
    check('naming the share as well as the cores and the memory', /2 cores, 4GiB of memory, a CPU share of 50%/.test(await limits.innerText()), await limits.innerText());

    await page.getByRole('button', { name: /^edit$/i }).click({ timeout: 8000 });
    await sleep(600);
    await shot('07-agent-overview-editing');
    await page.locator('#limits-cpu').fill('7');
    await page.locator('#limits-memory').fill('16GiB');
    await sleep(300);
    await page.getByRole('button', { name: /apply/i }).click({ timeout: 8000 });
    await sleep(2500);
    await shot('08-agent-overview-applied');
    const live = config(`ab-${P}-agent-02`);
    check('editing them in the app reaches the machine', live['limits.cpu'] === '7' && live['limits.memory'] === '16GiB', JSON.stringify(live));
    check('and the agent was not restarted to do it', JSON.parse(readFileSync(join(stubState, `ab-${P}-agent-02.json`), 'utf8')).status === 'Running', 'the machine changed state');

    // The refusal, in the app.
    await page.getByRole('button', { name: /^edit$/i }).click({ timeout: 8000 });
    await sleep(500);
    await page.locator('#limits-memory').fill('1GiB');
    await page.getByRole('button', { name: /apply/i }).click({ timeout: 8000 });
    await sleep(2000);
    await shot('09-agent-overview-refused');
    const refusal = await page.locator('form').filter({ has: page.locator('#limits-memory') }).innerText();
    check('a ceiling below what the agent is using is refused in the app too, in words', /kill processes inside the agent/.test(refusal), refusal);
    check('and nothing was applied', config(`ab-${P}-agent-02`)['limits.memory'] === '16GiB', JSON.stringify(config(`ab-${P}-agent-02`)));

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
