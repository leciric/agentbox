#!/usr/bin/env node
// Reproduces three things added on top of the project chat:
//   - the fleet: following a project's agents without opening each one, with
//     what they've changed, shown, and where their branch stands on GitHub;
//   - sharing one GitHub token with every agent, so gh works inside them;
//   - a project's media in one stream, labelled with the agent each item came
//     from and filterable by agent and by kind.
// State is throwaway, and GitHub is a stub, so nothing reaches the real one.
//
//   mise exec -- node scripts/demo/fleet.mjs > .demo-runs/fleet/demo.log 2>&1
//
// Set NO_APP=1 to skip the screenshots and the video.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/fleet/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-fleet-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-fleet');
const P = 'hello-fleet';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  // A stub incus, because this runs inside an AgentBox agent, which has no
  // Incus of its own. The fleet reads git, the store and GitHub, not incus.
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY']) delete env[name];

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$TMP');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 120_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}
const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim();
const commit = (dir, msg) =>
  execFileSync('git', ['-C', dir, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', 'commit', '-q', '-m', msg], { encoding: 'utf8' });

// fakeGitHub stands in for api.github.com: one open pull request with a failing
// check for agent-01, none for agent-02, and a login for the token check.
//
// It runs in its own process, because this script drives the CLI with
// spawnSync, which blocks this one's event loop: a server here would never
// answer while a command runs.
function fakeGitHub() {
  const script = join(work, 'github.mjs');
  const calls = join(work, 'github-calls.log');
  writeFileSync(script, `${serveGitHub.toString()}
serveGitHub(${JSON.stringify(calls)});
`);
  const proc = spawn(process.execPath, [script], { stdio: ['ignore', 'pipe', 'inherit'] });
  return new Promise((done, fail) => {
    proc.stdout.once('data', (chunk) => done({
      proc,
      url: String(chunk).trim(),
      calls: () => (existsSync(calls) ? readFileSync(calls, 'utf8').trim().split('\n').filter(Boolean) : []),
    }));
    proc.once('error', fail);
  });
}

// serveGitHub is stringified into that process, so it must not close over
// anything here.
function serveGitHub(callsFile) {
  const http = process.getBuiltinModule('node:http');
  const { appendFileSync } = process.getBuiltinModule('node:fs');
  const srv = http.createServer((req, res) => {
    appendFileSync(callsFile, `${req.method} ${req.url}\n`);
    res.setHeader('Content-Type', 'application/json');
    if (req.headers.authorization !== 'Bearer demo-token') {
      res.writeHead(401).end(JSON.stringify({ message: 'Bad credentials' }));
      return;
    }
    if (req.url === '/user') return void res.end(JSON.stringify({ login: 'demo-user' }));
    if (req.url.includes('/pulls')) {
      if (!req.url.includes('agentbox%2Fagent-01')) return void res.end('[]');
      return void res.end(
        JSON.stringify([
          {
            number: 42,
            title: 'Add the reminders page',
            state: 'open',
            html_url: 'https://github.com/acme/hello-fleet/pull/42',
            draft: false,
            comments: 3,
            updated_at: new Date().toISOString(),
            head: { sha: 'deadbeef' },
          },
        ]),
      );
    }
    if (req.url.includes('/check-runs')) {
      return void res.end(
        JSON.stringify({
          total_count: 2,
          check_runs: [
            { status: 'completed', conclusion: 'success' },
            { status: 'completed', conclusion: 'failure' },
          ],
        }),
      );
    }
    res.writeHead(404).end('{}');
  });
  srv.listen(0, '127.0.0.1', () => process.stdout.write(`http://127.0.0.1:${srv.address().port}\n`));
}

// agent makes a ready agent with a worktree, without needing a machine.
function agent(name, title) {
  const worktree = join(work, 'data/agentbox/worktrees', P, name);
  git(repo, 'worktree', 'add', '--quiet', '-b', `agentbox/${name}`, worktree, 'HEAD');
  const sql =
    `INSERT INTO agents (project,name,instance,ai,autonomous,branch,base_ref,base_commit,worktree,status,created_at,source,title,claude_account,interface,role) ` +
    `VALUES ('${P}','${name}','ab-${P}-${name}','claude',0,'agentbox/${name}','main','${git(repo, 'rev-parse', 'HEAD')}','${worktree}','ready',${Math.floor(Date.now() / 1000)},'','${title}','','chat','worker');`;
  execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), sql]);
  return worktree;
}

function addMedia(name, agentName, kind, title) {
  const id = `${agentName}-${kind}-${Math.random().toString(16).slice(2, 8)}`;
  const sql = `INSERT INTO media (id,project,agent,kind,name,file,mime,size,sha256,source,text,meta,created_at) VALUES ('${id}','${P}','${agentName}','${kind}','${name}','','',0,'','agent','${title}','{}',${Date.now()});`;
  execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), sql]);
}

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  mkdirSync(join(work, 'stub'), { recursive: true });
  const instances = ['agent-01', 'agent-02'].map((n) => `{"name":"ab-${P}-${n}","status":"Running"}`).join(',');
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/bin/sh\ncase "$1" in list) echo '[${instances}]' ;; query) echo '[]' ;; esac\nexit 0\n`,
    { mode: 0o755 },
  );

  const gh = await fakeGitHub();
  env.AGENTBOX_GITHUB_API = gh.url;
  log('A stub GitHub at', gh.url);

  log('Making a project with a GitHub remote');
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# hello-fleet\n');
  writeFileSync(join(repo, 'server.mjs'), "console.log('hello');\n");
  git(repo, 'add', '-A');
  commit(repo, 'first');
  git(repo, 'remote', 'add', 'origin', 'git@github.com:acme/hello-fleet.git');
  ab('add', repo);

  // 1. Sharing a GitHub token, checked before it is stored.
  const refused = spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'not-the-token', encoding: 'utf8' });
  console.log(`\n$ echo not-the-token | agentbox auth github --token-stdin\n${hide(refused.stderr).trim()}`);
  check('a token GitHub refuses is not stored', /refused the token/.test(refused.stderr), refused.stderr);
  check('and nothing was written', !existsSync(join(work, 'config/agentbox/credentials/github.token')));

  const shared = spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'demo-token', encoding: 'utf8' });
  console.log(`\n$ echo demo-token | agentbox auth github --token-stdin\n${hide(shared.stdout).trim()}`);
  check('a good token is stored, and says whose it is', shared.stdout.includes('demo-user'), shared.stdout + shared.stderr);
  const status = ab('auth', 'status');
  check('auth status says GitHub is shared with every agent', /GitHub\s+shared with every agent/.test(status), status);

  // 2. The agents, and what the fleet knows about them.
  log('Making two agents');
  const w1 = agent('agent-01', 'Reminders page');
  const w2 = agent('agent-02', 'Why the build is slow');
  writeFileSync(join(w1, 'server.mjs'), "console.log('hello');\nconsole.log('reminders');\n");
  writeFileSync(join(w1, 'reminders.mjs'), 'export const reminders = [];\n');

  const fleet = ab('fleet', P);
  check('the fleet lists both agents with what each is for', fleet.includes('Reminders page') && fleet.includes('Why the build is slow'), fleet);
  check('it shows what agent-01 changed, and that it is uncommitted', /2 files \+\d+\/-\d+\*/.test(fleet), fleet);
  check('and that agent-02 has changed nothing yet', /agent-02\s+Why the build is slow\s+\S+.*\s-\s/.test(fleet), fleet);
  check("it shows agent-01's pull request and its failing checks", /#42 open, checks failing, 3 comment/.test(fleet), fleet);
  check('the project names the GitHub repository it pushes to', fleet.includes('acme/hello-fleet'), fleet);

  // 3. The media of a whole project, labelled and filterable.
  log('Each agent shows some work');
  addMedia('the reminders page', 'agent-01', 'screenshot', '');
  addMedia('what I did', 'agent-01', 'note', 'Added the page and a test.');
  addMedia('build timings', 'agent-02', 'log', '');
  const all = ab('media', 'list', P);
  check('a project lists every agent’s media in one stream', all.includes('the reminders page') && all.includes('build timings'), all);
  check('each item says which agent it came from, and what that agent was for',
    all.includes('agent-01 · Reminders page') && all.includes('agent-02 · Why the build is slow'), all);
  const shots = ab('media', 'list', P, '--kind', 'screenshot');
  check('it can be filtered by kind', shots.includes('the reminders page') && !shots.includes('build timings'), shots);
  const one = ab('media', 'list', `${P}/agent-02`);
  check('and one agent’s own media still lists on its own', one.includes('build timings') && !one.includes('the reminders page'), one);

  if (!process.env.NO_APP) await shots_();

  // 4. Retiring the agents that finished.
  log('agent-01 commits its work; agent-02 is midway through, with nothing committed');
  writeFileSync(join(w2, 'notes.md'), 'still measuring\n');
  execFileSync('git', ['-C', w1, 'add', '-A'], { encoding: 'utf8' });
  commit(w1, 'the reminders page');
  const fleetIdle = ab('fleet', P);
  check('the fleet says which agents finished and are holding a machine', /holding a machine|agentbox retire/.test(fleetIdle), fleetIdle);

  const dry = ab('retire', P, '--how', 'destroy', '--dry-run');
  check('a dry run says what it would free, without doing it', dry.includes('Would destroy agent-01'), dry);
  check('and leaves the agent whose work is uncommitted', /Left agent-02.*uncommitted/.test(dry), dry);
  check('nothing was actually retired', existsSync(join(work, 'data/agentbox/worktrees', P, 'agent-01')), 'the worktree went');

  const retired = ab('retire', P, '--how', 'destroy');
  check('retiring frees the finished agent', retired.includes('Destroyed agent-01'), retired);
  check('and says where its work stays', retired.includes('agentbox/agent-01'), retired);
  check("the agent's machine and worktree are gone", !existsSync(join(work, 'data/agentbox/worktrees', P, 'agent-01')));
  const branches = git(repo, 'branch', '--list', '--format=%(refname:short)').split('\n');
  check('but its branch is still there: the branch is the work', branches.includes('agentbox/agent-01'), branches.join(','));
  check('and the commit it made is on that branch', git(repo, 'show', 'agentbox/agent-01:reminders.mjs').includes('reminders'), 'missing');
  const left = ab('fleet', P);
  check('the agent with uncommitted work was left alone', left.includes('agent-02') && !left.includes('agent-01'), left);

  // 5. Taking the token back.
  ab('auth', 'github', 'remove');
  const after = ab('auth', 'status');
  check('the token can be taken back', /GitHub\s+not shared/.test(after), after);
  const without = ab('fleet', P);
  check('without a token the fleet still works, just without pull requests', without.includes('agent-02') && !without.includes('#42'), without);
  const calls = gh.calls();
  check('the stub GitHub was only ever read, never written to', calls.length > 0 && calls.every((c) => c.startsWith('GET ')), calls.join(' | '));

  gh.proc.kill();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// shots_ drives the app: the Agents tab, and the Media tab filtered by agent.
async function shots_() {
  log('Starting the app on a private display');
  mkdirSync(media, { recursive: true });
  // Share the token again, so the app shows the pull request.
  spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'demo-token', encoding: 'utf8' });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 940 });
  let rec;
  try {
    const app = await electron.launch({
      args: ['.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'],
      cwd: desktop,
      env: { ...env, DISPLAY: x.display },
    });
    const page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await sleep(3000);
    const shot = async (name) => {
      await page.screenshot({ path: join(media, `fleet-${name}.png`) });
      log('screenshot', `fleet-${name}.png`);
    };
    await page.locator('aside, nav').getByText(P, { exact: true }).first().click({ timeout: 15000 });
    await sleep(2000);

    rec = spawn('ffmpeg', ['-y', '-f', 'x11grab', '-video_size', '1500x940', '-framerate', '12', '-i', x.display, '-pix_fmt', 'yuv420p', join(media, 'fleet.mp4')], { stdio: 'ignore' });
    await sleep(1000);

    await page.getByRole('tab', { name: /agents/i }).click({ timeout: 8000 });
    await sleep(3000);
    await shot('01-agents');
    const pr = await page.locator('[data-fleet-pr]').count();
    check("the app's Agents tab shows the pull request", pr > 0, `${pr} found`);
    const changed = await page.locator('[data-fleet-changes]').count();
    check('and what each agent has changed', changed > 0, `${changed} found`);

    const idle = await page.locator('[data-fleet-idle]').count();
    check('the Agents tab offers to free what finished agents are holding', idle > 0, `${idle} found`);

    await page.getByRole('tab', { name: /media/i }).click({ timeout: 8000 });
    await sleep(2500);
    await shot('02-media-all');
    const labelled = await page.locator('[data-media-agent]').count();
    check('media is labelled with the agent it came from', labelled >= 3, `${labelled} labelled`);

    // The filter chip, not the agent in the sidebar, which would navigate away.
    await page.locator('[data-media-filters]').getByRole('button', { name: /agent-01/ }).first().click({ timeout: 8000 });
    await sleep(1800);
    await shot('03-media-filtered');
    const visible = await page.locator('[data-media-agent="agent-01"]').count();
    const others = await page.locator('[data-media-agent="agent-02"]').count();
    check('filtering by an agent shows only its media', visible === 2 && others === 0, `agent-01=${visible} agent-02=${others}`);

    await page.getByRole('link', { name: /setup/i }).or(page.getByText('Setup', { exact: true })).first().click({ timeout: 8000 });
    await sleep(2500);
    await shot('04-setup-github');
    await app.close();
  } catch (err) {
    log('the app run failed:', err.message);
    check('the app shows the fleet and the project media', false, err.message);
  } finally {
    if (rec) {
      rec.kill('SIGINT');
      await sleep(2500);
    }
    x.stop();
  }
  const names = ['01-agents', '02-media-all', '03-media-filtered', '04-setup-github'];
  const got = names.filter((n) => existsSync(join(media, `fleet-${n}.png`)));
  check(`the app was captured (${got.length}/${names.length} screenshots)`, got.length === names.length, got.join(','));
  check('a video was recorded', existsSync(join(media, 'fleet.mp4')));
}

main()
  .then((ok) => {
    log('Done. Work directory:', work);
    rmSync(work, { recursive: true, force: true });
    process.exit(ok ? 0 : 1);
  })
  .catch((err) => {
    console.error(err);
    rmSync(work, { recursive: true, force: true });
    process.exit(1);
  });
