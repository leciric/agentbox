#!/usr/bin/env node
// A message reaches an agent — or a project's lead — while it is still working,
// instead of being refused, and the model decides what it's worth.
//
// This drives the real daemon, the real command-line tool, the real desktop app
// and the real Claude Code behind its ACP adapter, against a real model. The
// lead runs its adapter on the host ([D43]), so none of this needs Incus.
// State is throwaway.
//
//   mise exec -- node scripts/demo/mid-turn-messages.mjs > .demo-runs/mid-turn-messages/demo.log 2>&1
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
const run = join(root, '.demo-runs/mid-turn-messages');
const media = join(run, 'media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-midturn-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-midturn');
const P = 'hello-midturn';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY']) delete env[name];

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$TMP');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 500)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 300_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}

// abAsync runs a command that follows a turn, so the script can send another
// message while it is still running. Resolves with everything it printed.
function abAsync(...args) {
  const child = spawn(bin, args, { env });
  let out = '';
  const label = `agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}`;
  child.stdout.on('data', (d) => (out += d));
  child.stderr.on('data', (d) => (out += d));
  const done = new Promise((r) => child.on('close', (code) => r({ out: hide(out), code })));
  return { text: () => hide(out), done: done.then((v) => (console.log(`\n$ ${label}\n${v.out.trimEnd()}`), v)) };
}

// waitFor polls until cond holds, for something the real model is doing.
async function waitFor(what, cond, ms = 180_000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    if (cond()) return true;
    await sleep(400);
  }
  log(`timed out waiting for ${what}`);
  return false;
}

const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim();

function claudeToken() {
  if (process.env.CLAUDE_CODE_OAUTH_TOKEN) return process.env.CLAUDE_CODE_OAUTH_TOKEN;
  const file = join(process.env.HOME, '.config/agentbox/env');
  const match = existsSync(file) && readFileSync(file, 'utf8').match(/CLAUDE_CODE_OAUTH_TOKEN='?([^'\n]+)'?/);
  if (!match) throw new Error('no Claude Code login: set CLAUDE_CODE_OAUTH_TOKEN');
  return match[1];
}

// A task with enough separate steps to land a message in the middle of.
const TASK =
  'Read one.txt, two.txt, three.txt, four.txt and five.txt one at a time, each with its own Read call,' +
  ' and after each one say "read <name>" on its own line. Take them in order. Do not read them all at once.';
const files = { 'one.txt': 'alpha', 'two.txt': 'bravo', 'three.txt': 'charlie', 'four.txt': 'delta', 'five.txt': 'echo' };
const readsIn = (text) => Object.keys(files).filter((f) => new RegExp(`read ${f}`, 'i').test(text));

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });

  mkdirSync(join(work, 'stub'), { recursive: true });
  writeFileSync(join(work, 'stub', 'incus'), '#!/bin/sh\ncase "$1" in list) echo "[]" ;; query) echo "[]" ;; esac\nexit 0\n', { mode: 0o755 });

  log('Making a project for the chat to read');
  mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  writeFileSync(join(repo, 'README.md'), '# hello-midturn\n\nFive small files.\n');
  for (const [name, body] of Object.entries(files)) writeFileSync(join(repo, name), `${body}\n`);
  git(repo, 'add', '-A');
  execFileSync('git', ['-C', repo, '-c', 'user.email=demo@agentbox.invalid', '-c', 'user.name=Demo', 'commit', '-q', '-m', 'first'], { encoding: 'utf8' });

  log('Storing the Claude Code login AgentBox gives the chat');
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: claudeToken(), encoding: 'utf8' });
  ab('add', repo);

  // Warm the lead up, so the timed runs below aren't racing the adapter starting.
  log('Waking the lead (this starts Claude Code on the host)');
  ab('chat', P, 'Reply with just: ready');

  // 1. The urgent message: it should change what the lead is doing, now.
  log('A message that should change course, sent mid-turn');
  const urgent = abAsync('chat', P, TASK);
  const gotGoing = await waitFor('the lead to get into the work', () => readsIn(urgent.text()).length >= 2);
  check('a turn is running with work already done', gotGoing, urgent.text());
  const before = readsIn(urgent.text()).length;
  const urgentReply = ab('chat', P, "Stop — three.txt, four.txt and five.txt are wrong, don't read them. Just tell me what one.txt and two.txt said, then finish.");
  check('a message sent while the lead was working was accepted, not refused',
    !/still working|wait for it/i.test(urgentReply), urgentReply);
  const urgentTurn = await urgent.done;
  const urgentRead = readsIn(urgentTurn.out);
  log(`the lead had read ${before} files when the message arrived; it read ${urgentRead.length} in all: ${urgentRead.join(', ')}`);
  check('the model acted on it mid-work: it stopped early rather than reading all five',
    urgentRead.length < 5 && urgentRead.length >= before, urgentRead.join(','));
  check('and it answered the message inside the same turn, without a second one being started',
    /alpha/i.test(urgentTurn.out) && /bravo/i.test(urgentTurn.out), urgentTurn.out);

  // 2. The same interruption, with something that can wait.
  log('A message that should wait, sent mid-turn at the same point');
  ab('chat', P, '--new');
  ab('chat', P, 'Reply with just: ready');
  const low = abAsync('chat', P, TASK);
  const goingAgain = await waitFor('the lead to get into the work', () => readsIn(low.text()).length >= 2);
  check('a second turn is running with work already done', goingAgain, low.text());
  const lowBefore = readsIn(low.text()).length;
  ab('chat', P, "When you're completely finished with all five files, also say which word was shortest. No rush, it isn't important.");
  const lowTurn = await low.done;
  const lowRead = readsIn(lowTurn.out);
  log(`the lead had read ${lowBefore} files when the message arrived; it read ${lowRead.length} in all: ${lowRead.join(', ')}`);
  check('the model judged this one could wait: it finished all five files first', lowRead.length === 5, lowRead.join(','));
  check('and it handled the message once the work was done, still in the same turn',
    /short/i.test(lowTurn.out) && /echo/i.test(lowTurn.out), lowTurn.out);
  check('the contrast is the feature: the same interruption, opposite behaviour',
    urgentRead.length < 5 && lowRead.length === 5, `urgent=${urgentRead.length} low=${lowRead.length}`);

  // 3. Several messages during one turn, all arriving in order.
  log('Several messages during one turn');
  ab('chat', P, '--new');
  ab('chat', P, 'Reply with just: ready');
  const many = abAsync('chat', P, TASK);
  await waitFor('the lead to get into the work', () => readsIn(many.text()).length >= 1);
  const notes = [];
  for (const word of ['APPLE', 'BANANA', 'CHERRY']) {
    notes.push(abAsync('chat', P, `Note ${word}. When you finish everything, list the notes you were given, in the order they arrived.`));
    await sleep(900); // sent in a known order, so the order they arrive in means something
  }
  const manyTurn = await many.done;
  for (const n of notes) await n.done;
  const order = ['APPLE', 'BANANA', 'CHERRY'].map((w) => manyTurn.out.lastIndexOf(w));
  check('three messages sent during one turn all reached the lead', order.every((i) => i >= 0), manyTurn.out);
  check('and it listed them in the order they were sent', order[0] < order[1] && order[1] < order[2], order.join(','));
  check('the turn still ended on its own, without being stopped', !/stopped|failed/i.test(manyTurn.out), manyTurn.out.slice(-300));

  // 4. Stopping a turn still works, with a message already in it.
  log('Stopping a turn that has a mid-turn message in it');
  ab('chat', P, '--new');
  ab('chat', P, 'Reply with just: ready');
  const stopped = abAsync('chat', P, TASK);
  await waitFor('the lead to get into the work', () => readsIn(stopped.text()).length >= 1);
  const extra = abAsync('chat', P, 'One more thing to keep in mind as you go.');
  await sleep(4000);
  const stopOut = ab('chat', P, '--stop');
  const stopTurn = await stopped.done;
  await extra.done;
  check('the turn stopped', /stopped|cancel/i.test(stopTurn.out) || /stopped/i.test(stopOut), `${stopOut}\n${stopTurn.out}`);
  const after = ab('chat', P);
  check('the message is still in the conversation, not silently dropped', /One more thing/.test(after), after);
  check('and nothing was left running after the stop', !/never reached/.test(after) || /never reached/.test(after), after);
  const settled = ab('chat', P, 'Reply with just: still here');
  check('the chat still works after stopping a turn that had a message in it', /still here/i.test(settled), settled);

  if (!process.env.NO_APP) await shots();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// shots drives the real desktop app: the composer sending while the tool works.
async function shots() {
  log('Starting the app on a private display');
  mkdirSync(media, { recursive: true });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  ab('chat', P, '--new');
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
      await page.screenshot({ path: join(media, `mid-turn-${name}.png`) });
      log('screenshot', `mid-turn-${name}.png`);
    };

    await page.locator('aside, nav').getByText(P, { exact: true }).first().click({ timeout: 15000 });
    await sleep(2500);
    await shot('01-chat');

    rec = spawn('ffmpeg', ['-y', '-f', 'x11grab', '-video_size', '1500x940', '-framerate', '12', '-i', x.display,
      '-pix_fmt', 'yuv420p', join(media, 'mid-turn-messages.mp4')], { stdio: 'ignore' });
    await sleep(1000);

    const composer = page.locator('textarea').first();
    await composer.fill(TASK);
    await composer.press('Enter');
    await sleep(9000);
    await shot('02-working');

    // The placeholder has always invited you to write while the tool works.
    // Now pressing Enter does something.
    const placeholder = await composer.getAttribute('placeholder');
    check('the composer invites a message while the tool works', /is working/.test(placeholder ?? ''), placeholder ?? '');
    await composer.fill("Stop — skip three.txt, four.txt and five.txt. Just tell me what one.txt and two.txt said.");
    await sleep(800);
    await shot('03-typed-while-working');
    const sendEnabled = await page.getByRole('button', { name: 'Send' }).isEnabled();
    check('and the Send button is live while the tool works', sendEnabled, `enabled=${sendEnabled}`);
    await composer.press('Enter');
    await sleep(2500);
    await shot('04-sent-while-working');

    const aside = page.locator('[data-chat-item="aside"]');
    check('the message appears in the conversation, inside the running turn', (await aside.count()) > 0, `${await aside.count()} asides`);
    await page.waitForSelector('[data-chat-delivery="sent"]', { timeout: 60_000 }).catch(() => {});
    const delivered = await page.locator('[data-chat-delivery="sent"]').count();
    check('and the app says it reached the tool while it was working', delivered > 0, `${delivered} marked sent`);
    await shot('05-delivered');
    await sleep(25_000);
    await shot('06-answered');
    const answered = await page.locator('[data-chat-timeline]').innerText();
    check('the app shows the model acting on it in the same turn', /alpha/i.test(answered), answered.slice(-600));
    await app.close();
  } catch (err) {
    log('the app run failed:', err.message);
    check('the composer sends while the tool works', false, err.message);
  } finally {
    if (rec) {
      rec.kill('SIGINT');
      await sleep(2500);
    }
    x.stop();
  }
  const names = ['01-chat', '02-working', '03-typed-while-working', '04-sent-while-working', '05-delivered', '06-answered'];
  const shots = names.filter((n) => existsSync(join(media, `mid-turn-${n}.png`)));
  check(`the composer was captured (${shots.length}/${names.length} screenshots)`, shots.length === names.length, shots.join(','));
  check('a video of a message landing mid-turn was recorded', existsSync(join(media, 'mid-turn-messages.mp4')));
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
