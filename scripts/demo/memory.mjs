#!/usr/bin/env node
// Reproduces the memory review: the memory `agentbox top` and the app show for
// an agent, before and after the fix, next to what `free` reports inside the
// agent. The agent holds 512 MiB and reads a 2 GiB file, which the kernel keeps
// in its page cache; before the fix, AgentBox counted that cache as the agent's
// memory. It also prints the same comparison for agents already running on this
// machine, without changing them. State is throwaway.
//
//   mise exec -- node scripts/demo/memory.mjs > .demo-runs/memory/demo.log 2>&1
//
// BEFORE_REF is the commit to build the "before" binary from (default c3e5692).
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/memory/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');
const beforeRef = process.env.BEFORE_REF ?? 'c3e5692';

const work = mkdtempSync(join(tmpdir(), 'agentbox-mem-'));
const bins = { before: join(work, 'bin', 'agentbox-before'), after: join(work, 'bin', 'agentbox') };
const repo = join(work, 'repos', 'hello-stack');
const socket = join(work, 'data/agentbox/run/agentbox.sock');
const A = 'hello-stack/agent-01';
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART']) delete env[name];

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$TMP');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const MiB = 2 ** 20;
const gib = (n) => `${(n / 2 ** 30).toFixed(2)} GiB`;

function ab(bin, ...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}

function api(path) {
  return new Promise((resolve, reject) => {
    const req = http.request({ socketPath: socket, path }, (res) => {
      let body = '';
      res.on('data', (chunk) => (body += chunk));
      res.on('end', () => resolve(JSON.parse(body)));
    });
    req.on('error', reject);
    req.end();
  });
}

async function step(title, fn) {
  console.log(`\n######## ${title}`);
  try {
    await fn();
  } catch (err) {
    check(title, false, err.message);
    console.log(hide(err.stack ?? err));
    throw err;
  }
}

// freeUsed is the memory `free` reports in use inside an instance (lxcfs).
const freeUsed = (instance) => Number(run('incus', ['exec', instance, '--', 'free', '-b']).split('\n').find((l) => l.startsWith('Mem:')).trim().split(/\s+/)[2]);

// cgroup reads what the kernel charges to an instance's cgroup.
function cgroup(instance) {
  const dir = `/sys/fs/cgroup/lxc.payload.${instance}`;
  const stat = Object.fromEntries(readFileSync(`${dir}/memory.stat`, 'utf8').trim().split('\n').map((l) => l.split(' ')).map(([k, v]) => [k, Number(v)]));
  return { current: Number(readFileSync(`${dir}/memory.current`, 'utf8')), ...stat };
}

// compare prints what AgentBox shows next to what the agent uses, and returns both.
async function compare(label) {
  const usage = await api('/v1/usage?interval=1s');
  const shown = usage.agents.find((a) => a.ref === A).memory;
  const agent = (await api(`/v1/agents/${A}`)).instance;
  const used = freeUsed(agent);
  const cg = cgroup(agent);
  console.log(
    `\n${label}: AgentBox shows ${gib(shown)}. Inside the agent, free says ${gib(used)} used.` +
      `\n  cgroup: memory.current ${gib(cg.current)} = anon ${gib(cg.anon)} + page cache ${gib(cg.active_file + cg.inactive_file)} + kernel ${gib(cg.kernel)} (reclaimable slab ${gib(cg.slab_reclaimable)})` +
      `\n  host: ${gib(usage.host.memUsed)} used of ${gib(usage.host.memTotal)}`,
  );
  return { shown, used, cache: cg.active_file + cg.inactive_file };
}

const shot = (page, name) => page.screenshot({ path: join(media, `memory-${name}.png`) });

async function screenshots(xvfb, bin, name) {
  const app = await electron.launch({ args: [desktop, '--disable-gpu'], cwd: desktop, env: { ...env, AGENTBOX_BIN: bin, DISPLAY: xvfb.display } });
  try {
    const page = await app.firstWindow();
    await page.locator(`[data-agent="${A}"]`).click({ timeout: 30_000 });
    await page.getByRole('tab', { name: /Overview/ }).click();
    await page.getByText('Processes').waitFor();
    await sleep(6_000);
    await shot(page, name);
    return await page.locator(`[data-agent="${A}"]`).innerText();
  } finally {
    await app.close();
  }
}

let xvfb;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  mkdirSync(media, { recursive: true });
  mkdirSync(join(work, 'bin'), { recursive: true });
  const src = join(work, 'src-before');
  mkdirSync(src);
  execFileSync('sh', ['-c', `git -C "$1" archive "$2" | tar -x -C "$3"`, 'sh', root, beforeRef, src]);
  run('go', ['build', '-o', bins.before, './cmd/agentbox'], { cwd: src });
  run('go', ['build', '-o', bins.after, './cmd/agentbox'], { cwd: root });
  log(`built agentbox before the fix (${beforeRef}) and after it (this checkout)`);
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  xvfb = await startXvfb({ width: 1440, height: 900 });

  await step('1. Agents already running on this machine (read only)', async () => {
    const instances = JSON.parse(run('incus', ['list', '--format', 'json'])).filter((i) => i.status === 'Running' && i.name.startsWith('ab-'));
    const rows = [['INSTANCE', 'INCUS (BEFORE)', 'PAGE CACHE', 'RECL. SLAB', 'NEW', 'FREE IN AGENT']];
    for (const inst of instances) {
      try {
        const cg = cgroup(inst.name);
        const fresh = cg.current - cg.active_file - cg.inactive_file - cg.slab_reclaimable;
        rows.push([inst.name, gib(inst.state.memory.usage), gib(cg.active_file + cg.inactive_file), gib(cg.slab_reclaimable), gib(fresh), gib(freeUsed(inst.name))]);
      } catch (err) {
        rows.push([inst.name, '(unreadable)', err.message.split('\n')[0], '', '', '']);
      }
    }
    console.log(`\n${rows.map((r) => r.map((c, i) => String(c).padEnd(i === 0 ? 26 : 16)).join('')).join('\n')}`);
  });

  await step('2. An agent that uses 512 MiB and has read a 2 GiB file', async () => {
    ab(bins.before, 'add', repo);
    ab(bins.before, 'create', 'hello-stack', '--ai', 'none', '--title', 'Memory check');
    // Touch every page, so the 512 MiB are really in use.
    ab(bins.before, 'exec', A, '--', `tmux new-session -d -s hold "python3 -c 'import time; b = bytearray(512 << 20); b[::4096] = b\\"\\\\x01\\" * len(b[::4096]); time.sleep(3600)'"`);
    ab(bins.before, 'exec', A, '--', 'head -c 2G /dev/urandom > ~/cache.bin && cat ~/cache.bin > /dev/null && sleep 3 && free -h');
  });

  await step('3. Before the fix: AgentBox counts the page cache', async () => {
    const { shown, used, cache } = await compare('Before');
    ab(bins.before, 'top');
    check('before: the memory shown includes the page cache, well above what free reports', shown - used > 1.5 * 1024 * MiB, `shown ${gib(shown)}, used ${gib(used)}, cache ${gib(cache)}`);
    const row = await screenshots(xvfb, bins.before, '01-before');
    console.log(`\nsidebar row: ${row.replace(/\n+/g, ' · ')}`);
    ab(bins.before, 'daemon', 'stop');
  });

  await step('4. After the fix: AgentBox shows what the agent uses', async () => {
    ab(bins.after, 'daemon', 'start');
    const { shown, used } = await compare('After');
    ab(bins.after, 'top');
    check('after: the memory shown matches free inside the agent (within 64 MiB or 5%)', Math.abs(shown - used) <= Math.max(64 * MiB, 0.05 * used), `shown ${gib(shown)}, used ${gib(used)}`);
    check('after: the agent still counts the 512 MiB it holds', shown >= 512 * MiB, gib(shown));
    const usage = await api('/v1/usage?interval=1s');
    const agents = usage.agents.reduce((sum, a) => sum + a.memory, 0);
    check("after: the agents' memory adds up to less than the host's memory in use", agents < usage.host.memUsed, `agents ${gib(agents)}, host ${gib(usage.host.memUsed)}`);
    const row = await screenshots(xvfb, bins.after, '02-after');
    console.log(`\nsidebar row: ${row.replace(/\n+/g, ' · ')}`);
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  for (const bin of [bins.after, bins.before]) {
    if (!existsSync(bin)) continue;
    spawnSync(bin, ['destroy', A, '--force', '--delete-branch'], { env });
    spawnSync(bin, ['remove', 'hello-stack'], { env });
    spawnSync(bin, ['daemon', 'stop'], { env });
  }
  xvfb?.stop();
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
