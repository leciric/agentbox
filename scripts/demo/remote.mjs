#!/usr/bin/env node
// Reproduces the Step 10 evidence: environments on a hub.
// - A fresh Debian 13 VM stands in for the VPS. It runs Postgres, the hub
//   (agentbox-hub, with the web app) and AgentBox, connected to the hub as
//   the environment "vps".
// - This machine connects to the same hub as "pc", with throwaway state.
// - The CLI uses vps through the hub (--env). The desktop app signs in to the
//   hub, switches to vps, and uses an agent there: terminal, browser, media, resources.
// - This machine goes offline: the hub shows pc offline while the agent on vps
//   keeps working, and a phone-sized browser signs in to the hub and follows
//   that agent's terminal.
// - The VPS restarts: the hub, the daemon and the agent come back by themselves.
// - Without signing in, the app still manages this machine.
//
//   npm --prefix desktop run build && mise exec -- node scripts/demo/remote.mjs > .demo-runs/remote/demo.log 2>&1
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/remote/media');
const require = createRequire(join(desktop, 'package.json'));
const { _electron: electron, chromium } = require('playwright');

const vm = 'abx-vps';
const email = 'owner@example.com';
const password = 'correct horse battery staple';
mkdirSync(join(homedir(), '.cache/agentbox-evidence'), { recursive: true });
const work = mkdtempSync(join(homedir(), '.cache/agentbox-evidence/remote-'));
const bin = join(work, 'bin', 'agentbox');
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'AGENTBOX_ENV']) delete env[name];

const started = Date.now();
const results = [];
// hide keeps paths and environment tokens out of the log. The hub is deleted at the end, but tokens still don't belong there.
const hide = (text) => String(text).replaceAll(work, '$TMP').replaceAll(homedir(), '~').replace(/abx_e_[A-Za-z0-9_-]+/g, 'abx_e_…');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(6)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(-500)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const seconds = (since) => `${((Date.now() - since) / 1000).toFixed(0)}s`;

function run(shown, cmd, args, { input, tail = 30, quietOut = false } = {}) {
  return new Promise((resolve) => {
    const child = spawn(cmd, args, { env });
    let out = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (out += d));
    child.on('close', (code) => {
      const lines = hide(out).trimEnd().split('\n');
      const cut = lines.length > tail ? [`… (${lines.length - tail} lines before)`, ...lines.slice(-tail)] : lines;
      console.log(`\n$ ${hide(shown)}${quietOut ? '' : `\n${cut.join('\n')}`}${code ? `\n(exit ${code})` : ''}`);
      resolve(out);
    });
    child.stdin.end(input ?? '');
  });
}
// pc runs agentbox on this machine, with the throwaway state.
const pc = (args, input) => run(`agentbox ${args.join(' ')}`, bin, args, { input });
// onVPS runs a command as root on the VPS; asDev as its user, in a login shell.
const onVPS = (command, options) => run(`[vps, root] ${command}`, 'incus', ['exec', vm, '--', 'bash', '-lc', command], options);
const asDev = (command, options) => run(`[vps] ${command}`, 'incus', ['exec', vm, '--', 'su', '-', 'dev', '-c', command], options);

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

async function until(what, fn, timeout = 30_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = await fn();
    if (value) return value;
    await sleep(1_000);
  }
  throw new Error(`timed out waiting for ${what}`);
}

let xvfb;
let app;
let browser;
let hubURL;
let failed = false;
const shots = { app: null, web: null };
const shot = (page, name) => page.screenshot({ path: join(media, `remote-${name}.png`) });

try {
  console.log('######## Setup (off camera)');
  mkdirSync(media, { recursive: true });
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  // The hub is its own program, in leciric/agentbox-hub (D44).
  const hubBin = process.env.AGENTBOX_HUB_BIN;
  if (!hubBin || !existsSync(hubBin)) throw new Error('build agentbox-hub in leciric/agentbox-hub, and set AGENTBOX_HUB_BIN to it');
  if (!existsSync(join(desktop, 'out/web/index.html'))) throw new Error('build the app first: npm --prefix desktop run build');
  xvfb = await startXvfb({ width: 1440, height: 900 });

  await step('1. A VPS: a fresh Debian 13 VM with Postgres, the hub and its web app', async () => {
    const t0 = Date.now();
    spawnSync('incus', ['delete', '--force', vm], { stdio: 'ignore' });
    await run(`incus launch images:debian/13 ${vm} --vm (4 CPUs, 10 GiB)`, 'incus', ['launch', 'images:debian/13', vm, '--vm', '-c', 'limits.cpu=4', '-c', 'limits.memory=10GiB', '-d', 'root,size=60GiB']);
    await until('the VM to reach the network', async () => spawnSync('incus', ['exec', vm, '--', 'getent', 'hosts', 'deb.debian.org']).status === 0, 180_000);
    await onVPS(
      'apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends sudo git ca-certificates curl postgresql >/dev/null && ' +
        'useradd -m -u 1000 -s /bin/bash -G sudo dev && echo "dev ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/dev && ' +
        'psql --version && . /etc/os-release && echo "$PRETTY_NAME, $(nproc) CPUs"',
    );
    spawnSync('incus', ['file', 'push', bin, `${vm}/usr/local/bin/agentbox`, '--mode', '0755']);
    spawnSync('incus', ['file', 'push', hubBin, `${vm}/usr/local/bin/agentbox-hub`, '--mode', '0755']);
    spawnSync('incus', ['exec', vm, '--', 'mkdir', '-p', '/opt/agentbox']);
    spawnSync('incus', ['file', 'push', '-r', join(desktop, 'out/web'), `${vm}/opt/agentbox/`]);
    spawnSync('incus', ['file', 'push', '-r', join(root, 'testdata/fixtures/hello-stack'), `${vm}/home/dev/`]);
    await onVPS('chown -R dev:dev /home/dev && ls /opt/agentbox/web');
    // Postgres for the hub: a role and a database of its own.
    const db = await onVPS(
      `sudo -u postgres psql -qc "CREATE ROLE agentbox LOGIN PASSWORD 'hub-secret'" && sudo -u postgres createdb -O agentbox agentbox && echo "database ready"`,
    );
    check('Postgres runs on the VPS, with a database for the hub', db.includes('database ready'), db);
    const dsn = 'postgres://agentbox:hub-secret@127.0.0.1:5432/agentbox';
    const account = await asDev(`printf '%s\\n' '${password}' | agentbox-hub user add --db '${dsn}' --email ${email} --name Owner --password-stdin`);
    check('agentbox-hub user add creates the first account, in Postgres', account.includes(`Created the account ${email}`), account);
    // The hub, as a service. It listens on the VM's network so this machine reaches it; on a real VPS it sits behind a TLS proxy.
    await onVPS(
      `cat > /etc/systemd/system/agentbox-hub.service <<'EOF'
[Unit]
Description=AgentBox hub
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
User=dev
Environment=HOME=/home/dev
ExecStart=/usr/local/bin/agentbox-hub --listen 0.0.0.0:8080 --db ${dsn} --web /opt/agentbox/web
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable --now agentbox-hub && sleep 2 && systemctl is-active agentbox-hub`,
    );
    const network = JSON.parse(execFileSync('incus', ['list', vm, '--format', 'json'], { encoding: 'utf8' }))[0].state.network;
    const ip = Object.entries(network)
      .filter(([name]) => name !== 'lo')
      .flatMap(([, n]) => n.addresses)
      .find((a) => a.family === 'inet' && a.scope === 'global').address;
    hubURL = `http://${ip}:8080`;
    const health = await until('the hub to answer', async () => fetch(`${hubURL}/healthz`).then((r) => r.ok).catch(() => false), 30_000);
    log(`the VPS and its hub, at ${hubURL}, in ${seconds(t0)}`);
    check('the hub answers from the VPS, and serves the web app', health && (await fetch(hubURL).then((r) => r.text())).includes('<div id="root">'));
  });

  await step('2. AgentBox on the VPS, connected to its hub as "vps"', async () => {
    const t0 = Date.now();
    await asDev('sudo "$(command -v agentbox)" host setup 2>&1 | tail -2');
    // A new login has the incus-admin group.
    const built = await asDev('agentbox image build 2>&1 | grep -E "^==>|ready in|rror" | tail -6; echo "agentbox image build exited with ${PIPESTATUS[0]}"', { tail: 10 });
    check('agentbox image build finishes without an error on the new VPS', built.includes('exited with 0'), built);
    log(`host setup and the base image on the VPS in ${seconds(t0)}`);
    // The daemon as a user service that starts at boot, without anyone logged in.
    const service = await asDev('agentbox daemon stop; sudo loginctl enable-linger dev && sleep 2 && XDG_RUNTIME_DIR=/run/user/$(id -u) agentbox daemon install');
    check("the VPS runs AgentBox's daemon as a service that starts at boot", service.includes('started agentbox.service'), service);
    await asDev(`printf '%s\\n' '${password}' | agentbox login http://127.0.0.1:8080 --email ${email} --password-stdin`);
    const added = await asDev('agentbox env add vps');
    const token = /--token (abx_e_\S+)/.exec(added)?.[1];
    const connected = await asDev(`agentbox remote connect http://127.0.0.1:8080 --token ${token}`);
    check('the VPS connects to the hub as vps', connected.includes('Connected to http://127.0.0.1:8080'), connected);
    await asDev('cd ~/hello-stack && git init -q -b main && git add -A && git -c user.name=dev -c user.email=dev@example.com commit -qm "first commit" && agentbox add ~/hello-stack | head -2');
  });

  await step('3. This machine connects to the same hub as "pc"; the CLI uses vps through it', async () => {
    await pc(['login', hubURL, '--email', email, '--password-stdin'], `${password}\n`);
    const added = await pc(['env', 'add', 'pc']);
    const token = /--token (abx_e_\S+)/.exec(added)?.[1];
    const connected = await pc(['remote', 'connect', hubURL, '--token', token]);
    check('this machine connects to the hub on the VPS as pc', connected.includes('Connected to'), connected);
    const list = await pc(['env', 'list']);
    check('agentbox env list shows vps and pc, both online', /vps\s+online/.test(list) && /pc\s+online/.test(list), list);
    const t0 = Date.now();
    const created = await pc(['--env', 'vps', 'create', 'hello-stack', '--ai', 'none', '--title', 'Runs on the VPS']);
    log(`an agent created on the VPS, from this machine, in ${seconds(t0)}`);
    check('agentbox --env vps create makes an agent on the VPS, through the hub', created.includes('agentbox/agent-01'), created);
    const agents = await pc(['--env', 'vps', 'list']);
    const onVps = await asDev('agentbox list');
    check('the agent is on the VPS: its own agentbox list shows it', agents.includes('hello-stack/agent-01') && onVps.includes('hello-stack/agent-01'), agents + onVps);
    const db = await onVPS(`sudo -u postgres psql agentbox -Atc "SELECT name, hostname, version FROM environments ORDER BY name"`);
    check("the hub's Postgres has both environments", db.includes('pc|') && db.includes('vps|'), db);
  });

  await step('4. The desktop app signs in to the hub, and uses the agent on vps', async () => {
    app = await electron.launch({
      args: [desktop, '--disable-gpu'],
      cwd: desktop,
      env: { ...env, DISPLAY: xvfb.display },
      recordVideo: { dir: join(work, 'video-app'), size: { width: 1440, height: 900 } },
    });
    const page = await app.firstWindow();
    shots.app = page;
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 60_000 });
    await sleep(1_000);
    await shot(page, '01-app-this-machine');
    await page.locator('[data-environment-switcher]').click();
    await page.getByRole('menuitem', { name: 'Connect to a hub…' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('#hub-url').fill(hubURL);
    await dialog.locator('#hub-email').fill(email);
    await dialog.locator('#hub-password').fill(password);
    await sleep(500);
    await shot(page, '02-app-connect-to-hub');
    await dialog.getByRole('button', { name: 'Sign in' }).click();
    await dialog.waitFor({ state: 'detached', timeout: 20_000 });
    await page.locator('[data-environment-switcher]').click();
    await page.locator('[data-environment-option="vps"][data-online="true"]').waitFor({ timeout: 20_000 });
    await sleep(800);
    await shot(page, '03-app-environments');
    check('the switcher lists this machine, vps and pc, with vps online', (await page.locator('[data-environment-option="pc"]').count()) === 1);
    await page.locator('[data-environment-option="vps"]').click();
    await page.locator('[data-environment-switcher="vps"]').waitFor({ timeout: 10_000 });
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
    await page.locator('[data-agent="hello-stack/agent-01"]').click({ timeout: 30_000 });
    await page.getByRole('tab', { name: /Terminal/ }).click();
    await page.locator('.xterm-rows').first().waitFor({ timeout: 30_000 });
    await sleep(1_500);
    await page.locator('.xterm-helper-textarea').first().focus();
    await page.keyboard.type('clear; echo "on $(hostname), kernel $(uname -r)"; while true; do date +"still working at %T"; sleep 1; done\n', { delay: 12 });
    await page.waitForFunction(() => /still working at/.test(document.querySelector('.xterm-rows')?.textContent ?? ''), null, { timeout: 30_000 });
    await sleep(2_000);
    await shot(page, '04-app-vps-terminal');
    const text = await page.locator('.xterm-rows').first().innerText();
    check("the agent's terminal on vps works in the app, through the hub", text.includes('on ab-hello-stack-agent-01'), text);
    await page.getByRole('tab', { name: /Browser/ }).click();
    await page.locator('[data-browser-view="hello-stack/agent-01"][data-connected="true"]').waitFor({ timeout: 60_000 });
    await sleep(3_000);
    await shot(page, '05-app-vps-browser');
    check("the agent's browser on vps shows in the app, through the hub", true);
    // Media: a screenshot taken in the app is kept on vps, and its file comes back through the hub.
    await page.getByRole('button', { name: 'Screenshot', exact: true }).click();
    await page.getByText(/^Saved “.*” to Media$/).waitFor({ timeout: 60_000 });
    await page.getByRole('tab', { name: /Media/ }).click();
    const loaded = await page
      .waitForFunction(() => [...document.querySelectorAll('[data-media] img')].some((img) => img.complete && img.naturalWidth > 0), null, { timeout: 30_000 })
      .then(() => true, () => false);
    await sleep(1_500);
    await shot(page, '06-app-vps-media');
    await asDev('agentbox media list hello-stack/agent-01');
    check("a screenshot taken in the app is kept in the agent's Media on vps, and its image loads through the hub", loaded);
    await page.getByRole('tab', { name: /Overview/ }).click();
    await page.getByText('Processes').waitFor();
    await sleep(4_000);
    await shot(page, '07-app-vps-overview');
    await page.getByRole('tab', { name: /Terminal/ }).click();
  });

  await step('5. This machine goes offline; the agent on vps keeps working', async () => {
    await app.close();
    app = undefined;
    const stopped = await pc(['daemon', 'stop']);
    check("this machine's daemon stops", stopped.includes('Stopped'), stopped);
    const list = await until(
      'the hub to show pc offline',
      async () => {
        const res = spawnSync(bin, ['env', 'list'], { env, encoding: 'utf8' });
        return /pc\s+offline/.test(res.stdout) && res.stdout;
      },
      30_000,
    );
    console.log(`\n$ agentbox env list\n${list.trim()}`);
    check('the hub shows pc offline, and vps online', /vps\s+online/.test(list), list);
    const alive = await asDev('sleep 3; agentbox exec hello-stack/agent-01 -- "tmux capture-pane -p -t main | grep -c still\\ working"');
    check('the loop in the agent on vps is still running', Number(alive.trim().split('\n').pop()) > 3, alive);
  });

  await step("6. A phone signs in to the hub's web app, and follows the agent's terminal on vps", async () => {
    browser = await chromium.launch();
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      deviceScaleFactor: 3,
      isMobile: true,
      hasTouch: true,
      recordVideo: { dir: join(work, 'video-web'), size: { width: 390, height: 844 } },
    });
    const page = await context.newPage();
    shots.web = page;
    await page.goto(`${hubURL}/`);
    await page.locator('#web-email').fill(email);
    await page.locator('#web-password').fill(password);
    await sleep(500);
    await shot(page, '08-phone-sign-in');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.locator('[data-environment-card="vps"]').waitFor({ timeout: 20_000 });
    await sleep(800);
    await shot(page, '09-phone-environments');
    const cards = await page.locator('[data-environment-card]').allInnerTexts();
    check('on the phone, vps is online and pc offline', cards.some((c) => c.includes('vps') && c.includes('online')) && cards.some((c) => c.includes('pc') && c.includes('offline')), cards.join(' | '));
    await page.locator('[data-environment-card="vps"]').click();
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
    await page.getByRole('button', { name: 'Open the menu' }).click();
    await sleep(600);
    await shot(page, '10-phone-menu');
    await page.locator('[data-nav-drawer] [data-agent="hello-stack/agent-01"]').click();
    await page.locator('.xterm-rows').first().waitFor({ timeout: 30_000 });
    const before = await until('the loop on the phone', async () => {
      const t = await page.locator('.xterm-rows').first().innerText();
      return t.includes('still working at') && t;
    });
    await sleep(3_000);
    await shot(page, '11-phone-terminal');
    const after = await page.locator('.xterm-rows').first().innerText();
    const last = (t) => [...t.matchAll(/still working at (\d\d:\d\d:\d\d)/g)].pop()?.[1];
    check("the phone shows the agent's terminal live: the clock moved on while it watched", last(before) && last(after) && last(after) !== last(before), `${last(before)} → ${last(after)}`);
    // The rest of the agent's page, at a phone's width.
    await page.getByRole('tab', { name: /Media/ }).click();
    await page.locator('[data-media] img').first().waitFor({ timeout: 20_000 });
    await sleep(1_500);
    await shot(page, '12-phone-media');
    await page.getByRole('tab', { name: /Overview/ }).click();
    await page.getByText('Processes').waitFor();
    await sleep(2_000);
    await shot(page, '13-phone-overview');
    await context.close();
  });

  await step('7. The VPS restarts: its hub, its daemon and the agent come back by themselves', async () => {
    const t0 = Date.now();
    await run(`incus restart ${vm}`, 'incus', ['restart', vm]);
    const list = await until(
      'vps to come back online',
      async () => {
        const res = spawnSync(bin, ['env', 'list'], { env, encoding: 'utf8' });
        return /vps\s+online/.test(res.stdout) && res.stdout;
      },
      300_000,
    );
    log(`the VPS restarted, and vps was back online on its hub in ${seconds(t0)}`);
    console.log(`\n$ agentbox env list\n${list.trim()}`);
    check('after a restart, the hub and the vps environment come back without anyone logging in', true);
    // Incus starts the agent's container again at boot, and that can finish after the daemon is back on the hub.
    const running = await until(
      'the agent on vps to run again',
      async () => /hello-stack\/agent-01\s+\S+\s+running/.test(spawnSync(bin, ['--env', 'vps', 'list'], { env, encoding: 'utf8' }).stdout),
      180_000,
    ).catch(() => false);
    if (running) log(`the agent on vps was running again ${seconds(t0)} after the restart`);
    const agents = await pc(['--env', 'vps', 'list']);
    check('the agent on vps is running again', running && /hello-stack\/agent-01\s+\S+\s+running/.test(agents), agents);
  });

  await step('8. Not signed in, the app still manages this machine', async () => {
    const local = mkdtempSync(join(work, 'local-'));
    const localEnv = { ...env, XDG_CONFIG_HOME: join(local, 'config'), XDG_DATA_HOME: join(local, 'data') };
    const localApp = await electron.launch({ args: [desktop, '--disable-gpu'], cwd: desktop, env: { ...localEnv, DISPLAY: xvfb.display } });
    try {
      const page = await localApp.firstWindow();
      await page.locator('[data-connection="connected"]').waitFor({ timeout: 60_000 });
      await page.locator('[data-environment-switcher="local"]').waitFor();
      await sleep(1_500);
      await shot(page, '14-app-local-only');
      check('with no hub, the app opens on this machine and its daemon, as before', true);
    } finally {
      await localApp.close();
      spawnSync(bin, ['daemon', 'stop'], { env: localEnv });
    }
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  await browser?.close().catch(() => {});
  spawnSync(bin, ['daemon', 'stop'], { env });
  xvfb?.stop();
  for (const [dir, name, speed] of [['video-app', 'remote-app', 2], ['video-web', 'remote-phone', 2]]) {
    const path = join(work, dir);
    const video = existsSync(path) ? readdirSync(path).find((f) => f.endsWith('.webm')) : undefined;
    if (!video) continue;
    const mp4 = join(media, `${name}.mp4`);
    execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-i', join(path, video), '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '30', '-preset', 'veryfast', '-movflags', '+faststart', mp4]);
    execFileSync('ffmpeg', [
      '-y', '-loglevel', 'error', '-i', mp4,
      '-vf', `setpts=PTS/${speed},fps=6,scale=${name === 'remote-phone' ? 390 : 960}:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4`,
      join(media, `${name}.gif`),
    ]);
  }
  spawnSync('incus', ['delete', '--force', vm]);
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
