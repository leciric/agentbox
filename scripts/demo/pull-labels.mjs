#!/usr/bin/env node
// Opens the Pull requests tab on a stub GitHub, for walking through labels and
// the Mine filter by hand: a repository with pull requests by the token's
// account (demo-user) and by others, labels in GitHub's colours, and a label,
// "release", that the stub refuses to put on, so a failed edit rolls back.
// State is throwaway, and GitHub is a stub, so nothing reaches the real one.
// It builds the CLI and the app, starts the app on $DISPLAY (or :99), and
// keeps everything running until it is stopped.
//
//   mise exec -- node scripts/demo/pull-labels.mjs
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const work = mkdtempSync(join(tmpdir(), 'agentbox-labels-'));
const bin = join(work, 'bin', 'agentbox');
const P = 'hello-stack';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  // A stub incus: this runs inside an AgentBox agent, which has none.
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART']) delete env[name];

const log = (...parts) => console.log('[labels]', ...parts);
const ab = (...args) => {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 120_000 });
  log(`agentbox ${args.join(' ')}:`, `${r.stdout ?? ''}${r.stderr ?? ''}`.trim().replaceAll(work, '$TMP'));
};
const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim();

// serveGitHub is the stub, run in a process of its own: ab() blocks this one's
// event loop, and `auth github` asks GitHub who the token is.
function serveGitHub(port) {
  const http = require('node:http');
  const labels = [
    ['ci:full', '0e8a16', 'Run every CI job, macOS and Windows too'],
    ['nightly', '5319e7', 'Ships in tonight’s build'],
    ['deploy-dev', 'fbca04', 'Deploy to the dev environment'],
    ['bug', 'd73a4a', 'Something isn’t working'],
    ['enhancement', 'a2eeef', 'New feature or request'],
    ['documentation', '0075ca', 'Improvements or additions to documentation'],
    ['dependencies', '0366d6', 'Pull requests that update a dependency'],
    ['good first issue', '7057ff', 'Good for newcomers'],
    ['release', 'b60205', 'Only maintainers may add this'],
  ].map(([name, color, description]) => ({ name, color, description }));
  const label = (name) => labels.find((l) => l.name === name);
  const at = (h) => new Date(Date.now() - h * 3600_000).toISOString();
  const prs = [
    [41, 'Pull requests tab: labels and a Mine filter', 'open', 'demo-user', 'feat/pulls-labels', ['enhancement'], 0.2],
    [40, 'Fix the rail overflowing on long branch names', 'open', 'demo-user', 'fix/rail-overflow', ['bug', 'ci:full'], 1],
    [39, 'Bump electron from 39.1 to 39.2', 'open', 'dependabot[bot]', 'dependabot/npm/electron-39.2', ['dependencies'], 3],
    [38, 'Docs: explain project memory', 'open', 'hubot', 'docs/memory', ['documentation'], 5],
    [37, 'Faster agent start-up', 'merged', 'demo-user', 'perf/start', ['nightly'], 26],
    [36, 'Try a second sidebar layout', 'closed', 'hubot', 'exp/sidebar', [], 50],
  ].map(([number, title, state, author, head, on, hours]) => ({ number, title, state, author, head, on, updated: at(hours) }));

  const item = (p) => ({
    number: p.number,
    title: p.title,
    state: p.state === 'open' ? 'open' : 'closed',
    merged_at: p.state === 'merged' ? p.updated : null,
    html_url: `https://github.com/acme/hello-stack/pull/${p.number}`,
    draft: false,
    updated_at: p.updated,
    user: { login: p.author, avatar_url: '' },
    base: { ref: 'main' },
    head: { ref: p.head, sha: `sha${p.number}` },
    labels: p.on.map(label),
  });
  const send = (res, status, body) => setTimeout(() => res.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(body)), 350);

  http
    .createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        const url = new URL(req.url, 'http://stub');
        const path = url.pathname;
        if (req.headers.authorization !== 'Bearer demo-token') return send(res, 401, { message: 'Bad credentials' });
        if (path === '/user') return send(res, 200, { login: 'demo-user' });
        if (path === '/repos/acme/hello-stack')
          return send(res, 200, { default_branch: 'main', allow_merge_commit: true, allow_squash_merge: true, permissions: { push: true } });
        if (path === '/repos/acme/hello-stack/pulls') return send(res, 200, prs.map(item));
        if (path === '/repos/acme/hello-stack/labels') return send(res, 200, url.searchParams.get('page') === '1' ? labels : []);
        if (path.endsWith('/check-runs')) return send(res, 200, { total_count: 1, check_runs: [{ status: 'completed', conclusion: 'success' }] });
        let m = path.match(/^\/repos\/acme\/hello-stack\/pulls\/(\d+)$/);
        if (m) return send(res, 200, { ...item(prs.find((p) => p.number === Number(m[1]))), additions: 120, deletions: 14, comments: 2 });
        m = path.match(/^\/repos\/acme\/hello-stack\/issues\/(\d+)\/labels$/);
        if (m) {
          const pr = prs.find((p) => p.number === Number(m[1]));
          if (req.method === 'PUT') {
            const names = JSON.parse(body).labels;
            if (names.includes('release') && !pr.on.includes('release'))
              return send(res, 403, { message: 'Must have maintain access to add the release label' });
            pr.on = names.filter((n) => label(n));
            pr.updated = new Date().toISOString();
          }
          return send(res, 200, pr.on.map(label));
        }
        return send(res, 404, { message: 'Not Found' });
      });
    })
    .listen(port, '127.0.0.1');
}

async function main() {
  log('building agentbox and the app');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  mkdirSync(join(work, 'stub'), { recursive: true });
  writeFileSync(join(work, 'stub', 'incus'), `#!/bin/sh\ncase "$1" in list|query) echo '[]' ;; esac\nexit 0\n`, { mode: 0o755 });

  const port = 9377;
  const script = join(work, 'github.cjs');
  writeFileSync(script, `${serveGitHub.toString()}\nserveGitHub(${port});\n`);
  const gh = spawn(process.execPath, [script], { stdio: 'inherit' });
  env.AGENTBOX_GITHUB_API = `http://127.0.0.1:${port}`;
  await new Promise((r) => setTimeout(r, 500));

  const repo = join(work, 'repos', P);
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# demo\n');
  git(repo, 'add', '-A');
  git(repo, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', 'commit', '-q', '-m', 'first');
  git(repo, 'remote', 'add', 'origin', 'git@github.com:acme/hello-stack.git');
  ab('add', repo, '--name', P);
  spawnSync(bin, ['auth', 'github', '--token-stdin'], { env, input: 'demo-token', encoding: 'utf8' });

  log('starting the app');
  const app = spawn(join(desktop, 'node_modules/.bin/electron'), ['.', '--no-sandbox'], {
    cwd: desktop,
    env: { ...env, DISPLAY: process.env.DISPLAY || ':99' },
    stdio: 'ignore',
  });
  const stop = () => {
    app.kill();
    gh.kill();
    spawnSync(bin, ['daemon', 'stop'], { env });
    rmSync(work, { recursive: true, force: true });
    process.exit(0);
  };
  process.on('SIGINT', stop);
  process.on('SIGTERM', stop);
  app.on('exit', stop);
  log('ready: open hello-stack → Pull requests');
}

await main();
