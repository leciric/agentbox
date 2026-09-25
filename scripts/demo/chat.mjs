#!/usr/bin/env node
// Reproduces the chat evidence (D40). A new agent uses the chat by default: the
// app talks to Claude Code through its ACP adapter, which runs inside the agent.
// In the desktop app, on a private X display, you pick a model, ask for a change,
// allow its edit and its test run, and open the folded work and the changed
// files. The conversation survives a daemon restart, which resumes the same
// Claude session; a turn can be stopped; `agentbox chat` works from a terminal;
// an agent can keep Claude Code's command line and switch later; New chat starts
// over. State is throwaway. Needs the base image and a Claude Code login on the
// host (its short-lived access token is copied into the throwaway state, never
// printed). The model is Haiku, to keep the run cheap.
//
//   mise exec -- node scripts/demo/chat.mjs > .demo-runs/chat/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const runDir = join(root, '.demo-runs/chat');
const media = join(runDir, 'media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

// A short path: the daemon's unix socket has to fit in 107 bytes.
const work = join(homedir(), '.cache', 'agentbox-chat-evidence');
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-stack');
const socket = join(work, 'data/agentbox/run/agentbox.sock');
const A1 = 'hello-stack/agent-01';
const A2 = 'hello-stack/agent-02';
const env = {
  ...process.env,
  HOME: join(work, 'home'),
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART']) delete env[name];

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$WORK');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(6)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ab runs the CLI and prints the command and its output, like a terminal.
function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', input: '' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}${r.status ? `\n(exit ${r.status})` : ''}`);
  return { out, status: r.status };
}
const quiet = (...args) => hide(spawnSync(bin, args, { env, encoding: 'utf8', input: '' }).stdout);

function api(method, path) {
  return new Promise((resolve, reject) => {
    const req = http.request({ socketPath: socket, path, method }, (res) => {
      let body = '';
      res.on('data', (chunk) => (body += chunk));
      res.on('end', () => resolve(body ? JSON.parse(body) : null));
    });
    req.on('error', reject);
    req.end();
  });
}
const thread = (ref) => api('GET', `/v1/agents/${ref}/chat`);
const agentInfo = (ref) => api('GET', `/v1/agents/${ref}`);
const turnsEnded = (th) => th.items.filter((it) => it.kind === 'user' && it.result).length;

async function until(what, fn, timeout = 60_000, every = 500) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    // A closed window fails every check, which would look like waiting.
    if (appExited) throw new Error(`the app exited while waiting for ${what}`);
    // A call into a window that went away can hang rather than fail: give each check 15 s.
    const value = await Promise.race([fn().catch(() => undefined), sleep(15_000).then(() => undefined)]);
    if (value) return value;
    await sleep(every);
  }
  throw new Error(`timed out waiting for ${what}`);
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

let page;
const shot = (name) => page.screenshot({ path: join(media, `chat-${name}.png`) });

// answerUntilDone clicks a button of every permission request that appears,
// until the agent's turn count reaches turns. It returns how many it answered.
async function answerUntilDone(ref, turns, button = 'Allow', shots = []) {
  let answered = 0;
  await until(
    'the turn to end',
    async () => {
      const banner = page.locator('[data-chat-permission]');
      if (await banner.count()) {
        await sleep(600);
        if (shots[answered]) await shot(shots[answered]);
        await banner.getByRole('button', { name: button, exact: true }).click();
        answered++;
        await sleep(500);
        return false;
      }
      return turnsEnded(await thread(ref)) >= turns;
    },
    900_000,
    400,
  );
  return answered;
}

let xvfb;
let app;
let appExited = false;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  rmSync(work, { recursive: true, force: true });
  rmSync(media, { recursive: true, force: true });
  mkdirSync(media, { recursive: true });
  mkdirSync(env.HOME, { recursive: true });
  writeFileSync(join(env.HOME, '.gitconfig'), '[user]\n\tname = AgentBox Evidence\n\temail = evidence@agentbox.invalid\n');
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  const token = JSON.parse(readFileSync(join(homedir(), '.claude/.credentials.json'), 'utf8')).claudeAiOauth.accessToken;
  run(bin, ['auth', 'claude', '--token-stdin'], { input: token });
  log('saved an AgentBox Claude Code login in the throwaway state (not printed)');
  ab('add', repo);
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  xvfb = await startXvfb({ width: 1440, height: 900 });
  log(`built agentbox and the desktop app; private X display ${xvfb.display}`);

  // Electron on Xvfb now and then exits as it starts ("GPU process isn't usable"): try twice.
  for (let attempt = 1; ; attempt++) {
    try {
      app = await electron.launch({
        // No software GPU renderer either: on Xvfb, Chromium's GPU process kept
        // dying mid-run, and the app aborts when it can't restart it.
        args: [desktop, '--disable-gpu', '--disable-software-rasterizer'],
        cwd: desktop,
        env: { ...env, DISPLAY: xvfb.display },
        recordVideo: { dir: join(work, 'video'), size: { width: 1440, height: 900 } },
      });
      page = await app.firstWindow();
      // Why the app exits, if it does: Electron's own errors, a crashed renderer, errors in the page.
      app.process().stderr?.on('data', (chunk) => {
        for (const line of String(chunk).split('\n')) {
          if (/FATAL|ERROR|crash|Goodbye|Trace\/breakpoint|Segmentation/i.test(line)) log(`electron: ${line.trim().slice(0, 300)}`);
        }
      });
      app.process().on('exit', (code, signal) => log(`electron exited: code ${code}, signal ${signal}`));
      page.on('crash', () => log('the renderer crashed'));
      page.on('pageerror', (err) => log(`page error: ${err.message.slice(0, 300)}`));
      page.on('console', (m) => m.type() === 'error' && log(`console error: ${m.text().slice(0, 300)}`));
      await page.locator('[data-project="hello-stack"]').waitFor({ timeout: 30_000 });
      app.on('close', () => {
        appExited = true;
        log('the app closed');
      });
      break;
    } catch (err) {
      await app?.close().catch(() => {});
      if (attempt === 2) throw err;
      log(`the app didn't start (${hide(err.message.split('\n')[0])}); starting it again`);
    }
  }

  await step('1. A new agent uses the chat by default', async () => {
    await page.getByRole('button', { name: 'New agent', exact: true }).first().click();
    await page.locator('#agent-title').fill('About page');
    const chat = page.locator('[data-interface="chat"]');
    await chat.waitFor();
    await sleep(600);
    await shot('01-new-agent');
    check('the New agent dialog offers Chat and Terminal, with Chat chosen', (await chat.getAttribute('aria-checked')) === 'true' && (await page.locator('[data-interface="cli"]').count()) === 1);
    await page.getByRole('button', { name: 'Create agent' }).click();
    await page.locator(`[data-chat="${A1}"]`).waitFor({ timeout: 300_000 });
    const info = await agentInfo(A1);
    check('the agent is created with the chat as its interface', info.interface === 'chat', JSON.stringify(info));
    const windows = quiet('exec', A1, '--', "tmux list-windows -F '#W'");
    check("its tmux session has a shell and no Claude Code window", windows.trim() === 'shell', windows);
    check('the app opens the agent on its Chat tab', (await page.getByRole('tab', { name: /Chat/ }).getAttribute('data-state')) === 'active');
    await page.locator('[data-chat-state="ready"]').waitFor({ timeout: 300_000 });
    await sleep(800);
    await shot('02-ready');
    const th = await thread(A1);
    console.log(`\nThe session: ${th.session.adapter}; settings ${th.session.options.map((o) => `${o.id}=${o.value}`).join(', ')}; ${th.session.commands.length} slash commands`);
    check('Claude Code starts behind its ACP adapter, inside the agent', th.session.state === 'ready' && /Claude Agent/.test(th.session.adapter), JSON.stringify(th.session).slice(0, 300));
    const processes = quiet('exec', A1, '--', 'pgrep -af claude-agent-acp | head -1');
    check('the adapter runs in the agent as its user, in the worktree', processes.includes('claude-agent-acp'), processes);
    check('the composer offers its models and permission modes', (await page.locator('[data-chat-option="model"]').count()) === 1 && (await page.locator('[data-chat-option="mode"]').count()) === 1);
  });

  await step('2. Pick a model in the composer', async () => {
    await page.locator('[data-chat-option="model"]').click();
    await page.getByRole('menuitem', { name: /Haiku/ }).waitFor();
    await sleep(500);
    await shot('03-model');
    await page.getByRole('menuitem', { name: /Haiku/ }).click();
    const model = await until('the model to change', async () => {
      const th = await thread(A1);
      const value = th.session.options.find((o) => o.id === 'model')?.value;
      return value === 'haiku' && value;
    });
    check('choosing Haiku changes the session\'s model', model === 'haiku');
  });

  await step('3. Ask for a change, and allow its edit and its test run', async () => {
    const prompt =
      'Add an /about page to server.mjs: a heading "About hello-stack" and a link back to /login. Then run npm test, and tell me in one sentence whether it passed.';
    await page.locator('[aria-label="Message"]').fill(prompt);
    await page.keyboard.press('Enter');
    await page.locator('[data-chat-working]').waitFor({ timeout: 30_000 });
    await sleep(2_500);
    await shot('04-working');
    const answered = await answerUntilDone(A1, 1, 'Allow', ['05-permission']);
    await sleep(1_500);
    await shot('06-done');
    const th = await thread(A1);
    const kinds = th.items.map((it) => it.kind);
    console.log(`\nThe turn's items: ${kinds.join(', ')}`);
    for (const it of th.items.filter((i) => i.kind === 'tool')) console.log(`  tool ${it.tool.status.padEnd(9)} ${it.tool.kind.padEnd(7)} ${it.tool.command || it.tool.title}`);
    for (const it of th.items.filter((i) => i.kind === 'permission')) console.log(`  asked: ${it.permission.title} -> ${it.permission.outcome}`);
    console.log(`  reply: ${th.items.findLast((i) => i.kind === 'assistant')?.text}`);
    check(`Claude asked before acting, and you answered in the app (${answered} requests)`, answered >= 1 && th.items.some((i) => i.kind === 'permission' && i.permission.outcome === 'allow-once'));
    const user = th.items.find((i) => i.kind === 'user');
    check('the turn completed', user?.result?.state === 'completed', JSON.stringify(user?.result));
    const server = readFileSync(join(work, 'data/agentbox/worktrees/hello-stack/agent-01/server.mjs'), 'utf8');
    check('the edit is in the agent\'s worktree', server.includes('About hello-stack'), server.slice(0, 200));
    check('the edit came with its diff', th.items.some((i) => i.tool?.diffs?.some((d) => d.newText.includes('About hello-stack'))));
    check('the test run is in the conversation', th.items.some((i) => i.tool?.kind === 'execute' && /npm test/.test(i.tool.command ?? '') && i.tool.status === 'completed'));
    check('the finished turn folds its work behind "Worked for …"', /Worked for \d/.test((await page.locator('[data-chat-fold]').first().textContent()) ?? ''));
    check('and shows the files it changed', /changed file/.test((await page.locator('[data-chat-changes]').first().textContent()) ?? ''));
  });

  await step('4. Open the work and the changed files', async () => {
    await page.locator('[data-chat-fold]').first().click();
    await sleep(600);
    await shot('07-work');
    const groups = page.locator('[data-chat-work="group"] > button');
    if (await groups.count()) await groups.first().click();
    await sleep(500);
    await page.locator('[data-chat-changes] > button').first().click();
    await page.locator('[data-chat-changes] button[aria-expanded]').nth(1).click();
    await page.locator('[data-diff]').first().waitFor();
    await page.locator('[data-chat-changes]').first().scrollIntoViewIfNeeded();
    await sleep(800);
    await shot('08-diff');
    check("the diff of server.mjs shows the added heading", ((await page.locator('[data-diff]').first().textContent()) ?? '').includes('About hello-stack'));
  });

  await step('5. The conversation survives a daemon restart, and Claude remembers it', async () => {
    const before = await thread(A1);
    ab('daemon', 'stop');
    await sleep(1_000);
    // The app starts the daemon again, and the chat its session.
    const after = await until('the app to start the daemon and the chat again', async () => {
      const th = await thread(A1);
      return th.session.state === 'ready' && th;
    }, 300_000, 1_000);
    await page.locator('[data-chat-state="ready"]').waitFor({ timeout: 30_000 });
    check('after the restart, the chat has the same conversation', JSON.stringify(after.items) === JSON.stringify(before.items), `${after.items.length} items, was ${before.items.length}`);
    await page.locator('[aria-label="Message"]').fill('What heading did you just add? Reply with only the heading text.');
    await page.keyboard.press('Enter');
    await answerUntilDone(A1, 2);
    const th = await thread(A1);
    const reply = th.items.findLast((i) => i.kind === 'assistant')?.text ?? '';
    console.log(`\nClaude's answer after the restart: ${reply}`);
    check('Claude remembers the conversation: the session was resumed', /About hello-stack/.test(reply), reply);
    check('no notice says a new session started', !th.items.some((i) => i.kind === 'notice'));
    await sleep(1_000);
    await shot('09-resumed');
  });

  await step('6. Stop a turn', async () => {
    // Stopped while the model writes, so the step doesn't depend on which
    // commands Claude Code asks about, runs at once, or sends to the background.
    await page.locator('[aria-label="Message"]').fill('Write a detailed 1500-word essay on the history of the HTTP protocol. Do not use any tools.');
    await page.keyboard.press('Enter');
    await until(
      'the answer to stream',
      async () => {
        const th = await thread(A1);
        const turn = th.items.findLast((i) => i.kind === 'user').id;
        return th.items.some((i) => i.turn === turn && i.kind === 'assistant' && i.streaming && (i.text ?? '').length > 300);
      },
      120_000,
    );
    await sleep(800);
    await shot('10-stopping');
    await page.locator('[data-chat-composer] [aria-label="Stop the turn"]').click();
    await until('the turn to stop', async () => turnsEnded(await thread(A1)) >= 3, 60_000);
    const th = await thread(A1);
    const user = th.items.findLast((i) => i.kind === 'user');
    const answer = th.items.findLast((i) => i.turn === user.id && i.kind === 'assistant');
    console.log(`\nStopped after ${answer?.text?.length ?? 0} characters of the answer; the turn ended as ${user.result?.state} (${user.result?.stopReason || 'no stop reason'})`);
    check('Stop ends the turn as cancelled', user.result?.state === 'cancelled', JSON.stringify(user.result));
    check('the answer written so far stays, and no longer streams', !!answer && answer.text.length > 300 && !answer.streaming, JSON.stringify(answer ?? null).slice(0, 200));
    await page.locator('[data-chat-state="ready"]').waitFor({ timeout: 30_000 });
    check('the session is ready for the next message', (await thread(A1)).session.state === 'ready');
    await sleep(1_000);
    await shot('11-stopped');
  });

  await step('7. The same chat from a terminal', async () => {
    const transcript = ab('chat', A1);
    check('agentbox chat prints the conversation', transcript.out.includes('About hello-stack') && transcript.out.includes('› What heading'), transcript.out);
    const reply = ab('chat', A1, 'Reply with exactly: pong');
    check('agentbox chat <message> follows the reply to the end', reply.status === 0 && /pong/i.test(reply.out), reply.out);
    const bad = ab('chat', A1, '--set', 'model=gpt-9');
    check('a setting the tool doesn\'t offer is refused, listing the choices', bad.status !== 0 && bad.out.includes('choices:') && bad.out.includes('haiku'), bad.out);
    await sleep(1_500);
    await shot('12-from-terminal');
  });

  await step('8. An agent can keep Claude Code\'s command line, and switch later', async () => {
    ab('create', 'hello-stack', '--interface', 'cli', '--title', 'Terminal agent');
    const windows = quiet('exec', A2, '--', "tmux list-windows -F '#W'");
    check('an agent created with --interface cli runs Claude Code in tmux window 1', windows.split('\n').includes('claude'), windows);
    await page.locator(`[data-agent="${A2}"]`).click();
    await page.getByRole('tab', { name: /Terminal/ }).waitFor();
    await sleep(2_500);
    check('it has no Chat tab, and opens on the terminal', (await page.getByRole('tab', { name: /Chat/ }).count()) === 0 && (await page.getByRole('tab', { name: /Terminal/ }).getAttribute('data-state')) === 'active');
    await shot('13-cli-agent');
    await page.getByRole('tab', { name: /Overview/ }).click();
    await page.getByRole('radio', { name: 'Chat' }).click();
    await page.getByRole('tab', { name: /Chat/ }).waitFor({ timeout: 10_000 });
    await sleep(800);
    await shot('14-switched');
    check('switching it to Chat in Overview adds the Chat tab', (await agentInfo(A2)).interface === 'chat');
  });

  await step('9. New chat starts over', async () => {
    await page.locator(`[data-agent="${A1}"]`).click();
    await page.getByRole('button', { name: 'New chat' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'New chat' }).click();
    await page.locator('[data-chat-hero]').waitFor({ timeout: 10_000 });
    await page.locator('[data-chat-state="ready"]').waitFor({ timeout: 120_000 });
    await sleep(1_000);
    await shot('15-new-chat');
    const th = await thread(A1);
    check('New chat empties the conversation, and a new session starts', th.items.length === 0 && th.session.state === 'ready');
  });

  await step('10. Codex uses the same chat, once it has a login', async () => {
    const refused = ab('create', 'hello-stack', '--ai', 'codex');
    check('without a Codex login, a Codex agent is refused before anything is made', refused.status !== 0 && refused.out.includes('no Codex login'), refused.out);
  });

  await app.close();
  app = undefined;
} catch (err) {
  failed = true;
  // A failing step has printed its error already.
  if (!results.some((r) => r.startsWith('FAIL'))) console.log(hide(err.stack ?? err));
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  for (const ref of [A2, A1]) quiet('destroy', ref, '--force', '--delete-branch');
  quiet('remove', 'hello-stack');
  quiet('daemon', 'stop');
  xvfb?.stop();

  const videoDir = join(work, 'video');
  // The longest recording, if the app had to be started twice.
  const video = existsSync(videoDir)
    ? readdirSync(videoDir)
        .filter((f) => f.endsWith('.webm'))
        .map((f) => join(videoDir, f))
        .sort((a, b) => statSync(b).size - statSync(a).size)[0]
    : undefined;
  if (video) {
    const mp4 = join(media, 'chat.mp4');
    const gif = join(media, 'chat.gif');
    run('ffmpeg', ['-y', '-loglevel', 'error', '-i', video,'-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '30', '-preset', 'veryfast', '-movflags', '+faststart', mp4]);
    run('ffmpeg', [
      '-y', '-loglevel', 'error', '-i', mp4,
      '-vf', 'setpts=PTS/4,fps=5,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4',
      gif,
    ]);
    log('video: saved an MP4 and a 4x GIF');
  }
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
