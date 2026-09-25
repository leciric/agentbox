#!/usr/bin/env node
// Drives the AgentBox AppImage inside the new-machine VM, one phase per run, for
// scripts/demo/setup-app.sh. Each phase opens the app the way the user would
// after logging in, does what the Setup page asks, and prints PASS or FAIL
// lines. Screenshots and a video of each phase go to /tmp/setup-app.
//
//   DISPLAY=:99 APPIMAGE=~/Downloads/AgentBox-<version>-x86_64.AppImage node driver.mjs <phase>
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';

const { _electron: electron } = createRequire(import.meta.url)('playwright');
const phase = process.argv[2];
const out = '/tmp/setup-app';
mkdirSync(join(out, 'shots'), { recursive: true });

let failed = 0;
function check(name, ok, detail = '') {
  if (!ok) failed++;
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${ok ? '' : ` (got: ${String(detail).replace(/\s+/g, ' ').slice(0, 400)})`}`);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const shot = (page, name) => page.screenshot({ path: join(out, 'shots', `setup-app-${name}.png`) });
const step = (title) => `[data-setup-step="${title}"]`;
const progress = (page) => page.locator('[data-setup-progress]').getAttribute('data-setup-progress');
const detail = (page, title) => page.locator(`${step(title)} p.text-xs`).first().innerText().catch(() => '');
const statuses = (page) =>
  page.locator('[data-setup-step]').evaluateAll((els) => Object.fromEntries(els.map((el) => [el.getAttribute('data-setup-step'), el.getAttribute('data-status')])));

async function openSetup(page) {
  // The sidebar's item: "Setup", or "Setup needs attention" once the checks load with something missing.
  await page.getByRole('button', { name: /^Setup/ }).click();
  // Setup opens as a wizard while there is setting up left to do; these checks
  // are on the checklist behind it, which shows everything at once.
  const checklist = page.getByRole('button', { name: 'Show the checklist' });
  await checklist.first().click({ timeout: 30_000 }).catch(() => {});
  await page.locator(`${step('Incus')}:not([data-status="checking"])`).waitFor({ timeout: 60_000 });
  await page.locator(`${step('Command-line tool')}:not([data-status="checking"])`).waitFor({ timeout: 30_000 });
}

async function addProject(page, path, name) {
  await page.getByRole('button', { name: 'Add project' }).first().click();
  await page.locator('#project-path').fill(path);
  await page.getByRole('dialog').getByRole('button', { name: 'Add project' }).click();
  await page.getByRole('heading', { name }).waitFor({ timeout: 30_000 });
}

async function newAgent(page, { project, title, ai }) {
  await page.getByRole('button', { name: 'New agent' }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.locator('#agent-project').selectOption(project);
  await dialog.locator('#agent-title').fill(title);
  await dialog.locator(`[data-ai="${ai}"]`).click();
  await dialog.getByRole('button', { name: 'Create agent' }).click();
  await page.locator(`[data-agent="${project}/agent-01"]`).waitFor({ timeout: 240_000 });
}

// showsDevice waits until the Android view shows the phone's screen. Until
// scrcpy's first frame, which can take minutes in the VM just after boot, the
// display is black, or X's two-colour background; a phone's screen has many colours.
async function showsDevice(page, ref, timeout = 5 * 60_000) {
  await page.waitForFunction(
    (sel) => {
      const canvas = document.querySelector(`${sel} canvas`);
      if (!canvas?.width) return false;
      const px = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data;
      const colours = new Set();
      for (let i = 0; i < px.length; i += 4 * 7) colours.add(((px[i] >> 4) << 8) | ((px[i + 1] >> 4) << 4) | (px[i + 2] >> 4));
      return colours.size > 64;
    },
    `[data-android-view="${ref}"]`,
    { timeout, polling: 1_000 },
  );
}

// A first boot in the VM can be slow enough for Android to show "System UI isn't
// responding". dismissANR taps its Wait, through the app, as the user would.
async function dismissANR(page, ref) {
  const windows = execFileSync('agentbox', ['exec', ref, '--', 'adb shell dumpsys window'], { encoding: 'utf8' });
  if (!windows.includes('Application Not Responding')) return;
  await page.locator('#android-control').click();
  const box = await page.locator(`[data-android-view="${ref}"] canvas`).boundingBox();
  await page.mouse.click(box.x + box.width * 0.3, box.y + box.height * 0.57);
  await sleep(1_000);
  await page.locator('#android-control').click();
  console.log(`  Android showed "System UI isn't responding": tapped Wait, in the app`);
}

const phases = {
  // A new user opens the AppImage: every required item is missing.
  async 'first-open'({ app, page }) {
    await page.getByText('Welcome to AgentBox').waitFor({ timeout: 60_000 });
    await sleep(1_500);
    await shot(page, '01-welcome');
    check('the AppImage opens on Welcome, connected to the daemon it started', true);

    await openSetup(page);
    await sleep(2_500);
    const before = await statuses(page);
    for (const title of Object.keys(before)) console.log(`  ${title}: ${before[title]}: ${await detail(page, title)}`);
    await shot(page, '02-setup-new-machine');
    check('on a new machine, no required item is ready: 0 of 4', (await progress(page)) === '0/4', await progress(page));
    check(
      'the command-line tool, Incus, the user mapping, the base image and the Claude Code login are needed',
      ['Command-line tool', 'Incus', 'User mapping', 'Base image', 'Claude Code login'].every((t) => before[t] === 'missing'),
      JSON.stringify(before),
    );
    check('Codex and Android are optional, and preview URLs are on', before['Codex login'] === 'optional' && before['Android emulators'] === 'optional' && before['Preview URLs'] === 'ok', JSON.stringify(before));
    check('Build base image waits for Incus', await page.locator(step('Base image')).getByRole('button', { name: /base image/ }).isDisabled());
    const hints = { incus: await detail(page, 'Incus'), mapping: await detail(page, 'User mapping'), android: await detail(page, 'Android emulators') };
    check(
      "the hints are for a new user: Incus isn't installed, and host setup fixes the user mapping and KVM",
      hints.incus === "Incus isn't installed" && hints.mapping.includes('sudo agentbox host setup') && hints.android.includes('agentbox host setup'),
      JSON.stringify(hints),
    );

    await page.getByRole('button', { name: 'Install command-line tool' }).click();
    await page.locator(`${step('Command-line tool')}[data-status="ok"]`).waitFor({ timeout: 30_000 });
    await sleep(1_500);
    await shot(page, '03-cli-installed');
    check('Install command-line tool: 1 of 4', (await progress(page)) === '1/4', await progress(page));
    const command = await page.locator(`${step('Incus')} span[title]`).first().getAttribute('title');
    writeFileSync(join(out, 'host-setup-command'), command ?? '');
    console.log(`  the Incus step's command: ${command}`);
    check('the Incus step gives the host setup command, finding agentbox on the PATH', command === 'sudo "$(command -v agentbox)" host setup', command);

    // The token claude setup-token prints; setup-app.sh put it here, readable only by this user.
    const tokenFile = join(out, 'claude-token');
    const token = readFileSync(tokenFile, 'utf8').trim();
    rmSync(tokenFile);
    await page.locator(step('Claude Code login')).scrollIntoViewIfNeeded();
    await page.locator('input[aria-label="Claude Code token"]').fill(token);
    await sleep(600);
    await shot(page, '04-claude-token');
    await page.locator(step('Claude Code login')).getByRole('button', { name: 'Save' }).click();
    await page.locator(`${step('Claude Code login')}[data-status="ok"]`).waitFor({ timeout: 30_000 });
    check('pasting the token and saving it: the Claude Code login is ready', true);

    await page.locator(step('Codex login')).getByRole('button', { name: 'Copy the command' }).click();
    const copied = await app.evaluate(({ clipboard }) => clipboard.readText());
    check('the Codex step copies agentbox auth codex', copied === 'agentbox auth codex', copied);
    const android = await page.locator(`${step('Android emulators')} span[title]`).first().getAttribute('title');
    writeFileSync(join(out, 'android-command'), android ?? '');
    console.log(`  the Android step's command: ${android}`);
    check('the Android step gives the sdkmanager command', /^sdkmanager "emulator" "platform-tools" "system-images;android-\d+;google_apis;x86_64"$/.test(android ?? ''), android);
    await page.locator(step('Preview URLs')).scrollIntoViewIfNeeded();
    await sleep(800);
    await shot(page, '05-setup-optional-items');
  },

  // Run in a session from before host setup: the user is in incus-admin, but
  // this session isn't. The ACL host setup puts on the Incus socket goes by
  // UID, so it applies here anyway — which is why there is no logout step.
  async 'same-session-as-host-setup'({ page }) {
    await openSetup(page);
    await page.locator(`${step('Incus')}[data-status="ok"]`).waitFor({ timeout: 60_000 });
    await sleep(1_500);
    await shot(page, '06-ready-without-logging-out');
    const incus = await detail(page, 'Incus');
    console.log(`  Incus: ${incus}`);
    check('after host setup, the session that ran it can use Incus: no logout', incus.includes('your user can use it'), incus);
    const s = await statuses(page);
    check('the user mapping is ready as well', s['User mapping'] === 'ok', JSON.stringify(s));
    const groups = execFileSync('id', ['-nG'], { encoding: 'utf8' });
    check('and this session is not in incus-admin, so it is the ACL doing it', !/\bincus-admin\b/.test(groups), groups);
  },

  // A new login: the app restarts the daemon the first session started, then builds the base image.
  async 'base-image'({ page }) {
    await openSetup(page);
    await page.locator(`${step('Incus')}[data-status="ok"]`).waitFor({ timeout: 90_000 });
    await sleep(1_500);
    await shot(page, '07-incus-ready');
    check('after logging in again, Incus and the user mapping are ready: 3 of 4', (await progress(page)) === '3/4', await progress(page));
    await page.locator(step('Base image')).getByRole('button', { name: 'Build base image' }).click();
    await page.locator(step('Base image')).getByLabel('Job log').waitFor({ timeout: 60_000 });
    await sleep(60_000);
    await shot(page, '08-image-building');
    const t0 = Date.now();
    await page.locator(`${step('Base image')}[data-status="ok"]`).waitFor({ timeout: 40 * 60_000 });
    console.log(`  the base image was ready ${((Date.now() - t0) / 1000 + 60).toFixed(0)}s after Build base image`);
    // The job's status badge, in the Base image step.
    const badge = page.locator(`${step('Base image')} [data-job] [data-job-status]`);
    await page.locator(`${step('Base image')} [data-job] [data-job-status]:not([data-job-status="running"])`).waitFor({ timeout: 60_000 });
    await sleep(2_000);
    await shot(page, '09-image-ready');
    check('Build base image, in the app: the base image is ready, 4 of 4', (await progress(page)) === '4/4', await progress(page));
    const status = await badge.getAttribute('data-job-status');
    const job = await page.locator(`${step('Base image')} [data-job]`).innerText();
    check('the image-build job succeeded, with no error', status === 'succeeded', `${status}: ${job.slice(-300)}`);
  },

  async 'first-agent'({ page }) {
    await addProject(page, '/home/dev/hello-stack', 'hello-stack');
    await sleep(1_000);
    await shot(page, '10-project');
    await newAgent(page, { project: 'hello-stack', title: 'First agent', ai: 'claude' });
    await page.waitForFunction(() => [...document.querySelectorAll('.xterm-rows')].some((el) => /Claude Code/.test(el.textContent ?? '')), null, { timeout: 240_000 });
    await sleep(2_500);
    await shot(page, '11-claude-code');
    check('New agent with Claude Code: its terminal opens with Claude Code running', true);
    await page.getByRole('tab', { name: /Browser/ }).click();
    await page.locator('[data-browser-view="hello-stack/agent-01"][data-connected="true"]').waitFor({ timeout: 120_000 });
    await sleep(2_500);
    await shot(page, '12-browser');
    check("the agent's browser shows in the Browser tab", true);
    await page.getByRole('tab', { name: /Overview/ }).click();
    const url = await page.locator('[data-preview-url]').getAttribute('data-preview-url', { timeout: 30_000 });
    writeFileSync(join(out, 'preview-url'), url ?? '');
    await sleep(1_500);
    await shot(page, '13-overview');
    check('the Overview tab gives the preview URL for port 3000', /^http:\/\/3000\.agent-01\.hello-stack\.localhost:\d+$/.test(url ?? ''), url);
  },

  // After the Codex login and the Android SDK, in a terminal.
  async 'optional-items'({ page }) {
    await openSetup(page);
    await page.locator(`${step('Codex login')}[data-status="ok"]`).waitFor({ timeout: 60_000 });
    await page.locator(`${step('Android emulators')}[data-status="ok"]`).waitFor({ timeout: 60_000 });
    await sleep(1_500);
    await shot(page, '14-setup-ready');
    await page.locator(step('Preview URLs')).scrollIntoViewIfNeeded();
    await sleep(800);
    await shot(page, '15-setup-ready-bottom');
    const s = await statuses(page);
    for (const title of Object.keys(s)) console.log(`  ${title}: ${s[title]}: ${await detail(page, title)}`);
    check('every item on the Setup page is ready', Object.values(s).every((v) => v === 'ok'), JSON.stringify(s));

    await addProject(page, '/home/dev/hello-android', 'hello-android');
    await newAgent(page, { project: 'hello-android', title: 'Android check', ai: 'none' });
    await page.locator('[data-agent="hello-android/agent-01"]').click();
    await page.getByRole('tab', { name: /Android/ }).click();
    await page.getByRole('button', { name: 'Start emulator' }).click({ timeout: 60_000 });
    const t0 = Date.now();
    await page.locator('[data-android-view="hello-android/agent-01"][data-connected="true"]').waitFor({ timeout: 15 * 60_000 });
    console.log(`  the emulator booted, nested in the VM, in ${((Date.now() - t0) / 1000).toFixed(0)}s`);
    check("an Android project's agent starts its emulator from the app, in the VM", true);
    await showsDevice(page, 'hello-android/agent-01');
    console.log(`  the Android tab showed the device ${((Date.now() - t0) / 1000).toFixed(0)}s after Start emulator`);
    await dismissANR(page, 'hello-android/agent-01');
    await sleep(5_000);
    // Still there once the view has settled, and in the screenshot.
    await showsDevice(page, 'hello-android/agent-01', 10_000);
    await shot(page, '16-android-in-the-vm');
    check("the Android tab shows the device's screen", true);
  },

  // setup-app.sh started another program on the preview port, and restarted the daemon.
  async 'preview-off'({ page }) {
    await openSetup(page);
    await page.locator(`${step('Preview URLs')}[data-status="optional"]`).waitFor({ timeout: 60_000 });
    await page.locator(step('Preview URLs')).scrollIntoViewIfNeeded();
    await sleep(1_000);
    await shot(page, '17-preview-off');
    const text = await detail(page, 'Preview URLs');
    check('with another program on the preview port, Preview URLs says they are off, and why', text.includes('another program uses the port'), text);
  },

  async 'preview-on'({ page }) {
    await openSetup(page);
    await page.locator(`${step('Preview URLs')}[data-status="ok"]`).waitFor({ timeout: 60_000 });
    await page.locator(step('Preview URLs')).scrollIntoViewIfNeeded();
    await sleep(1_000);
    await shot(page, '18-preview-on');
    check('with the port free again, Preview URLs are on', true);
  },
};

if (!phases[phase]) {
  console.error(`unknown phase ${phase}: ${Object.keys(phases).join(', ')}`);
  process.exit(2);
}
const app = await electron.launch({
  executablePath: process.env.APPIMAGE,
  args: ['--disable-gpu'],
  env: { ...process.env },
  recordVideo: { dir: join(out, `video-${Date.now()}-${phase}`), size: { width: 1440, height: 900 } },
});
const page = await app.firstWindow();
try {
  await page.locator('[data-connection="connected"]').waitFor({ timeout: 120_000 });
  await phases[phase]({ app, page });
} catch (err) {
  check(`${phase}: ${err.message.split('\n')[0]}`, false, err.stack);
  await shot(page, `failed-${phase}`).catch(() => {});
} finally {
  await app.close().catch(() => {});
}
process.exitCode = failed ? 1 : 0;
