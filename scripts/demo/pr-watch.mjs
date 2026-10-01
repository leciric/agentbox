#!/usr/bin/env node
// Reproduces the pull request watch: an agent's open pull request is read
// with one GraphQL request per look; when its checks fail, and then when it
// conflicts with its base, the agent is told what broke and how to fix it,
// once per transition, and the app shows it on the agent's pull request. State
// is throwaway, and GitHub is a stub, so nothing reaches the real one.
//
//   mise exec -- node scripts/demo/pr-watch.mjs > .demo-runs/pr-watch/demo.log 2>&1
//
// With APP=1 it also screenshots the real app, on a private display, into
// .demo-runs/pr-watch/media. With KEEP=1 it leaves the daemon and the stub running at the end and prints
// the environment to start the app against them, for a recording; the stub's
// /__set/<state> (pending, failing, conflict, merged) moves the pull request.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/pr-watch/media');
const work = mkdtempSync(join(tmpdir(), 'agentbox-prwatch-'));
const bin = join(work, 'bin', 'agentbox');
const P = 'hello-stack';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  AGENTBOX_HOME: join(work, 'data', 'agentbox'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  // A stub Incus API on a socket of its own, because this runs inside an
  // AgentBox agent, which has no Incus. The watch itself reads git, the store
  // and GitHub; the stub is there so the app has an agent to show.
  INCUS_SOCKET: join(work, 'incus.socket'),
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
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 120_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.join(' ')}\n${out.trimEnd()}`);
  return out;
}
const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim();
const commit = (dir, msg) =>
  execFileSync('git', ['-C', dir, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', 'commit', '-q', '-m', msg], { encoding: 'utf8' });

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

// fakeGitHub runs the stub in a process of its own, since spawnSync blocks
// this one's event loop.
function fakeGitHub() {
  const script = join(work, 'github.mjs');
  const calls = join(work, 'github-calls.log');
  writeFileSync(
    script,
    `${serveIncus.toString()}\nserveIncus(${JSON.stringify(env.INCUS_SOCKET)}, ${JSON.stringify(`ab-${P}-agent-01`)});\n` +
      `${serveGitHub.toString()}\nserveGitHub(${JSON.stringify(calls)});\n`,
  );
  const proc = spawn(process.execPath, [script], { stdio: ['ignore', 'pipe', 'inherit'], detached: !!process.env.KEEP });
  return new Promise((done, fail) => {
    proc.stdout.once('data', (chunk) => done({
      proc,
      url: String(chunk).trim(),
      calls: () => (existsSync(calls) ? readFileSync(calls, 'utf8').trim().split('\n').filter(Boolean) : []),
      set: (state) => fetch(`${String(chunk).trim()}/__set/${state}`).then((r) => r.text()),
      head: (sha) => fetch(`${String(chunk).trim()}/__head/${sha}`).then((r) => r.text()),
    }));
    proc.once('error', fail);
  });
}

// serveGitHub is stringified into that process: it must not close over
// anything here. It serves the REST calls the fleet and the Pull requests
// tab make, and the watch's GraphQL query, from the same pull request.
function serveGitHub(callsFile) {
  const http = process.getBuiltinModule('node:http');
  const { appendFileSync } = process.getBuiltinModule('node:fs');
  const pr = { number: 12, title: 'Add reminders pagination', base: 'main', branch: 'feat/reminders-pagination', sha: '', state: 'pending' };
  const url = `https://github.com/acme/hello-stack/pull/${pr.number}`;
  const merged = () => pr.state === 'merged';
  const conflict = () => pr.state === 'conflict';
  const checks = () => (pr.state === 'pending' ? 'PENDING' : pr.state === 'passing' ? 'SUCCESS' : 'FAILURE');

  function gqlPR() {
    const contexts = [
      { __typename: 'CheckRun', name: 'lint', status: 'COMPLETED', conclusion: 'SUCCESS', detailsUrl: 'https://github.com/acme/hello-stack/actions/runs/7/job/1' },
      pr.state === 'pending'
        ? { __typename: 'CheckRun', name: 'go test', status: 'IN_PROGRESS', conclusion: null, detailsUrl: '' }
        : { __typename: 'CheckRun', name: 'go test', status: 'COMPLETED', conclusion: pr.state === 'passing' ? 'SUCCESS' : 'FAILURE', detailsUrl: 'https://github.com/acme/hello-stack/actions/runs/7/job/2' },
    ];
    return {
      number: pr.number, title: pr.title, url, state: merged() ? 'MERGED' : 'OPEN', isDraft: false, updatedAt: new Date().toISOString(),
      headRefName: pr.branch, headRefOid: pr.sha, baseRefName: pr.base,
      mergeable: conflict() ? 'CONFLICTING' : 'MERGEABLE', reviewDecision: null,
      commits: { nodes: [{ commit: { oid: pr.sha, statusCheckRollup: { state: checks(), contexts: { nodes: contexts } } } }] },
    };
  }

  const srv = http.createServer((req, res) => {
    const set = req.url.match(/^\/__set\/(\w+)$/);
    if (set) {
      pr.state = set[1];
      return void res.end('{}');
    }
    const head = req.url.match(/^\/__head\/(\w+)$/);
    if (head) {
      pr.sha = head[1];
      return void res.end('{}');
    }
    appendFileSync(callsFile, `${req.method} ${req.url}\n`);
    res.setHeader('Content-Type', 'application/json');
    if (req.headers.authorization !== 'Bearer demo-token') return void res.writeHead(401).end(JSON.stringify({ message: 'Bad credentials' }));
    const u = new URL(req.url, 'http://stub');
    if (u.pathname === '/user') return void res.end(JSON.stringify({ login: 'demo-user' }));
    if (u.pathname === '/graphql' && req.method === 'POST') {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        const q = JSON.parse(body).query;
        const repository = { open: { nodes: merged() ? [] : [gqlPR()] } };
        if (q.includes(`pr${pr.number}: pullRequest`)) repository[`pr${pr.number}`] = gqlPR();
        res.end(JSON.stringify({ data: { rateLimit: { remaining: 4987, resetAt: new Date(Date.now() + 3600e3).toISOString() }, repository } }));
      });
      return;
    }
    const list = {
      number: pr.number, title: pr.title, state: merged() ? 'closed' : 'open', html_url: url, draft: false,
      updated_at: new Date().toISOString(), base: { ref: pr.base }, head: { ref: pr.branch, sha: pr.sha },
      ...(merged() ? { merged_at: new Date().toISOString() } : {}),
    };
    if (u.pathname === '/repos/acme/hello-stack') {
      return void res.end(JSON.stringify({ default_branch: 'main', allow_merge_commit: true, allow_squash_merge: true, allow_rebase_merge: true, permissions: { push: true } }));
    }
    if (u.pathname === '/repos/acme/hello-stack/pulls') return void res.end(JSON.stringify([list]));
    if (u.pathname === `/repos/acme/hello-stack/pulls/${pr.number}`) return void res.end(JSON.stringify({ ...list, additions: 142, deletions: 38, comments: 1 }));
    if (u.pathname.match(/^\/repos\/acme\/hello-stack\/commits\/[^/]+\/pulls$/)) return void res.end(JSON.stringify([list]));
    if (u.pathname.match(/^\/repos\/acme\/hello-stack\/commits\/[^/]+\/check-runs$/)) {
      const runs = pr.state === 'pending'
        ? [{ status: 'in_progress' }]
        : [{ status: 'completed', conclusion: 'success' }, { status: 'completed', conclusion: pr.state === 'passing' ? 'success' : 'failure' }];
      return void res.end(JSON.stringify({ total_count: runs.length, check_runs: runs }));
    }
    res.writeHead(404).end(JSON.stringify({ message: `no stub for ${req.method} ${u.pathname}` }));
  });
  srv.listen(0, '0.0.0.0', () => process.stdout.write(`http://127.0.0.1:${srv.address().port}\n`));
}

// serveIncus answers the few Incus API calls the daemon makes to list and
// describe agents: one running container. Stringified like serveGitHub.
function serveIncus(socket, name) {
  const http = process.getBuiltinModule('node:http');
  const { rmSync } = process.getBuiltinModule('node:fs');
  const instance = {
    name, status: 'Running', status_code: 103, type: 'container', project: 'default', location: 'none',
    config: {}, expanded_config: {}, devices: {}, expanded_devices: {}, profiles: ['default'], created_at: '2026-09-27T10:00:00Z',
    state: {
      status: 'Running', status_code: 103, pid: 1, processes: 12,
      cpu: { usage: 1e9 }, memory: { usage: 512 << 20, usage_peak: 600 << 20 },
      network: { eth0: { addresses: [{ family: 'inet', address: '10.8.8.21', netmask: '24', scope: 'global' }] } },
    },
  };
  const sync = (metadata) => JSON.stringify({ type: 'sync', status: 'Success', status_code: 200, metadata });
  rmSync(socket, { force: true });
  http
    .createServer((req, res) => {
      res.setHeader('Content-Type', 'application/json');
      const u = new URL(req.url, 'http://incus');
      if (u.pathname === '/1.0') {
        return void res.end(sync({ api_extensions: ['instances', 'container_full', 'instance_get_full'], api_status: 'stable', api_version: '1.0', auth: 'trusted', public: false,
          auth_methods: ['tls'], environment: { server_name: 'stub', project: 'default', server_version: '6.0', storage: 'btrfs' }, config: {} }));
      }
      if (u.pathname === '/1.0/instances') return void res.end(sync(u.searchParams.get('recursion') ? [instance] : [`/1.0/instances/${name}`]));
      if (u.pathname === `/1.0/instances/${name}`) return void res.end(sync(instance));
      if (u.pathname === `/1.0/instances/${name}/state`) return void res.end(sync(instance.state));
      res.writeHead(404).end(JSON.stringify({ type: 'error', error: 'not found', error_code: 404 }));
    })
    .listen(socket);
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

// events is what the project's agents reported, newest first.
const brokenEvents = async () => ((await apiRequest('GET', `/v1/projects/${P}/agent-events`)).json ?? []).filter((e) => e.kind === 'pr_broken');

async function waitFor(what, cond, ms = 200_000) {
  for (const deadline = Date.now() + ms; Date.now() < deadline; await sleep(1000)) {
    if (await cond()) return true;
  }
  log('timed out waiting for', what);
  return false;
}

// shots drives the real app against this daemon: the agent's pull request in
// the rail, the Pull requests tab, what the agent was told, and both settings.
async function shots() {
  const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');
  mkdirSync(media, { recursive: true });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 940 });
  const app = await electron.launch({
    args: ['.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'],
    cwd: desktop,
    env: { ...env, DISPLAY: x.display, XDG_SESSION_TYPE: 'x11' },
  });
  try {
    const page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    // agent-01's chat can't start without a machine, so the rail files it
    // under Finished, which is folded until opened.
    await page.evaluate(() => localStorage.setItem('agentbox.rail.finished', '1'));
    await page.reload();
    await sleep(3000);
    const shot = async (name) => {
      await page.screenshot({ path: join(media, `pr-watch-${name}.png`) });
      log('screenshot', `pr-watch-${name}.png`);
    };
    const tab = (name) => page.getByRole('tab', { name }).first().click({ timeout: 8000 });

    await page.locator('aside, nav').getByText(P, { exact: true }).first().click({ timeout: 15000 });
    await sleep(2500);
    const badge = page.locator('[data-rail-pr="12"]').first();
    check('the rail shows #12 conflicting', (await page.locator('[data-rail-pr-conflict]').count()) > 0);
    await badge.hover({ timeout: 10000 }).catch(async () => log('no #12 in the rail; the page says:', (await page.locator('body').innerText()).slice(0, 2500).replaceAll('\n', ' | ')));
    await sleep(1200);
    await shot('01-rail');

    await tab(/pull requests/i);
    await sleep(2500);
    check('the Pull requests tab shows the conflict', (await page.getByText('conflicts', { exact: true }).count()) > 0);
    await shot('02-pull-requests-tab');

    await page.locator('[data-agent="hello-stack/agent-01"]').first().click({ timeout: 8000 });
    await sleep(2500);
    await tab(/chat/i).catch(() => {});
    await sleep(1500);
    await page.getByText(/It conflicts with/).first().scrollIntoViewIfNeeded().catch(() => {});
    check('agent-01\'s chat shows what it was told', (await page.getByText(/AgentBox is watching your pull request #12/).count()) > 0);
    await shot('03-agent-chat');

    await page.locator('aside, nav').getByText(P, { exact: true }).first().click({ timeout: 8000 });
    await sleep(1500);
    await tab(/overview/i);
    await sleep(1000);
    await tab(/^settings$/i);
    await sleep(1500);
    await page.locator('[data-project-pr-watch]').scrollIntoViewIfNeeded();
    await shot('04-project-setting');

    await page.locator('aside, nav').getByText('Settings', { exact: true }).first().click({ timeout: 8000 });
    await sleep(2000);
    await page.getByRole('button', { name: /show settings/i }).click({ timeout: 3000 }).catch(() => {});
    await sleep(1000);
    await tab(/agents/i);
    await sleep(1000);
    await page.locator('[data-pr-watch]').first().scrollIntoViewIfNeeded();
    check('Settings has the watch, on', (await page.locator('[data-pr-watch][data-state="checked"]').count()) > 0);
    await shot('05-settings');
  } finally {
    await app.close().catch(() => {});
    x.stop();
  }
}

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  const gh = await fakeGitHub();
  env.AGENTBOX_GITHUB_API = gh.url;
  log('A stub GitHub at', gh.url);

  const repo = join(work, 'repos', P);
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# hello-stack\n');
  git(repo, 'add', '-A');
  commit(repo, 'first');
  git(repo, 'remote', 'add', 'origin', 'git@github.com:acme/hello-stack.git');
  ab('add', repo, '--name', P);
  spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'demo-token', encoding: 'utf8' });

  log('agent-01 commits its work, which went up as pull request #12');
  const worktree = agent(P, repo, 'agent-01', 'Reminders pagination', 'agentbox/reminders-pagination');
  writeFileSync(join(worktree, 'pagination.go'), 'package reminders\n');
  git(worktree, 'add', '-A');
  commit(worktree, 'feat: paginate reminders');
  await gh.head(git(worktree, 'rev-parse', 'HEAD'));
  ab('ls');

  const settings = (await apiRequest('GET', '/v1/settings')).json;
  check('the watch is on by default', settings?.prWatch === true, JSON.stringify(settings?.prWatch));

  await waitFor('the first look', () => gh.calls().some((c) => c.startsWith('POST /graphql')));
  check('checks running: nothing to say', (await brokenEvents()).length === 0);

  log('CI fails on #12');
  await gh.set('failing');
  const failed = await waitFor('the failure to be noticed', async () => (await brokenEvents()).length === 1);
  const events = await brokenEvents();
  check('a failing check is reported once, naming the check', failed && /failing checks \(go test\)/.test(events[0]?.summary ?? ''), JSON.stringify(events));
  const thread = (await apiRequest('GET', `/v1/agents/${P}/agent-01/chat`)).json;
  const told = JSON.stringify(thread ?? {});
  check('agent-01 is told to read the failing log and push the fix', told.includes('--log-failed') && told.includes('feat/reminders-pagination'), told.slice(0, 600));

  log('main moves on, and #12 conflicts with it');
  await gh.set('conflict');
  await waitFor('the conflict to be noticed', async () => (await brokenEvents()).length === 2);
  const after = await brokenEvents();
  check('the conflict is a second report, and only that', after.length === 2 && /conflicts with main/.test(after[0].summary) && !/failing/.test(after[0].summary), JSON.stringify(after));

  // The tab answers from the pull request cache, read behind the answer: the
  // first read of a repository is the empty one that starts it.
  let p12;
  await waitFor('the Pull requests tab', async () => {
    p12 = (await apiRequest('GET', `/v1/projects/${P}/pulls`)).json?.pullRequests?.find((p) => p.number === 12);
    return p12?.watched;
  }, 20_000);
  check('the Pull requests tab shows it conflicting', p12?.conflict === true && p12?.watched === true, JSON.stringify(p12));

  const looks = gh.calls().filter((c) => c.startsWith('POST /graphql')).length;
  log(`GitHub was asked ${looks} time(s) by the watch, one request per look`);

  if (process.env.APP) await shots();

  if (process.env.KEEP) {
    console.log(`\nKept running. Start the app against it with:\n  XDG_CONFIG_HOME=${env.XDG_CONFIG_HOME} AGENTBOX_HOME=${env.AGENTBOX_HOME} AGENTBOX_PREVIEW_ADDR=off npm --prefix desktop start\nMove the pull request with: curl ${gh.url}/__set/<pending|passing|failing|conflict|merged>\nStop with: AGENTBOX_HOME=${env.AGENTBOX_HOME} ${bin} daemon stop; kill ${gh.proc.pid}`);
    gh.proc.unref();
  } else {
    spawnSync(bin, ['daemon', 'stop'], { env });
    gh.proc.kill();
  }
  console.log('\n######## Summary');
  console.log(results.join('\n'));
  process.exit(results.some((r) => r.startsWith('FAIL')) ? 1 : 0);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
