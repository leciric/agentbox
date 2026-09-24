#!/usr/bin/env node
// Reproduces the Step 8 evidence: an agent asked to "implement it, then prove
// it works" keeps screenshots, a recording, test results and a note, with no
// further help; all of it shows up in the app's Media tab. Also checks that
// agents can only add to their own media, that recordings stop by themselves,
// and that an export is ready for a pull request. Records screenshots and a
// video of the app. State is throwaway. Needs the base image and a Claude Code
// login on the host (its short-lived access token is copied into the throwaway
// state, never printed).
//
//   mise exec -- node scripts/demo/step-8.mjs > .demo-runs/step-8/demo.log 2>&1
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import net from 'node:net';
import { homedir, tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/step-8/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-step8-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-stack');
const socket = join(work, 'data/agentbox/run/agentbox.sock');
const A1 = 'hello-stack/agent-01';
const A2 = 'hello-stack/agent-02';
const previewPort = 7798;
const env = {
  ...process.env,
  HOME: join(work, 'home'), // the app's Export writes to ~/AgentBox/exports
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: `127.0.0.1:${previewPort}`,
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
const shellQuote = (s) => `'${s.replaceAll("'", `'\\''`)}'`;

// ab runs the CLI and prints the command and its output, like a terminal.
function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}
const quiet = (...args) => hide(spawnSync(bin, args, { env, encoding: 'utf8' }).stdout);

// api calls the daemon's HTTP API on its socket.
function api(method, path) {
  return new Promise((resolve, reject) => {
    const req = http.request({ socketPath: socket, path, method }, (res) => {
      let body = '';
      res.on('data', (chunk) => (body += chunk));
      res.on('end', () => resolve(body ? JSON.parse(body) : null));
    });
    req.on('error', reject);
    req.end();
  });
}
const mediaOf = (ref) => api('GET', `/v1/agents/${ref}/media`);

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

function portOpen(port) {
  return new Promise((resolve) => {
    const s = net.connect({ host: '127.0.0.1', port, timeout: 1_000 });
    s.once('connect', () => (s.destroy(), resolve(true)));
    s.once('timeout', () => (s.destroy(), resolve(false)));
    s.once('error', () => resolve(false));
  });
}

const shot = (page, name) => page.screenshot({ path: join(media, `step-8-media-${name}.png`) });

let xvfb;
let app;
let page;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  mkdirSync(media, { recursive: true });
  mkdirSync(env.HOME, { recursive: true });
  // The throwaway home has no git identity: give the fixture's commit, and the agents, a made-up one.
  writeFileSync(join(env.HOME, '.gitconfig'), '[user]\n\tname = AgentBox Evidence\n\temail = evidence@agentbox.invalid\n');
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  const token = JSON.parse(readFileSync(join(homedir(), '.claude/.credentials.json'), 'utf8')).claudeAiOauth.accessToken;
  run(bin, ['auth', 'claude', '--token-stdin'], { input: token });
  log('saved an AgentBox Claude Code login in the throwaway state (not printed)');
  if (await portOpen(previewPort)) throw new Error(`port ${previewPort} is taken; the preview proxy needs it`);
  ab('add', repo);
  ab('create', 'hello-stack', '--ai', 'claude', '--title', 'About page');
  ab('create', 'hello-stack', '--ai', 'none', '--title', 'Isolation check');
  quiet('exec', A1, '--', "tmux new-window -d -t main -n dev 'node server.mjs'");
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  xvfb = await startXvfb({ width: 1440, height: 900 });
  log(`built the desktop app; private X display ${xvfb.display}`);

  app = await electron.launch({
    args: [desktop, '--disable-gpu'],
    cwd: desktop,
    env: { ...env, DISPLAY: xvfb.display },
    recordVideo: { dir: join(work, 'video'), size: { width: 1440, height: 900 } },
  });
  page = await app.firstWindow();
  await page.locator(`[data-agent="${A1}"]`).click({ timeout: 30_000 });
  await page.getByRole('tab', { name: /Media/ }).click();
  await page.getByText('Nothing kept yet').waitFor();
  await sleep(1_000);
  await shot(page, '01-empty');

  await step('1. Ask Claude Code to implement a page, then prove it works', async () => {
    const prompt =
      'Add an /about page to server.mjs: a heading "About hello-stack" and a link back to /login. The dev server runs in tmux window "dev"; ' +
      'restart it there after your change (tmux send-keys -t main:dev C-c, then node server.mjs). When it works, prove it: run npm test and keep ' +
      'its results, record a short demo in the browser going from /login to /about, take screenshots of both pages, and leave a short note on ' +
      'what you did and how you checked it.';
    console.log(`\n$ agentbox exec ${A1} -- claude -p --model sonnet --dangerously-skip-permissions '${prompt}'`);
    const claude = spawn(bin, ['exec', A1, '--', `timeout 900 claude -p --model sonnet --dangerously-skip-permissions ${shellQuote(prompt)}`], { env });
    let reply = '';
    claude.stdout.on('data', (d) => (reply += d));
    claude.stderr.on('data', (d) => (reply += d));
    const exited = new Promise((r) => claude.once('exit', r));

    let shots = 0;
    const deadline = Date.now() + 900_000;
    let done = false;
    exited.then(() => (done = true));
    while (!done && Date.now() < deadline) {
      await sleep(5_000);
      const count = await page.locator('[data-media]').count();
      if (count > shots && shots < 4) {
        shots = count;
        await sleep(1_500);
        await shot(page, `02-arriving-${shots}`);
      }
    }
    await exited;
    console.log(hide(reply.trim()));
    await sleep(2_000);

    const items = await mediaOf(A1);
    const fromAgent = items.filter((i) => i.source === 'agent');
    const kinds = fromAgent.map((i) => `${i.kind}:${i.name}${i.meta.tests ? ` (${i.meta.tests.passed} passed, ${i.meta.tests.failed} failed)` : ''}`);
    console.log(`\nMedia the agent kept:\n  ${kinds.join('\n  ')}`);
    check('Claude kept at least two screenshots', fromAgent.filter((i) => i.kind === 'screenshot').length >= 2, kinds.join(', '));
    check('Claude kept a recording', fromAgent.some((i) => i.kind === 'recording' && i.meta.duration > 0), kinds.join(', '));
    check('Claude kept the test results, with pass and fail counts', fromAgent.some((i) => i.meta.tests && i.meta.tests.passed > 0), kinds.join(', '));
    check('Claude left a note', fromAgent.some((i) => i.kind === 'note' && i.text.length > 20), kinds.join(', '));
    const about = run('curl', ['-s', `http://3000.agent-01.hello-stack.localhost:${previewPort}/about`]);
    check('the /about page it made is served by agent-01', /About hello-stack/.test(about), about);
  });

  await step('2. Everything shows up in the Media tab', async () => {
    await page.locator('[data-media]').first().waitFor();
    await sleep(1_500);
    await shot(page, '03-gallery');
    check('the gallery shows every item', (await page.locator('[data-media]').count()) === (await mediaOf(A1)).length);

    await page.getByRole('button', { name: /^Screenshots/ }).click();
    await sleep(800);
    await page.locator('[data-media="screenshot"]').first().click();
    await page.getByRole('dialog').locator('img').waitFor();
    await sleep(1_200);
    await shot(page, '04-screenshot');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });

    await page.getByRole('button', { name: /^All/ }).click();
    await page.locator('[data-media="recording"]').first().click();
    const video = page.getByRole('dialog').locator('video');
    await video.waitFor();
    await until('the recording to play', async () => (await video.evaluate((v) => v.currentTime)) > 1, 20_000);
    check('the recording plays in the viewer', true);
    await shot(page, '05-recording');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });

    const report = page.locator('[data-media="report"]').first();
    if (await report.count()) {
      await report.click();
      await sleep(1_500);
      await shot(page, '06-report');
      await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    }
    await page.locator('[data-media="note"]').first().click();
    await sleep(1_000);
    await shot(page, '07-note');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });
  });

  await step('3. You add to it, and export it for a pull request', async () => {
    await page.getByRole('button', { name: 'Note', exact: true }).click();
    await page.locator('#note-text').fill('Checked /about by hand in the browser tab: the link back to /login works.');
    await page.getByRole('button', { name: 'Save note' }).click();
    await until('your note', async () => (await mediaOf(A1)).some((i) => i.source === 'user' && i.kind === 'note'));
    check('a note you add is marked as yours', true);
    await page.getByRole('button', { name: 'Export' }).click();
    await page.getByText(/^Exported \d+ items/).waitFor({ timeout: 30_000 });
    await sleep(1_000);
    await shot(page, '08-exported');
    const exports = join(env.HOME, 'AgentBox', 'exports');
    const folder = readdirSync(exports)[0];
    const readme = readFileSync(join(exports, folder, 'README.md'), 'utf8');
    console.log(`\n$ cat ~/AgentBox/exports/${folder}/README.md\n${readme.trim()}`);
    const items = await mediaOf(A1);
    const files = readdirSync(join(exports, folder));
    const withFiles = items.filter((i) => i.kind !== 'note').length; // notes are text in the README
    check(
      'the export has a README.md naming every item, and a file for each item that has one',
      items.every((i) => readme.includes(`## ${i.name}`)) && files.includes('README.md') && files.length === withFiles + 1,
      `${files.length} files for ${withFiles} items with files`,
    );
  });

  await step("4. Agents add to their own media only, and can't change it", async () => {
    const [item] = (await mediaOf(A1)).filter((i) => i.kind === 'screenshot');
    const del = ab('exec', A1, '--', `curl -s -X DELETE --unix-socket /run/agentbox.sock http://agentbox/v1/media/${item.id}; echo; agentbox media rm ${A1} ${item.id}`);
    check("inside agent-01, deleting its own media is refused", del.includes('not available inside an agent'), del);
    const sum = createHash('sha256').update(readFileSync(item.path)).digest('hex');
    check("the stored file still matches the SHA-256 recorded when it was added", sum === item.sha256, `${sum} != ${item.sha256}`);
    const other = ab('exec', A2, '--', 'agentbox media list');
    check("agent-02 sees only its own media, which is empty", other.includes('No media yet'), other);
    const probe = ab('exec', A2, '--', `curl -s --unix-socket /run/agentbox.sock http://agentbox/v1/agents/hello-stack/agent-01/media`);
    check("agent-02 can't list agent-01's media", probe.includes('not available inside an agent'), probe);
  });

  await step('5. A forgotten recording stops by itself', async () => {
    ab('exec', A2, '--', 'agentbox media record start --name forgotten --limit 4s');
    await sleep(8_000);
    const status = ab('exec', A2, '--', 'agentbox media record status');
    check('after its 4 s limit, the recording has stopped', status.includes('Not recording'), status);
    const kept = ab('exec', A2, '--', 'agentbox media record stop && agentbox media list');
    const [item] = await mediaOf(A2);
    check('stopping it afterwards still keeps the video, about 4 s long', item?.kind === 'recording' && item.meta.duration >= 3 && item.meta.duration <= 6, kept);
  });

  await page.locator(`[data-agent="${A1}"]`).click();
  await page.getByRole('tab', { name: /Media/ }).click();
  await sleep(2_000);
  await shot(page, '09-final');
  await app.close();
  app = undefined;
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  for (const ref of [A2, A1]) quiet('destroy', ref, '--force', '--delete-branch');
  quiet('remove', 'hello-stack');
  quiet('daemon', 'stop');
  xvfb?.stop();

  const videoDir = join(work, 'video');
  const video = existsSync(videoDir) ? readdirSync(videoDir).find((f) => f.endsWith('.webm')) : undefined;
  if (video) {
    const mp4 = join(media, 'step-8-media.mp4');
    const gif = join(media, 'step-8-media.gif');
    run('ffmpeg', ['-y', '-loglevel', 'error', '-i', join(videoDir, video), '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '30', '-preset', 'veryfast', '-movflags', '+faststart', mp4]);
    run('ffmpeg', [
      '-y', '-loglevel', 'error', '-i', mp4,
      '-vf', 'setpts=PTS/4,fps=5,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4',
      gif,
    ]);
    log('video: saved an MP4 and a 4x GIF');
  }
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
