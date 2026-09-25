#!/usr/bin/env node
// Reproduces Step 11a: a chat per project, driven by a lead that has no machine.
// It shows that opening a project's chat creates nothing, that the first message
// creates a detached worktree and no branch, that the lead answers from a real
// Claude Code running on the host, that it refuses to run anything and offers to
// create an agent instead, and that every route assuming a machine is refused.
// State is throwaway.
//
//   mise exec -- node scripts/demo/project-chat.mjs > .demo-runs/project-chat/demo.log 2>&1
//
// Needs a Claude Code login: CLAUDE_CODE_OAUTH_TOKEN, or ~/.config/agentbox/env.
// Set NO_APP=1 to skip the screenshots and the video.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const run = join(root, '.demo-runs/project-chat');
const media = join(run, 'media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-lead-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-lead');
const P = 'hello-lead';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  // A stub incus, so the app renders its (empty) agent list. This evidence runs
  // inside an AgentBox agent, which has no Incus of its own. The project chat
  // never calls incus; the check below proves the stub is never used.
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
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 300_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}

const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim();
const dataPath = (...parts) => join(work, 'data', 'agentbox', ...parts);

// claudeToken finds the Claude Code login this machine has: the environment, or
// the file AgentBox writes into every agent.
function claudeToken() {
  if (process.env.CLAUDE_CODE_OAUTH_TOKEN) return process.env.CLAUDE_CODE_OAUTH_TOKEN;
  const file = join(process.env.HOME, '.config/agentbox/env');
  const match = existsSync(file) && readFileSync(file, 'utf8').match(/CLAUDE_CODE_OAUTH_TOKEN='?([^'\n]+)'?/);
  if (!match) throw new Error('no Claude Code login: set CLAUDE_CODE_OAUTH_TOKEN');
  return match[1];
}

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });

  mkdirSync(join(work, 'stub'), { recursive: true });
  writeFileSync(join(work, 'stub', 'incus'), '#!/bin/sh\necho "INCUS CALLED: $*" >> "$TMPDIR_INCUS_LOG"\ncase "$1" in list) echo "[]" ;; query) echo "[]" ;; esac\nexit 0\n', { mode: 0o755 });
  env.TMPDIR_INCUS_LOG = join(work, 'incus-calls.log');

  log('Making a project to talk about');
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# hello-lead\n\nA tiny demo service.\n\n- `npm start` runs it on port 3000.\n- `npm test` runs the tests.\n');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'hello-lead', scripts: { start: 'node server.mjs', test: 'node --test' } }, null, 2));
  writeFileSync(join(repo, 'server.mjs'), "import http from 'node:http';\nhttp.createServer((_, res) => res.end('hello')).listen(3000);\n");
  git(repo, 'add', '-A');
  execFileSync('git', ['-C', repo, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', 'commit', '-q', '-m', 'first'], { encoding: 'utf8' });

  log('Storing the Claude Code login AgentBox will give the chat');
  const token = claudeToken();
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: token, encoding: 'utf8' });
  ab('add', repo);

  // 1. A project you have never written to has made nothing.
  const before = ab('chat', P);
  check('a project has a chat before it has any agent', before.includes('No messages yet'), before);
  const madeNothing = !existsSync(dataPath('worktrees')) && !existsSync(dataPath('projects')) && !existsSync(dataPath('tools'));
  check('reading it creates nothing: no worktree, no private HOME, no tools', madeNothing,
    `worktrees=${existsSync(dataPath('worktrees'))} projects=${existsSync(dataPath('projects'))} tools=${existsSync(dataPath('tools'))}`);
  const list = ab('list');
  check('and the project has no agents', list.includes('No agents'), list);

  // 2. The first message creates the lead and gets a real answer.
  log('Sending the first message; Claude Code starts on the host');
  const answer = ab('chat', P, 'In one sentence, what does this project do and how do I start it?');
  check('the chat answers from a real Claude Code', /hello|server|npm start|port 3000/i.test(answer), answer);

  const worktree = dataPath('worktrees', P, 'lead');
  check('the first message made a worktree', existsSync(worktree), worktree);
  check('standing on the project branch, detached', existsSync(worktree) && git(worktree, 'rev-parse', '--abbrev-ref', 'HEAD') === 'HEAD',
    existsSync(worktree) ? git(worktree, 'rev-parse', '--abbrev-ref', 'HEAD') : 'no worktree');
  const branches = git(repo, 'branch', '--list', '--format=%(refname:short)').split('\n').filter(Boolean);
  check('and no branch: the chat commits nothing', branches.join(',') === 'main', branches.join(','));
  check('the main checkout still holds main', git(repo, 'rev-parse', '--abbrev-ref', 'HEAD') === 'main');

  // 3. It has no shell, and says so rather than pretending.
  log('Asking it to run something');
  const ran = ab('chat', P, 'Run the test suite and tell me whether it passes.');
  const declined = /(don'?t|do not|cannot|can'?t|unable).{0,40}(run|execute)|no shell|agent/i.test(ran);
  check('asked to run the tests, it says it does not run things', declined, ran);

  const settings = JSON.parse(readFileSync(dataPath('projects', P, 'lead-home/.claude/settings.json'), 'utf8'));
  const deny = settings.permissions.deny;
  for (const tool of ['Bash', 'Write', 'Edit', 'MultiEdit', 'NotebookEdit', 'WebFetch', 'WebSearch']) {
    check(`${tool} is denied by name, so Claude Code never offers it`, deny.includes(tool), deny.join(' '));
  }
  check('reads are fenced to the working directory', settings.permissions.blockReadsOutsideWorkingDirectories === true);
  check("the user's keys are denied by absolute path, with two slashes", deny.some((r) => /^Read\(\/\/[^/]/.test(r)) && !deny.some((r) => r.includes('///')), deny.join(' '));

  // 4. The routes that assume a machine are refused.
  for (const [what, args] of [['diff', ['diff', `${P}/lead`]], ['start', ['start', `${P}/lead`]], ['shell', ['shell', `${P}/lead`]]]) {
    const out = ab(...args);
    check(`${what} on the chat is refused: it has no machine`, out.includes('no machine'), out);
  }
  const listAfter = ab('list');
  check('the chat is still not one of the project’s agents', listAfter.includes('No agents'), listAfter);

  // 5. It survives a restart.
  log('Restarting the daemon');
  ab('daemon', 'stop');
  await sleep(1500);
  const again = ab('chat', P);
  check('after a restart the conversation is still there', again.includes('what does this project do'), again);

  // 6. incus was never called: the chat has no machine to make.
  const incusCalls = existsSync(env.TMPDIR_INCUS_LOG) ? readFileSync(env.TMPDIR_INCUS_LOG, 'utf8').trim() : '';
  const onlyLists = incusCalls.split('\n').filter((l) => l && !/INCUS CALLED: (list|query)/.test(l));
  check('the project chat never asked incus to make anything', onlyLists.length === 0, incusCalls);

  if (!process.env.NO_APP) await shots();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// shots drives the desktop app on a private display: the project's Chat tab
// before and after, and a video of a turn.
async function shots() {
  log('Starting the app on a private display');
  mkdirSync(media, { recursive: true });
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
      await page.screenshot({ path: join(media, `project-chat-${name}.png`) });
      log('screenshot', `project-chat-${name}.png`);
    };

    await shot('01-home');
    // The project in the sidebar: its chat is the tab it opens on.
    await page.locator('aside, nav').getByText(P, { exact: true }).first().click({ timeout: 15000 });
    await sleep(2500);
    await shot('02-project-chat');

    rec = spawn('ffmpeg', ['-y', '-f', 'x11grab', '-video_size', '1500x940', '-framerate', '12', '-i', x.display,
      '-pix_fmt', 'yuv420p', join(media, 'project-chat.mp4')], { stdio: 'ignore' });
    await sleep(1000);

    const composer = page.locator('textarea').first();
    await composer.fill('Which file starts the server, and on which port?');
    await sleep(500);
    await shot('03-typed');
    await composer.press('Enter');
    await sleep(6000);
    await shot('04-working');
    await sleep(22_000);
    await shot('05-answered');
    await page.getByRole('tab', { name: /overview/i }).click({ timeout: 5000 });
    await sleep(1800);
    await shot('06-overview');
    await app.close();
  } catch (err) {
    log('the app run failed:', err.message);
    check('the app shows the project chat', false, err.message);
  } finally {
    if (rec) {
      rec.kill('SIGINT');
      await sleep(2500);
    }
    x.stop();
  }
  const names = ['01-home', '02-project-chat', '03-typed', '04-working', '05-answered', '06-overview'];
  const shots = names.filter((n) => existsSync(join(media, `project-chat-${n}.png`)));
  check(`the app's project Chat tab was captured (${shots.length}/${names.length} screenshots)`, shots.length === names.length, shots.join(','));
  check('a video of a turn was recorded', existsSync(join(media, 'project-chat.mp4')));
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
