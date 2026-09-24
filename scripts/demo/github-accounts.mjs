#!/usr/bin/env node
// Reproduces the several-GitHub-accounts evidence: stores two named accounts,
// gives one to each of two projects, and drives the desktop app on a private X
// display to do the same from the interface. State is throwaway, and GitHub is
// a stub in its own process, so nothing reaches the real one: the stub knows
// two tokens and answers with a different login for each, which is what makes
// "this project's agents use that account" visible.
//
// It runs the app on a private X display from Xvfb; set DEMO_DISPLAY to use
// a display that already exists instead (an AgentBox agent's, for one).
//
//   mise exec -- node scripts/demo/github-accounts.mjs > .demo-runs/github-accounts/demo.log 2>&1
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/github-accounts/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

// A short path: the daemon's unix socket has to fit in 107 bytes.
const work = join(homedir(), '.cache', 'agentbox-gh-accounts');
const bin = join(work, 'bin', 'agentbox');
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  XDG_SESSION_TYPE: 'x11',
};
delete env.WAYLAND_DISPLAY;
delete env.AGENTBOX_SOCKET;
delete env.AGENTBOX_NO_AUTOSTART;
delete env.GH_TOKEN;
delete env.GITHUB_TOKEN;

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
const shot = (page, name) => page.screenshot({ path: join(media, `github-accounts-${name}.png`) });

// A repository for a project, so the daemon has something real to register.
// An origin makes it a GitHub repository, which is what the Pull requests tab
// reads — with the project's account, and no other.
function repoFor(name, origin) {
  const path = join(work, name);
  cpSync(join(root, 'testdata/fixtures/hello-stack'), path, { recursive: true });
  run('git', ['-C', path, 'init', '-q', '-b', 'main']);
  run('git', ['-C', path, 'add', '-A']);
  run('git', ['-C', path, '-c', 'commit.gpgsign=false', 'commit', '-qm', `${name} fixture`]);
  if (origin) run('git', ['-C', path, 'remote', 'add', 'origin', origin]);
  return path;
}

function fakeGitHub() {
  const script = join(work, 'github.mjs');
  writeFileSync(script, `${serveGitHub.toString()}\nserveGitHub();\n`);
  const proc = spawn(process.execPath, [script], { stdio: ['ignore', 'pipe', 'inherit'] });
  return new Promise((done, fail) => {
    proc.stdout.once('data', (chunk) => done({ proc, url: String(chunk).trim() }));
    proc.once('error', fail);
  });
}

// serveGitHub is stringified into that process, so it must not close over
// anything here. Each token is a different GitHub user, which is the whole
// point: the account a project picks decides who its agents are on GitHub.
//
// acme/app is a private repository that only the personal account is in. GitHub
// answers 404 for a repository an account can't see — it hides it rather than
// admitting it exists — so the other account gets exactly what a real second
// account gets, and the tab has to say something useful about it.
function serveGitHub() {
  const http = process.getBuiltinModule('node:http');
  const users = { 'personal-token': 'demo-personal', 'work-token': 'demo-work' };
  const pr = {
    number: 7,
    title: 'Reminders page',
    state: 'open',
    html_url: 'https://github.com/acme/app/pull/7',
    draft: false,
    comments: 2,
    additions: 34,
    deletions: 3,
    updated_at: new Date().toISOString(),
    base: { ref: 'main' },
    head: { ref: 'agentbox/agent-01', sha: 'abc' },
  };
  const srv = http.createServer((req, res) => {
    const login = users[String(req.headers.authorization).replace('Bearer ', '')];
    const path = String(req.url).split('?')[0];
    res.setHeader('Content-Type', 'application/json');
    if (!login) return void res.writeHead(401).end(JSON.stringify({ message: 'Bad credentials' }));
    if (path === '/user') return void res.end(JSON.stringify({ login }));
    if (path.startsWith('/repos/acme/app')) {
      if (login !== 'demo-personal') return void res.writeHead(404).end(JSON.stringify({ message: 'Not Found' }));
      if (path === '/repos/acme/app') {
        return void res.end(
          JSON.stringify({ default_branch: 'main', allow_merge_commit: true, allow_squash_merge: true, allow_rebase_merge: true, permissions: { push: true } }),
        );
      }
      if (path === '/repos/acme/app/pulls') return void res.end(JSON.stringify([pr]));
      if (path === '/repos/acme/app/pulls/7') return void res.end(JSON.stringify(pr));
      if (path.endsWith('/check-runs')) return void res.end(JSON.stringify({ total_count: 1, check_runs: [{ status: 'completed', conclusion: 'success' }] }));
    }
    res.writeHead(404).end('{}');
  });
  srv.listen(0, '127.0.0.1', () => process.stdout.write(`http://127.0.0.1:${srv.address().port}\n`));
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

let xvfb;
let app;
let page;
let gh;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  rmSync(work, { recursive: true, force: true });
  mkdirSync(media, { recursive: true });
  run('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  const repos = { pawly: repoFor('pawly', 'https://github.com/acme/app.git'), 'hello-stack': repoFor('hello-stack'), notes: repoFor('notes') };
  // One Claude Code account, which is no choice at all: the Add project dialog
  // leaves that select out and only offers the GitHub one.
  mkdirSync(join(work, 'config/agentbox/credentials/claude'), { recursive: true });
  writeFileSync(join(work, 'config/agentbox/credentials/claude/personal.token'), 'sk-ant-oat01-demo\n', { mode: 0o600 });
  run('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  gh = await fakeGitHub();
  env.AGENTBOX_GITHUB_API = gh.url;
  log('built agentbox and the desktop app; a stub GitHub at', gh.url);

  await step('1. Two named GitHub accounts on one machine', async () => {
    const personal = run(bin, ['auth', 'github', '--token-stdin', '--account', 'personal'], { input: 'personal-token\n' });
    console.log(hide(personal));
    check('the first account is stored, as the GitHub user its token belongs to', personal.includes('as demo-personal'), personal);
    const rejected = hide(spawnSync(bin, ['auth', 'github', '--token-stdin', '--account', 'work'], { env, encoding: 'utf8', input: 'not-a-real-token\n' }).stderr);
    check('a token GitHub refuses is not stored', rejected.includes('401'), rejected);
    check('and it left no account behind', !existsSync(join(work, 'config/agentbox/credentials/github/work.token')));
    const second = run(bin, ['auth', 'github', '--token-stdin', '--account', 'work'], { input: 'work-token\n' });
    console.log(hide(second));
    check('the second account is a different GitHub user', second.includes('as demo-work'), second);

    const list = ab('auth', 'github', 'list');
    console.log(list);
    check('both accounts are stored, and the first one is the default', /personal\s+yes/.test(list) && /work\s+-/.test(list), list);
    const status = ab('auth', 'status');
    console.log(status);
    check('auth status names them', status.includes('2 account(s): personal (default), work'), status);
    const stored = join(work, 'config/agentbox/credentials/github');
    const modes = ['personal', 'work'].map((a) => (statSync(join(stored, `${a}.token`)).mode & 0o777).toString(8));
    check('each account is its own 0600 file', modes.join(',') === '600,600', modes.join(','));
  });

  await step('2. One project uses one account, another project the other', async () => {
    console.log(ab('add', repos.pawly, '--name', 'pawly'));
    console.log(ab('add', repos['hello-stack'], '--name', 'hello-stack'));
    check('without a choice, a project uses the default account', ab('github-account', 'pawly').includes('default GitHub account, "personal"'));
    console.log(ab('github-account', 'pawly', 'work'));
    const projects = ab('projects');
    console.log(projects);
    check("pawly's agents use \"work\"", /pawly\s+-\s+work/.test(projects), projects);
    check("hello-stack's agents keep the default", /hello-stack\s+-\s+-/.test(projects), projects);
    const gone = ab('github-account', 'pawly', 'nope');
    check('an account that is not stored is refused', gone.includes('no GitHub account named "nope"'), gone);
    const created = ab('create', 'pawly', '--ai', 'none', '--github-account', 'nope');
    check('so is one asked for when the agent is created', created.includes('no GitHub account named "nope"'), created);
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
    const accounts = page.locator('[data-github-account]');
    await accounts.first().waitFor({ timeout: 15_000 });
    check('the Setup page lists both accounts', (await accounts.count()) === 2, String(await accounts.count()));
    const line = await page.locator('[data-github-account="personal"]').innerText();
    check('the default one is marked', line.includes('default'), line);
    const githubStep = page.locator('[data-setup-step="GitHub for agents"]');
    const detail = await githubStep.innerText();
    check('the step says who agents are on GitHub by default', detail.includes('Agents use GitHub as demo-personal by default'), detail);
    await githubStep.scrollIntoViewIfNeeded();
    await page.waitForTimeout(800);
    await shot(page, '01-setup-accounts');
  });

  await step('4. Making another account the default, from the app', async () => {
    await page.locator('[data-github-account="work"]').getByRole('button', { name: 'Make default' }).click();
    await page.locator('[data-github-account="work"]').getByText('default').waitFor({ timeout: 10_000 });
    const list = ab('auth', 'github', 'list');
    check('the CLI agrees that "work" is now the default', /work\s+yes/.test(list), list);
    const githubStep = page.locator('[data-setup-step="GitHub for agents"]');
    await githubStep.getByText('Agents use GitHub as demo-work by default').waitFor({ timeout: 20_000 });
    check('the Setup page now names the other GitHub user', true);
    await page.waitForTimeout(600);
    await shot(page, '02-setup-default');
  });

  await step("5. A project's page picks its account", async () => {
    await page.locator('[data-project="pawly"]').click();
    await page.getByRole('tab', { name: 'Overview' }).click();
    const select = page.getByLabel('GitHub account');
    await select.waitFor({ timeout: 15_000 });
    check("the project page shows the project's account", (await select.inputValue()) === 'work', await select.inputValue());
    await page.waitForTimeout(600);
    await shot(page, '03-project-account');
    await select.selectOption('personal');
    await page.waitForTimeout(1000);
    const projects = ab('projects');
    check('the change reaches the daemon', /pawly\s+-\s+personal/.test(projects), projects);
    await select.selectOption('work');
    await page.waitForTimeout(1000);
  });

  await step("6. The New agent dialog offers the account, starting from the project's", async () => {
    await page.getByRole('button', { name: 'New agent', exact: true }).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('#agent-project').selectOption('pawly');
    await dialog.getByRole('button', { name: 'More options' }).click();
    const select = dialog.locator('#agent-github-account');
    await select.waitFor({ timeout: 10_000 });
    const first = await select.locator('option').first().innerText();
    check("the default choice is the project's account", first.includes("The project's (work)"), first);
    const options = await select.locator('option').allInnerTexts();
    check('both accounts can be picked for one agent', options.includes('work') && options.includes('personal'), options.join(', '));
    await page.waitForTimeout(600);
    await shot(page, '04-new-agent-account');
    await dialog.getByRole('button', { name: 'Cancel' }).click();
  });

  await step("7. The Add project dialog picks the new project's accounts", async () => {
    await page.getByRole('button', { name: 'Add project' }).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('#project-path').fill(repos.notes);
    const github = dialog.locator('#project-github-account');
    await github.waitFor({ timeout: 10_000 });
    const options = await github.locator('option').allInnerTexts();
    check('the dialog offers the GitHub accounts, by the GitHub user each one is', options.includes('work (demo-work)') && options.includes('personal (demo-personal)'), options.join(', '));
    check("the default choice is the machine's default account", options[0] === 'Default (work)', options[0]);
    check('with one Claude Code account there is nothing to choose, so that select is left out', (await dialog.locator('#project-claude-account').count()) === 0);
    await github.selectOption('personal');
    await page.waitForTimeout(600);
    await shot(page, '05-add-project-accounts');
    await dialog.getByRole('button', { name: 'Add project' }).click();

    await page.getByRole('tab', { name: 'Overview' }).click();
    const select = page.getByLabel('GitHub account');
    await select.waitFor({ timeout: 15_000 });
    check('the project page opens on what was chosen for it', (await select.inputValue()) === 'personal', await select.inputValue());
    const projects = ab('projects');
    check('the project was added on that account, not fixed afterwards', /notes\s+-\s+personal/.test(projects), projects);
    const refused = ab('add', repos.notes, '--name', 'nope', '--github-account', 'missing');
    check('an account that is not stored is refused before the project exists', refused.includes('no GitHub account named "missing"'), refused);
  });

  await step("8. The Pull requests tab says which account it reads as, and why it can't", async () => {
    // pawly pushes to acme/app, a private repository the "work" account isn't
    // in: GitHub answers 404, and the tab used to show nothing at all.
    await page.locator('[data-project="pawly"]').click();
    await page.getByRole('tab', { name: 'Pull requests' }).click();
    const notice = page.getByRole('alert');
    await notice.waitFor({ timeout: 20_000 });
    const text = (await notice.innerText()).replace(/\s+/g, ' ');
    check('it names the account, who that is on GitHub, and the repository', text.includes("The account work (demo-work) can't see acme/app."), text);
    check('and what would fix it', text.includes('Pick another account for this project') && text.includes('add one in Setup'), text);
    await page.waitForTimeout(600);
    await shot(page, '06-pulls-no-access');

    // The fix is a link: it opens the project's account setting.
    await notice.getByRole('button', { name: 'Pick another account for this project' }).click();
    const select = page.getByLabel('GitHub account');
    await select.waitFor({ timeout: 10_000 });
    check("the sentence's link opens the project's account setting", true);
    await select.selectOption('personal');
    await page.getByRole('tab', { name: 'Pull requests' }).click();
    const pr = page.locator('[data-pull-request="7"]');
    await pr.waitFor({ timeout: 20_000 });
    check("the other account sees the repository, so its pull requests are there", (await pr.innerText()).includes('Reminders page'), await pr.innerText());
    const header = await page.locator('[data-pulls-account]').innerText();
    check('and the tab says who it is reading them as', header.replace(/\s+/g, ' ').includes('acme/app as demo-personal (personal)'), header);
    await page.waitForTimeout(600);
    await shot(page, '07-pulls-account');
  });

  await step('9. A machine set up before named accounts keeps its token', async () => {
    // The single-token layout: one credentials/github.token, no accounts.
    const older = { ...env, XDG_CONFIG_HOME: join(work, 'old-config'), XDG_DATA_HOME: join(work, 'old-data') };
    const credentials = join(work, 'old-config/agentbox/credentials');
    mkdirSync(credentials, { recursive: true });
    writeFileSync(join(credentials, 'github.token'), 'personal-token\n', { mode: 0o600 });
    const list = hide(spawnSync(bin, ['auth', 'github', 'list'], { env: older, encoding: 'utf8' }).stdout);
    console.log(list);
    check('the old token becomes the account "default"', /default\s+yes/.test(list), list);
    const moved = join(credentials, 'github/default.token');
    check('it is moved into the account store, and the old file is gone', existsSync(moved) && !existsSync(join(credentials, 'github.token')));
    check('its token is untouched', readFileSync(moved, 'utf8').trim() === 'personal-token');
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
  spawnSync(bin, ['daemon', 'stop'], { env, encoding: 'utf8' });
  gh?.proc.kill();
  xvfb?.stop();
  process.exit(failed || passed !== results.length ? 1 : 0);
}
