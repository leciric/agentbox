#!/usr/bin/env node
// Checks that the Media tab shows the text of what an agent keeps: a log (its
// card and the viewer), a test report that's a single JUnit XML file, and a
// plain file. A real project's evidence found the viewer showing "Failed to fetch"
// for its Maestro report, and log cards empty: the app read their text with
// fetch() from its file:// page, and the media protocol sent no CORS headers.
// State is throwaway.
//
//   mise exec -- node scripts/demo/media-viewer.mjs > .demo-runs/media-viewer/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/media-viewer/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-mv-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-stack');
const A = 'hello-stack/agent-01';
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
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 300)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}

const junit = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="checkout" tests="2" failures="1">
    <testcase classname="checkout" name="adds an item to the cart" time="0.4"/>
    <testcase classname="checkout" name="applies a coupon" time="0.2"><failure message="expected 90, got 100"/></testcase>
  </testsuite>
</testsuites>
`;

let xvfb;
let app;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  mkdirSync(media, { recursive: true });
  run('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  xvfb = await startXvfb({ width: 1440, height: 900 });

  console.log('\n######## 1. The agent keeps a log, a JUnit report and a plain file');
  ab('add', repo);
  ab('create', 'hello-stack', '--ai', 'none', '--title', 'Media text');
  spawnSync(bin, ['exec', A, '--', 'cat > /tmp/junit.xml'], { env, input: junit });
  ab('exec', A, '--', 'printf "server started on :3000\\nGET /health 200\\n" > /tmp/server.log && printf "a plain file the agent kept\\n" > /tmp/notes.txt');
  ab('exec', A, '--', 'agentbox media add /tmp/server.log --kind log --name server-log && agentbox media add /tmp/junit.xml --kind report --name unit-tests && agentbox media add /tmp/notes.txt --kind file --name notes');

  console.log('\n######## 2. The Media tab shows their text');
  app = await electron.launch({ args: [desktop, '--disable-gpu'], cwd: desktop, env: { ...env, DISPLAY: xvfb.display } });
  const page = await app.firstWindow();
  await page.locator(`[data-agent="${A}"]`).click({ timeout: 30_000 });
  await page.getByRole('tab', { name: /Media/ }).click();
  const logCard = page.locator('[data-media-name="server-log"]');
  await logCard.waitFor({ timeout: 30_000 });
  await page.waitForFunction(() => document.querySelector('[data-media-name="server-log"]')?.textContent?.includes('server started on :3000'), null, { timeout: 15_000 }).catch(() => {});
  await sleep(1_000);
  await page.screenshot({ path: join(media, 'media-viewer-01-cards.png') });
  const cardText = await logCard.innerText();
  check("the log's card shows its first lines", cardText.includes('server started on :3000'), cardText);

  const open = async (name, expect, shot) => {
    await page.locator(`[data-media-name="${name}"]`).click();
    const dialog = page.getByRole('dialog');
    await dialog.waitFor();
    await page.waitForFunction((want) => document.querySelector('[role="dialog"]')?.textContent?.includes(want), expect, { timeout: 15_000 }).catch(() => {});
    await sleep(800);
    await page.screenshot({ path: join(media, `media-viewer-${shot}.png`) });
    const text = await dialog.innerText();
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'detached' });
    return text;
  };
  const report = await open('unit-tests', 'applies a coupon', '02-junit-report');
  check('the JUnit report opens with its XML, and its counts', report.includes('applies a coupon') && report.includes('1 passed') && report.includes('1 failed'), report);
  const file = await open('notes', 'a plain file the agent kept', '03-file');
  check('a plain file opens with its text', file.includes('a plain file the agent kept'), file);
  const logText = await open('server-log', 'GET /health 200', '04-log');
  check('the log opens with its text', logText.includes('GET /health 200'), logText);
  check('no viewer said "Failed to fetch"', ![report, file, logText, cardText].some((t) => t.includes('Failed to fetch')));
} catch (err) {
  failed = true;
  console.log(hide(err.stack ?? err));
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  spawnSync(bin, ['destroy', A, '--force', '--delete-branch'], { env });
  spawnSync(bin, ['remove', 'hello-stack'], { env });
  spawnSync(bin, ['daemon', 'stop'], { env });
  xvfb?.stop();
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.length > 0 && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
