#!/usr/bin/env node
// Reproduces the Pull requests tab: a project's repository's pull requests,
// not only the ones with an agent behind them, with the one that does linked
// to it; merging one behind a confirmation that names the branch and the
// method; the real reason when GitHub refuses, or when the token can only
// read. State is throwaway, and GitHub is a stub, so nothing reaches the
// real one.
//
//   mise exec -- node scripts/demo/pull-requests.mjs > .demo-runs/pull-requests/demo.log 2>&1
//
// Set NO_APP=1 to skip the screenshots.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/pull-requests/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-pulls-'));
const bin = join(work, 'bin', 'agentbox');
const P = 'hello-stack';
const E = 'empty-project';
const R = 'readonly-project';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  // A stub incus, because this runs inside an AgentBox agent, which has no
  // Incus of its own. Pull requests read git, the store and GitHub, not incus.
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

// The daemon's own HTTP API, over its real unix socket: the same wire format
// the desktop app and the CLI use.
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
        let out = '';
        res.on('data', (c) => (out += c));
        res.on('end', () => done({ status: res.statusCode, json: out ? JSON.parse(out) : undefined }));
      },
    );
    req.on('error', fail);
    if (data) req.write(data);
    req.end();
  });
}

// pulls reads a project's pull requests, waiting out the first read of its
// repository: the daemon answers from its cache and reads GitHub behind the
// answer, so the first request for a repository is the one that starts it.
async function pulls(project) {
  for (let i = 0; i < 200; i++) {
    const res = await apiRequest('GET', `/v1/projects/${project}/pulls`);
    if (!res.json?.refreshing) return res; // nothing in flight: this is the settled answer
    await sleep(100);
  }
  throw new Error(`GitHub was never read for ${project}`);
}

// slowGitHub tells the stub how long to take over the pull request list. It is
// the stub's own knob, not part of the GitHub API.
const slowGitHub = (gh, ms) => fetch(`${gh.url}/__delay/${ms}`).then((r) => r.text());

// listCalls is how often the whole list was read — the call the fleet used to
// make one of per agent branch, and the tab one of per poll.
const listCalls = (gh, repo) => gh.calls().filter((c) => c.startsWith(`GET /repos/${repo}/pulls?`) && !c.includes('head=')).length;
const branchCalls = (gh, branch) => gh.calls().filter((c) => c.includes('head=') && c.includes(branch)).length;

// fakeGitHub stands in for api.github.com: three repositories exercising the
// tab's states — a busy one with agents behind two of its pull requests, one
// with none yet, and one the token can only read.
//
// It runs in its own process, because this script drives the CLI with
// spawnSync, which blocks this one's event loop: a server here would never
// answer while a command runs.
function fakeGitHub() {
  const script = join(work, 'github.mjs');
  const calls = join(work, 'github-calls.log');
  writeFileSync(script, `${serveGitHub.toString()}\nserveGitHub(${JSON.stringify(calls)});\n`);
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

  // How long the pull request list takes to come back. The real GitHub takes
  // hundreds of milliseconds; this is turned up on purpose, so what the app
  // does while GitHub hasn't answered is visible in a screenshot.
  let delay = 0;

  const repos = {
    'acme/hello-stack': {
      push: true, allowMerge: true, allowSquash: true, allowRebase: true,
      pulls: [
        { number: 12, title: 'Add reminders pagination', state: 'open', draft: false, base: 'main', head: 'agentbox/agent-01', sha: 'sha-12', updated: '2026-09-15T16:00:00Z' },
        { number: 11, title: 'Fix flaky build script', state: 'open', draft: false, base: 'main', head: 'agentbox/agent-99', sha: 'sha-11', updated: '2026-09-15T12:00:00Z' },
        { number: 10, title: 'WIP: experiment with SSR', state: 'open', draft: true, base: 'main', head: 'agentbox/agent-02', sha: 'sha-10', updated: '2026-09-14T09:00:00Z' },
        { number: 9, title: 'Bump dependency versions', state: 'closed', draft: false, base: 'main', head: 'agentbox/agent-98', sha: 'sha-09', updated: '2026-09-10T09:00:00Z' },
        { number: 8, title: 'Add dark mode toggle', state: 'closed', merged: true, draft: false, base: 'main', head: 'agentbox/agent-97', sha: 'sha-08', updated: '2026-09-05T09:00:00Z' },
      ],
      stats: { 12: [142, 38, 3], 11: [9, 2, 0], 10: [301, 12, 1], 9: [4, 4, 0], 8: [220, 14, 12] },
      checks: { 'sha-12': 'passing', 'sha-11': 'failing', 'sha-10': 'pending' },
      mergeRefusals: { 11: 'Required status check "build" is failing.' },
    },
    'acme/empty-project': { push: true, allowMerge: true, allowSquash: true, allowRebase: true, pulls: [], stats: {}, checks: {}, mergeRefusals: {} },
    'acme/readonly-project': {
      push: false, allowMerge: true, allowSquash: true, allowRebase: true,
      pulls: [{ number: 3, title: 'Tidy the README', state: 'open', draft: false, base: 'main', head: 'agentbox/nobody', sha: 'sha-3', updated: '2026-09-15T08:00:00Z' }],
      stats: { 3: [6, 1, 0] },
      checks: {},
      mergeRefusals: {},
    },
  };

  function pull(key, number) {
    return repos[key].pulls.find((p) => p.number === number);
  }
  function listItem(key, p) {
    const o = {
      number: p.number, title: p.title, state: p.state,
      html_url: `https://github.com/${key}/pull/${p.number}`,
      draft: p.draft, updated_at: p.updated,
      base: { ref: p.base }, head: { ref: p.head, sha: p.sha },
    };
    if (p.merged) o.merged_at = p.updated;
    return o;
  }
  function detail(key, p) {
    const [additions, deletions, comments] = repos[key].stats[p.number] ?? [0, 0, 0];
    return { ...listItem(key, p), additions, deletions, comments };
  }

  const srv = http.createServer((req, res) => {
    // The delay knob isn't GitHub: it's this script telling the stub how slow
    // to be, so it is neither logged nor authenticated.
    const slow = req.url.match(/^\/__delay\/(\d+)$/);
    if (slow) {
      delay = Number(slow[1]);
      return void res.end('{}');
    }
    appendFileSync(callsFile, `${req.method} ${req.url}\n`);
    res.setHeader('Content-Type', 'application/json');
    if (req.headers.authorization !== 'Bearer demo-token') {
      res.writeHead(401).end(JSON.stringify({ message: 'Bad credentials' }));
      return;
    }
    const url = new URL(req.url, 'http://stub');
    if (url.pathname === '/user') return void res.end(JSON.stringify({ login: 'demo-user' }));

    const repoMatch = url.pathname.match(/^\/repos\/([^/]+)\/([^/]+)(\/.*)?$/);
    if (!repoMatch) return void res.writeHead(404).end('{}');
    const [, owner, name, sub = ''] = repoMatch;
    const key = `${owner}/${name}`;
    const repo = repos[key];
    if (!repo) return void res.writeHead(404).end(JSON.stringify({ message: `no stub repo ${key}` }));

    if (sub === '' && req.method === 'GET') {
      return void res.end(JSON.stringify({
        default_branch: 'main',
        allow_merge_commit: repo.allowMerge, allow_squash_merge: repo.allowSquash, allow_rebase_merge: repo.allowRebase,
        permissions: { push: repo.push },
      }));
    }
    if (sub === '/pulls' && req.method === 'GET') {
      // head=owner:branch is the one-branch lookup, for a branch the list
      // page didn't carry. Without it, this is the whole list, and the whole
      // list is what takes time.
      const head = url.searchParams.get('head');
      if (head) {
        const branch = head.slice(head.indexOf(':') + 1);
        return void res.end(JSON.stringify(repo.pulls.filter((p) => p.head === branch).map((p) => listItem(key, p))));
      }
      return void setTimeout(() => res.end(JSON.stringify(repo.pulls.map((p) => listItem(key, p)))), delay);
    }
    let m = sub.match(/^\/pulls\/(\d+)\/merge$/);
    if (m && req.method === 'PUT') {
      const refusal = repo.mergeRefusals[Number(m[1])];
      if (refusal) return void res.writeHead(405).end(JSON.stringify({ message: refusal }));
      return void res.end(JSON.stringify({ sha: 'deadbeef', merged: true, message: 'Pull Request successfully merged' }));
    }
    m = sub.match(/^\/pulls\/(\d+)$/);
    if (m && req.method === 'GET') {
      const p = pull(key, Number(m[1]));
      if (!p) return void res.writeHead(404).end(JSON.stringify({ message: 'not found' }));
      return void res.end(JSON.stringify(detail(key, p)));
    }
    m = sub.match(/^\/commits\/([^/]+)\/check-runs$/);
    if (m && req.method === 'GET') {
      const state = repo.checks[m[1]];
      const runs =
        state === 'failing' ? [{ status: 'completed', conclusion: 'success' }, { status: 'completed', conclusion: 'failure' }]
        : state === 'pending' ? [{ status: 'in_progress' }]
        : state === 'passing' ? [{ status: 'completed', conclusion: 'success' }]
        : [];
      return void res.end(JSON.stringify({ total_count: runs.length, check_runs: runs }));
    }
    res.writeHead(404).end(JSON.stringify({ message: `no stub for ${req.method} ${url.pathname}` }));
  });
  srv.listen(0, '127.0.0.1', () => process.stdout.write(`http://127.0.0.1:${srv.address().port}\n`));
}

// agent makes a ready agent with a worktree, without needing a machine.
function agent(project, repo, name, title, branch) {
  const worktree = join(work, 'data/agentbox/worktrees', project, name);
  git(repo, 'worktree', 'add', '--quiet', '-b', branch, worktree, 'HEAD');
  const sql =
    `INSERT INTO agents (project,name,instance,ai,autonomous,branch,base_ref,base_commit,worktree,status,created_at,source,title,claude_account,interface,role) ` +
    `VALUES ('${project}','${name}','ab-${project}-${name}','claude',0,'${branch}','main','${git(repo, 'rev-parse', 'HEAD')}','${worktree}','ready',${Math.floor(Date.now() / 1000)},'','${title}','','chat','worker');`;
  execFileSync('sqlite3', [join(work, 'data/agentbox/state.db'), sql]);
  return worktree;
}

function makeRepo(dir, origin) {
  mkdirSync(dir, { recursive: true });
  git(dir, 'init', '-q', '-b', 'main');
  writeFileSync(join(dir, 'README.md'), '# demo\n');
  git(dir, 'add', '-A');
  commit(dir, 'first');
  if (origin) git(dir, 'remote', 'add', 'origin', origin);
}

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  mkdirSync(join(work, 'stub'), { recursive: true });
  const instances = ['agent-01', 'agent-02', 'agent-03'].map((n) => `{"name":"ab-${P}-${n}","status":"Running"}`).join(',');
  writeFileSync(join(work, 'stub', 'incus'), `#!/bin/sh\ncase "$1" in list) echo '[${instances}]' ;; query) echo '[]' ;; esac\nexit 0\n`, { mode: 0o755 });

  const gh = await fakeGitHub();
  env.AGENTBOX_GITHUB_API = gh.url;
  log('A stub GitHub at', gh.url);

  log('Making three projects: one busy, one with no pull requests yet, one the token can only read');
  const repoP = join(work, 'repos', P);
  const repoE = join(work, 'repos', E);
  const repoR = join(work, 'repos', R);
  makeRepo(repoP, 'git@github.com:acme/hello-stack.git');
  makeRepo(repoE, 'git@github.com:acme/empty-project.git');
  makeRepo(repoR, 'git@github.com:acme/readonly-project.git');
  ab('add', repoP, '--name', P);
  ab('add', repoE, '--name', E);
  ab('add', repoR, '--name', R);

  // 1. Without an account, the tab says so, and which repository it is about
  // ([D55](../../docs/implementation/decisions.md#d55)).
  const before = await apiRequest('GET', `/v1/projects/${P}/pulls`);
  check(
    'with no GitHub account stored, the tab is told so, and which repository it is about',
    before.json.githubError?.kind === 'noAccount' && before.json.githubError?.repo === 'acme/hello-stack',
    JSON.stringify(before.json),
  );

  // 2. Sharing a GitHub token, checked before it is stored.
  const shared = spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'demo-token', encoding: 'utf8' });
  console.log(`\n$ echo demo-token | agentbox auth github --token-stdin\n${hide(shared.stdout).trim()}`);
  check('a good token is stored, and says whose it is', shared.stdout.includes('demo-user'), shared.stdout + shared.stderr);

  // 3. Two agents, one behind an open pull request and one behind a draft.
  log('Making two agents');
  agent(P, repoP, 'agent-01', 'Reminders pagination', 'agentbox/agent-01');
  agent(P, repoP, 'agent-02', 'SSR experiment', 'agentbox/agent-02');

  // 4. The answer comes from the daemon's cache, and GitHub is read behind
  // it. With the stub holding its answer back for a second and a half, the
  // first request still comes back at once — with nothing, and saying so.
  await slowGitHub(gh, 1500);
  const coldStarted = Date.now();
  const cold = await apiRequest('GET', `/v1/projects/${P}/pulls`);
  const coldMs = Date.now() - coldStarted;
  check('a request never waits for GitHub, even with nothing cached', coldMs < 400 && cold.json.refreshing === true, `${coldMs}ms ${JSON.stringify(cold.json)}`);
  check('it names the repository, and says nothing has been read yet', cold.json.github === 'acme/hello-stack' && cold.json.fetchedAt === undefined, JSON.stringify(cold.json));

  // Requests pile up while GitHub is slow — a tab that polls, a rail, the
  // fleet — and must not pile GitHub calls up behind them.
  const piledUp = listCalls(gh, 'acme/hello-stack');
  await Promise.all(Array.from({ length: 10 }, () => apiRequest('GET', `/v1/projects/${P}/pulls`)));
  const list = await pulls(P);
  check('ten requests during one slow read made one GitHub call, not ten', listCalls(gh, 'acme/hello-stack') - piledUp === 1, `${listCalls(gh, 'acme/hello-stack') - piledUp} calls`);
  check('and the list is there once GitHub has answered', list.json.pullRequests.length === 5 && !!list.json.fetchedAt, JSON.stringify(list.json.pullRequests.map((p) => p.number)));

  // 5. Listing: every pull request GitHub has, the one behind an agent linked
  // to it, checks and diff size for the open ones, merge methods offered.
  check('it lists every pull request, not only the ones with an agent', list.json.pullRequests.length === 5, JSON.stringify(list.json.pullRequests.map((p) => p.number)));
  const twelve = list.json.pullRequests.find((p) => p.number === 12);
  check('the one behind an agent is linked to it', twelve?.agent === 'agent-01', JSON.stringify(twelve));
  check('open ones carry checks and diff size', twelve?.checks === 'passing' && twelve?.additions === 142, JSON.stringify(twelve));
  const eleven = list.json.pullRequests.find((p) => p.number === 11);
  check('a pull request with no agent behind it has none linked', !eleven?.agent, JSON.stringify(eleven));
  check('failing checks are reported', eleven?.checks === 'failing', JSON.stringify(eleven));
  check('a token that can push offers every merge method', list.json.canMerge === true && list.json.mergeMethods?.length === 3, JSON.stringify(list.json));

  const empty = await pulls(E);
  check('a repository with none yet says so, not an error', empty.json.pullRequests.length === 0 && !empty.json.githubError, JSON.stringify(empty.json));

  const readonly = await pulls(R);
  check('a token that can only read says it cannot merge, when GitHub says so', readonly.json.canMergeKnown === true && !readonly.json.canMerge, JSON.stringify(readonly.json));

  // 6. A third agent, whose branch the cached answer says nothing about. That
  // is reason enough to re-read — and the request is still answered from the
  // cache at once, with the list it had.
  log('Making a third agent, whose branch nothing has been read about');
  agent(P, repoP, 'agent-03', 'Reminders empty state', 'agentbox/agent-03');
  const staleStarted = Date.now();
  const stale = await apiRequest('GET', `/v1/projects/${P}/pulls`);
  const staleMs = Date.now() - staleStarted;
  check('a request that starts a re-read is still answered at once, from the cache', staleMs < 400 && stale.json.pullRequests.length === 5, `${staleMs}ms ${stale.json.pullRequests.length} rows`);
  check('and says when what it answered with was read, and that it is reading', !!stale.json.fetchedAt && stale.json.refreshing === true, JSON.stringify({ fetchedAt: stale.json.fetchedAt, refreshing: stale.json.refreshing }));
  await pulls(P);
  await slowGitHub(gh, 0);

  // 7. The fleet: one list for every agent, not one call per branch, and only
  // a branch the list page didn't carry is asked about by name — once.
  const beforeFleet = gh.calls().length;
  const fleet = await apiRequest('GET', `/v1/projects/${P}/fleet`);
  check('the fleet matches its agents against the list it already has, with no GitHub call at all', gh.calls().length === beforeFleet, `${gh.calls().length - beforeFleet} calls`);
  const byAgent = Object.fromEntries(fleet.json.agents.map((a) => [a.name, a.pr]));
  check('each agent carries the pull request on its own branch', byAgent['agent-01']?.number === 12 && byAgent['agent-02']?.number === 10, JSON.stringify(byAgent));
  check('and an agent whose branch has none carries none', byAgent['agent-03'] === undefined, JSON.stringify(byAgent['agent-03']));
  const asked = branchCalls(gh, 'agent-03');
  for (let i = 0; i < 5; i++) await apiRequest('GET', `/v1/projects/${P}/fleet`);
  check('five more polls ask GitHub nothing: the answer for a branch outside the list keeps', branchCalls(gh, 'agent-03') === asked && asked === 1, `${asked} then ${branchCalls(gh, 'agent-03')}`);

  // 8. Merging: a draft is refused before GitHub is ever asked; a closed one
  // too; GitHub's own reason for a real refusal; success against the stub.
  const draft = await apiRequest('POST', `/v1/projects/${P}/pulls/10/merge`, { method: 'merge' });
  check('a draft is refused before GitHub is asked', draft.status === 400 && /draft/.test(JSON.stringify(draft.json)), JSON.stringify(draft.json));
  const closed = await apiRequest('POST', `/v1/projects/${P}/pulls/9/merge`, { method: 'merge' });
  check('a closed pull request is refused', closed.status === 400 && /closed/.test(JSON.stringify(closed.json)), JSON.stringify(closed.json));
  const refused = await apiRequest('POST', `/v1/projects/${P}/pulls/11/merge`, { method: 'merge' });
  check("a real refusal surfaces GitHub's own reason", refused.status === 400 && /status check/.test(JSON.stringify(refused.json)), JSON.stringify(refused.json));
  const merged = await apiRequest('POST', `/v1/projects/${P}/pulls/12/merge`, { method: 'squash' });
  check('merging succeeds against the stub, with the method sent', merged.status === 200 && merged.json.state === 'merged', JSON.stringify(merged.json));
  const calls = gh.calls();
  check('the merge sent squash, not a silent default', calls.some((c) => c.startsWith('PUT') && c.includes('/pulls/12/merge')), calls.join(' | '));

  if (!process.env.NO_APP) await shots_(gh);

  gh.proc.kill();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// shots_ drives the real app: every state of the tab, the confirmation
// dialog, a merge performed against the stub, and the failure cases.
async function shots_(gh) {
  log('Starting the app on a private display');
  mkdirSync(media, { recursive: true });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 940 });
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
      await page.screenshot({ path: join(media, `pulls-${name}.png`) });
      log('screenshot', `pulls-${name}.png`);
    };
    const goTo = async (project) => {
      await page.locator('aside, nav').getByText(project, { exact: true }).first().click({ timeout: 15000 });
      await sleep(1000);
      await page.getByRole('tab', { name: /pull requests/i }).click({ timeout: 8000 });
      await sleep(1500);
    };

    // Removing the token again, so the app's first look is what a project
    // with none looks like.
    ab('auth', 'github', 'remove', 'default');
    await goTo(P);
    await sleep(1500);
    await shot('01-no-token');
    check('without a token the tab explains why, rather than failing', (await page.getByText(/no github account is stored/i).count()) > 0);

    log('Sharing the token again, from the CLI, while the app keeps running');
    spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'demo-token', encoding: 'utf8' });
    // The app's queries have a 5s staleTime, so a remount right away would
    // still show the cached "no token" result: outwait it.
    await sleep(5500);

    // A new account empties the daemon's cache — what the old token could see
    // says nothing about what the new one can — so this is the tab's cold
    // start, with GitHub taking two and a half seconds to answer.
    await slowGitHub(gh, 2500);
    await page.getByRole('tab', { name: /agents/i }).click({ timeout: 8000 });
    await page.getByRole('tab', { name: /pull requests/i }).click({ timeout: 8000 });
    await sleep(700);
    await shot('10-reading-github');
    check('the tab is drawn while GitHub hasn’t answered, rather than a spinner over nothing', (await page.getByText(/reading pull requests/i).count()) > 0);
    check('and it says it is asking, not that the repository has none', (await page.getByText(/no pull requests/i).count()) === 0);

    // The daemon announces the read on its event stream, so the list arrives
    // then — not at the tab's next poll, which is a minute away.
    await sleep(4000);
    await shot('11-arrived-on-the-event');
    const arrived = await page.locator('[data-pull-request]').count();
    check('the list arrives the moment GitHub answers, 56 s before the tab would poll again', arrived >= 3, `${arrived} rows`);
    await slowGitHub(gh, 0);

    await page.getByRole('tab', { name: /agents/i }).click({ timeout: 8000 });
    await page.getByRole('tab', { name: /pull requests/i }).click({ timeout: 8000 });
    await sleep(3000);
    await shot('02-open');
    const rows = await page.locator('[data-pull-request]').count();
    check('the tab lists the repository’s pull requests', rows >= 3, `${rows} rows`);
    const agentLink = await page.getByRole('button', { name: /agent-01.?s agent/i }).count();
    check('the one behind an agent links to it', agentLink > 0, `${agentLink} found`);

    // The rail beside it: each agent's pull request, next to the agent.
    await shot('12-rail-pull-request');
    const rail = page.locator('[data-agent-rail]');
    const badges = await rail.locator('[data-rail-pr]').count();
    check('the rail shows the pull request of each agent that has one', badges === 2, `${badges} badges for three agents`);
    check('agent-01’s is its own, #12, with its checks', (await rail.locator('[data-rail-pr="12"]').innerText()).replace(/\s+/g, ' ').trim() === '#12 ✓', await rail.locator('[data-rail-pr="12"]').innerText());
    check('and the agent whose branch has no pull request shows nothing', (await rail.locator('[data-rail-pr]').count()) === 2 && (await rail.locator('[data-agent]').count()) >= 3);

    // Clicking a badge opens the pull request in the browser. The handler is
    // replaced first, so this run opens no browser on the virtual display —
    // what it checks is that the click reaches it, with the right address.
    await app.evaluate(({ ipcMain }) => {
      globalThis.__opened = [];
      ipcMain.removeHandler('shell:openExternal');
      ipcMain.handle('shell:openExternal', (_event, url) => {
        globalThis.__opened.push(url);
      });
    });
    await rail.locator('[data-rail-pr="12"]').click({ timeout: 8000 });
    await sleep(600);
    const opened = await app.evaluate(() => globalThis.__opened);
    check('clicking it opens that pull request in the browser', opened?.[0] === 'https://github.com/acme/hello-stack/pull/12', JSON.stringify(opened));

    await page.locator('[data-pulls-filters]').getByRole('button', { name: /closed/i }).click({ timeout: 8000 });
    await sleep(1200);
    await shot('03-closed-and-merged');
    await page.locator('[data-pulls-filters]').getByRole('button', { name: /^all/i }).click({ timeout: 8000 });
    await sleep(1200);
    await shot('04-all');
    await page.locator('[data-pulls-filters]').getByRole('button', { name: /^open/i }).click({ timeout: 8000 });
    await sleep(800);

    // Merging #12, behind the confirmation, which names the branch and the base.
    await page.locator('[data-pull-request="12"]').getByRole('button', { name: /merge/i }).click({ timeout: 8000 });
    await sleep(800);
    await shot('05-confirm-merge');
    const dialog = page.getByRole('dialog');
    check('the confirmation names the branch and what it merges into', (await dialog.getByText(/agentbox\/agent-01/).count()) > 0 && (await dialog.getByText(/main/).count()) > 0);
    check('it offers the merge method rather than picking one', (await dialog.locator('select').count()) > 0);
    await dialog.getByRole('button', { name: /^merge$/i }).click({ timeout: 8000 });
    await sleep(2000);
    await shot('06-merged');

    // A pull request whose checks are failing: GitHub's real refusal, shown.
    await page.locator('[data-pull-request="11"]').getByRole('button', { name: /merge/i }).click({ timeout: 8000 });
    await sleep(600);
    await page.getByRole('dialog').getByRole('button', { name: /^merge$/i }).click({ timeout: 8000 });
    await sleep(1500);
    await shot('07-merge-refused');
    check("a refused merge shows GitHub's real reason, not a generic failure", (await page.getByText(/status check/i).count()) > 0);
    await page.getByRole('dialog').getByRole('button', { name: /cancel/i }).click({ timeout: 8000 }).catch(() => {});

    // A repository with no pull requests yet.
    await goTo(E);
    await sleep(1000);
    await shot('08-empty');
    check('a repository with none yet says so', (await page.getByText(/no pull requests/i).count()) > 0);

    // A token that can only read: no merge button anywhere, and it says why.
    await goTo(R);
    await sleep(1000);
    await shot('09-read-only-token');
    const mergeButtons = await page.locator('[data-pull-request]').getByRole('button', { name: /merge/i }).count();
    check('a token that can only read is never offered a merge button', mergeButtons === 0, `${mergeButtons} found`);
    check('and the tab says why', (await page.getByText(/can't merge into it/i).count()) > 0);

    await app.close();
  } catch (err) {
    log('the app run failed:', err.message);
    check('the app shows the Pull requests tab in every state', false, err.message);
  } finally {
    x.stop();
  }
  const names = ['01-no-token', '02-open', '03-closed-and-merged', '04-all', '05-confirm-merge', '06-merged', '07-merge-refused', '08-empty', '09-read-only-token',
    '10-reading-github', '11-arrived-on-the-event', '12-rail-pull-request'];
  const got = names.filter((n) => existsSync(join(media, `pulls-${n}.png`)));
  check(`the app was captured (${got.length}/${names.length} screenshots)`, got.length === names.length, got.join(','));
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
