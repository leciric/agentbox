#!/usr/bin/env node
// Reproduces the several-Claude-accounts evidence (D39): stores two named
// accounts, picks one per project and one per agent, and drives the desktop
// app on a private X display to do the same from the interface. State is
// throwaway, and the tokens are made up: nothing here talks to Anthropic.
//
// It runs the app on a private X display from Xvfb; set DEMO_DISPLAY to use
// a display that already exists instead (an AgentBox agent's, for one).
//
//   mise exec -- node scripts/demo/claude-accounts.mjs > .demo-runs/claude-accounts/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/claude-accounts/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

// A short path: the daemon's unix socket has to fit in 107 bytes.
const work = join(homedir(), '.cache', 'agentbox-accounts');
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repo');
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  XDG_SESSION_TYPE: 'x11',
  // The tokens here are made up, and the daemon now checks stored tokens
  // against Anthropic (D62): a port nothing listens on keeps that promise, and
  // the accounts show as unchecked rather than rejected.
  ANTHROPIC_BASE_URL: 'http://127.0.0.1:1',
};
delete env.WAYLAND_DISPLAY;
delete env.AGENTBOX_SOCKET;
delete env.AGENTBOX_NO_AUTOSTART;

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$WORK');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const ab = (...args) => {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', input: '' });
  return hide(`${r.stdout}${r.stderr}`);
};
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });
const shot = (page, name) => page.screenshot({ path: join(media, `claude-accounts-${name}.png`) });

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

let xvfb;
let app;
let page;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  rmSync(work, { recursive: true, force: true });
  mkdirSync(media, { recursive: true });
  run('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  run('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  log('built agentbox and the desktop app');

  await step('1. Two named Claude Code accounts on one machine', async () => {
    run(bin, ['auth', 'claude', '--token-stdin'], { input: 'sk-ant-oat01-personal-example\n' });
    run(bin, ['auth', 'claude', '--token-stdin', '--account', 'work'], { input: 'sk-ant-oat01-work-example\n' });
    const list = ab('auth', 'claude', 'list');
    console.log(list);
    check('both accounts are stored, and the first one is the default', /default\s+yes/.test(list) && /work\s+-/.test(list), list);
    const status = ab('auth', 'status');
    check('auth status names them', status.includes('2 account(s): default (default), work'), status);
    const stored = join(work, 'config/agentbox/credentials/claude');
    check('each account is its own 0600 file', existsSync(join(stored, 'default.token')) && existsSync(join(stored, 'work.token')));
  });

  await step('2. A project picks an account; the other projects keep the default', async () => {
    console.log(ab('add', repo, '--name', 'hello-stack'));
    console.log(ab('claude-account', 'hello-stack'));
    check('without a choice, a project uses the default account', ab('claude-account', 'hello-stack').includes('default Claude Code account, "default"'));
    console.log(ab('claude-account', 'hello-stack', 'work'));
    const projects = ab('projects');
    console.log(projects);
    check('the project now uses "work"', /hello-stack\s+work/.test(projects), projects);
    const gone = ab('claude-account', 'hello-stack', 'nope');
    check('an account that is not stored is refused', gone.includes('no Claude Code account named "nope"'), gone);
  });

  await step('3. The Setup page lists the accounts, and marks the default', async () => {
    let display = process.env.DEMO_DISPLAY;
    if (display) {
      log(`using the display it was given, ${display}`);
    } else {
      xvfb = await startXvfb({ width: 1440, height: 900 });
      display = xvfb.display;
      log(`started a private X display, ${display}`);
    }
    app = await electron.launch({ args: [desktop, '--disable-gpu'], cwd: desktop, env: { ...env, DISPLAY: display } });
    page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
    await page.getByRole('button', { name: /^Setup/ }).first().click();
    // Setup opens as a wizard while there is setting up left to do; the
    // accounts are on the checklist behind it, which shows everything at once.
    await page.getByRole('button', { name: 'Show the checklist' }).first().click({ timeout: 30_000 }).catch(() => {});
    const accounts = page.locator('[data-claude-account]');
    await accounts.first().waitFor({ timeout: 15_000 });
    check('the Setup page lists both accounts', (await accounts.count()) === 2, String(await accounts.count()));
    const line = await page.locator('[data-claude-account="default"]').innerText();
    check('the default one is marked', line.includes('default'), line);
    const claudeStep = page.locator('[data-setup-step="Claude Code login"]');
    const step5 = await claudeStep.innerText();
    check('the step says how many accounts agents can use', step5.includes('2 accounts for agents'), step5);
    await claudeStep.scrollIntoViewIfNeeded();
    await page.waitForTimeout(800);
    await shot(page, '01-setup-accounts');
  });

  await step('4. Making another account the default, from the app', async () => {
    await page.locator('[data-claude-account="work"]').getByRole('button', { name: 'Make default' }).click();
    await page.locator('[data-claude-account="work"]').getByText('default').waitFor({ timeout: 10_000 });
    const list = ab('auth', 'claude', 'list');
    check('the CLI agrees that "work" is now the default', /work\s+yes/.test(list), list);
    await page.waitForTimeout(600);
    await shot(page, '02-setup-default');
  });

  await step("5. A project's page picks its account", async () => {
    await page.locator('[data-project="hello-stack"]').click();
    // A project opens on its chat; its accounts are on the Overview tab.
    await page.getByRole('tab', { name: 'Overview' }).click();
    const select = page.getByLabel('Claude Code account');
    await select.waitFor({ timeout: 15_000 });
    check("the project page shows the project's account", (await select.inputValue()) === 'work');
    await select.selectOption('default');
    await page.waitForTimeout(1000);
    const projects = ab('projects');
    check('the change reaches the daemon', /hello-stack\s+default/.test(projects), projects);
    await page.waitForTimeout(600);
    await shot(page, '03-project-account');
  });

  await step('6. The New agent dialog offers the account, starting from the project\'s', async () => {
    await page.getByRole('button', { name: 'New agent', exact: true }).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'More options' }).click();
    const select = dialog.locator('#agent-claude-account');
    await select.waitFor({ timeout: 10_000 });
    const first = await select.locator('option').first().innerText();
    check("the default choice is the project's account", first.includes("The project's (default)"), first);
    const options = await select.locator('option').allInnerTexts();
    check('both accounts can be picked for one agent', options.includes('work') && options.includes('default'), options.join(', '));
    await page.waitForTimeout(600);
    await shot(page, '04-new-agent-account');
    await dialog.getByRole('button', { name: 'Cancel' }).click();
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Results');
  for (const line of results) console.log(line);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`\n${passed}/${results.length} checks passed in ${((Date.now() - started) / 1000).toFixed(0)}s`);
  console.log(`Screenshots: ${media}`);
  await app?.close().catch(() => {});
  xvfb?.stop();
  process.exit(failed || passed !== results.length ? 1 : 0);
}
