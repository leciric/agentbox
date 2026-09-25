#!/usr/bin/env node
// Reproduces the Step 9 evidence: agents of an Android project each get their
// own emulator. The app shows agent-01's emulator live and lets you use it; the
// agent builds and installs the app, tests it with adb while recording, and
// keeps screenshots and logcat in its media; agent-02 runs its own emulator at
// the same time, with its own app data. Records screenshots and a video of the
// app. State is throwaway. Needs the base image, KVM, and an Android SDK with
// the emulator and an x86_64 system image on the host (shared read-only).
//
//   mise exec -- node scripts/demo/step-9.mjs > .demo-runs/step-9/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/step-9/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-step9-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'pawly');
const socket = join(work, 'data/agentbox/run/agentbox.sock');
const A1 = 'pawly/agent-01';
const A2 = 'pawly/agent-02';
const APP = 'dev.agentbox.pawly';
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
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

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}
const quiet = (...args) => hide(spawnSync(bin, args, { env, encoding: 'utf8' }).stdout);

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

// uiText lists the texts on the emulator's screen, from a uiautomator dump.
function uiText(ref) {
  const xml = quiet('exec', ref, '--', 'adb shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1; adb shell cat /sdcard/ui.xml');
  return [...xml.matchAll(/text="([^"]+)"/g)].map((m) => m[1]);
}

const shot = (page, name) => page.screenshot({ path: join(media, `step-9-android-${name}.png`) });
const view = (ref) => `[data-android-view="${ref}"]`;

let xvfb;
let app;
let page;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  mkdirSync(media, { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  cpSync(join(root, 'testdata/fixtures/hello-android'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'Pawly fixture']);
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  xvfb = await startXvfb({ width: 1440, height: 900 });
  log(`built the desktop app; private X display ${xvfb.display}`);

  await step('1. AgentBox recognizes the Android project, and this machine can run emulators', async () => {
    const added = ab('add', repo);
    check('agentbox add says the project is an Android app', added.includes('android') && added.includes('detected'), added);
    const setup = await api('GET', '/v1/setup');
    const android = setup.checks.find((c) => c.id === 'android');
    console.log(`\nSetup check: ${android.status}: ${hide(android.detail)}`);
    check('the Setup check finds KVM and the shared Android SDK', android.status === 'ok', android.detail);
    ab('create', 'pawly', '--ai', 'none', '--title', 'Medication list');
    ab('create', 'pawly', '--ai', 'none', '--title', 'Second device');
  });

  await step("2. Start agent-01's emulator from the app", async () => {
    app = await electron.launch({
      args: [desktop, '--disable-gpu'],
      cwd: desktop,
      env: { ...env, DISPLAY: xvfb.display },
      recordVideo: { dir: join(work, 'video'), size: { width: 1440, height: 900 } },
    });
    page = await app.firstWindow();
    await page.locator(`[data-agent="${A1}"]`).click({ timeout: 30_000 });
    await page.getByRole('tab', { name: /Android/ }).click();
    await page.getByRole('button', { name: 'Start emulator' }).waitFor({ timeout: 30_000 });
    await sleep(1_000);
    await shot(page, '01-off');
    const t0 = Date.now();
    await page.getByRole('button', { name: 'Start emulator' }).click();
    await page.getByText('Starting Android…').first().waitFor({ timeout: 10_000 });
    await sleep(6_000);
    await shot(page, '02-starting');
    await page.locator(`${view(A1)}[data-connected="true"]`).waitFor({ timeout: 180_000 });
    log(`Android booted and the app's view connected in ${((Date.now() - t0) / 1000).toFixed(1)}s`);
    await sleep(3_000);
    await shot(page, '03-home-screen');
    const status = ab('android', 'status', A1);
    check('agent-01 runs a Pixel 7 emulator, and the app shows it live', status.includes('The emulator is running: Pixel 7'), status);
  });

  await step('3. The agent builds the app and installs it on its emulator', async () => {
    const built = ab('exec', A1, '--', 'sudo apt-get update -qq >/dev/null 2>&1; sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openjdk-21-jdk-headless >/dev/null 2>&1; ./build.sh');
    check('build.sh builds build/pawly.apk with the shared SDK', built.includes('built build/pawly.apk'), built);
    const installed = ab('exec', A1, '--', `agentbox android install build/pawly.apk && adb shell am start -W -n ${APP}/.MainActivity | grep -E "Status|Activity"`);
    check('inside the agent, agentbox android install installs it, and adb starts it', installed.includes('Success') && installed.includes('Status: ok'), installed);
    await until('Pawly on the screen', async () => uiText(A1).includes('Medications'), 30_000);
    await sleep(2_000);
    await shot(page, '04-pawly-empty');
  });

  await step('4. Take control in the app: tap and type on the device', async () => {
    await page.locator('#android-control').click();
    await sleep(800);
    const box = await page.locator(`${view(A1)} canvas`).boundingBox();
    // The field sits under the title, about 16% down the screen.
    const nodes = quiet('exec', A1, '--', 'adb shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1; adb shell cat /sdcard/ui.xml');
    const field = /content-desc="Medication name"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/.exec(nodes) ?? /bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"[^>]*content-desc="Medication name"/.exec(nodes);
    const [x1, y1, x2, y2] = field.slice(1).map(Number);
    const px = box.x + (((x1 + x2) / 2) / 1080) * box.width;
    const py = box.y + (((y1 + y2) / 2) / 2400) * box.height;
    await page.mouse.click(px, py);
    await sleep(1_500);
    await page.keyboard.type('Heartgard', { delay: 150 });
    await sleep(800);
    await shot(page, '05-typing');
    await page.keyboard.press('Enter');
    await until('Heartgard in the list', async () => uiText(A1).includes('Heartgard'), 20_000);
    await sleep(1_500);
    await shot(page, '06-added-by-you');
    check('a tap and typing in the app added "Heartgard" in Pawly on the device', true);
    await page.locator('#android-control').click();
  });

  await step('5. The agent tests the app while recording, and keeps proof in its media', async () => {
    const out = ab(
      'exec', A1, '--',
      'agentbox android record start --name add-medication && ./e2e.sh Amoxicillin; ' +
        'agentbox android record stop && agentbox android screenshot --name medication-list && agentbox android logs --package dev.agentbox.pawly',
    );
    check('the e2e check passes on the emulator', out.includes('PASS: Amoxicillin is in the list'), out);
    const items = await api('GET', `/v1/agents/${A1}/media`);
    const recording = items.find((i) => i.name === 'add-medication');
    const screenshot = items.find((i) => i.name === 'medication-list');
    const logcat = items.find((i) => i.kind === 'log');
    check('the recording is a video of the device screen', recording?.kind === 'recording' && recording.meta.duration > 1 && recording.meta.target === 'android', JSON.stringify(recording));
    check('the screenshot is the full 1080×2400 device screen', screenshot?.meta.width === 1080 && screenshot?.meta.height === 2400, JSON.stringify(screenshot));
    const lines = logcat ? readFileSync(logcat.path, 'utf8') : '';
    check("the logcat has Pawly's own lines", /I Pawly\s*: Added medication: Amoxicillin/.test(lines), lines.slice(-600));
    await page.getByRole('tab', { name: /Media/ }).click();
    await page.locator('[data-media="recording"]').first().waitFor();
    await sleep(1_500);
    await shot(page, '07-media');
    await page.locator('[data-media="recording"]').first().click();
    const video = page.getByRole('dialog').locator('video');
    await until('the recording to play', async () => (await video.evaluate((v) => v.currentTime)) > 1, 20_000);
    await shot(page, '08-recording');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    await page.locator('[data-media-name="medication-list"]').click();
    await sleep(1_200);
    await shot(page, '09-screenshot');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });
  });

  await step('6. agent-02 runs its own emulator at the same time, with its own app data', async () => {
    const t0 = Date.now();
    const started2 = ab('android', 'start', A2);
    log(`agent-02's emulator booted in ${((Date.now() - t0) / 1000).toFixed(1)}s, while agent-01's kept running`);
    check("agent-02's emulator starts while agent-01's runs", started2.includes('The emulator is running'), started2);
    const one = quiet('exec', A1, '--', `adb shell pm list packages ${APP}`);
    const two = quiet('exec', A2, '--', `adb shell pm list packages ${APP}; echo "(end)"`);
    console.log(`\nagent-01: ${one.trim() || '(none)'}\nagent-02: ${two.replace('(end)', '').trim() || '(none)'}`);
    check("Pawly is installed on agent-01's device and not on agent-02's", one.includes(APP) && !two.includes(APP), one + two);
    const usage = await api('GET', '/v1/usage?interval=1s');
    for (const a of usage.agents) console.log(`${a.ref}: ${a.cpu.toFixed(0)}% CPU, ${(a.memory / 2 ** 30).toFixed(1)} GiB`);
    await page.locator(`[data-agent="${A2}"]`).click();
    await page.getByRole('tab', { name: /Android/ }).click();
    await page.locator(`${view(A2)}[data-connected="true"]`).waitFor({ timeout: 60_000 });
    await sleep(2_000);
    await shot(page, '10-agent-02');
  });

  await step('7. Stopping the emulator keeps its data', async () => {
    ab('android', 'stop', A1);
    ab('android', 'start', A1);
    quiet('exec', A1, '--', `adb shell am start -W -n ${APP}/.MainActivity`);
    const texts = await until('Pawly after the restart', async () => {
      const t = uiText(A1);
      return t.includes('Medications') && t;
    });
    check('after stopping and starting again, Pawly still lists Heartgard and Amoxicillin', texts.includes('Heartgard') && texts.includes('Amoxicillin'), texts.join(', '));
    await page.locator(`[data-agent="${A1}"]`).click();
    await page.getByRole('tab', { name: /Android/ }).click();
    await page.locator(`${view(A1)}[data-connected="true"]`).waitFor({ timeout: 60_000 });
    await sleep(2_500);
    await shot(page, '11-after-restart');
    await app.close();
    app = undefined;
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  for (const ref of [A2, A1]) {
    quiet('android', 'stop', ref);
    quiet('destroy', ref, '--force', '--delete-branch');
  }
  quiet('remove', 'pawly');
  quiet('daemon', 'stop');
  xvfb?.stop();

  const videoDir = join(work, 'video');
  const video = existsSync(videoDir) ? readdirSync(videoDir).find((f) => f.endsWith('.webm')) : undefined;
  if (video) {
    const mp4 = join(media, 'step-9-android.mp4');
    const gif = join(media, 'step-9-android.gif');
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
