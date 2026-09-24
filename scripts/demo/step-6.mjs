#!/usr/bin/env node
// Reproduces the Step 6 evidence: drives the desktop app with Playwright on a
// private X display, checks each part of the roadmap demo, and saves
// screenshots and a video. State is throwaway. Needs the base image and a
// Claude Code login on the host: the host session's short-lived access token
// is copied into the throwaway state; its refresh token never leaves the host.
//
//   mise exec -- node scripts/demo/step-6.mjs > .demo-runs/step-6/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir, tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/step-6/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-step6-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-stack');
const A1 = 'hello-stack/agent-01';
const A2 = 'hello-stack/agent-02';
const FORK = 'hello-stack/forked';
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

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$TMP');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const ab = (...args) => {
  const r = spawnSync(bin, args, { env, encoding: 'utf8' });
  return hide(`${r.stdout}${r.stderr}`);
};
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });

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

// UI helpers

const shot = (page, name) => page.screenshot({ path: join(media, `step-6-desktop-${name}.png`) });
const term = (ref) => `[data-terminal="${ref}"]`;
const terminalText = (page, ref) => page.locator(`${term(ref)} .xterm-rows`).innerText().catch(() => '');

async function waitForTerminal(page, ref, pattern, timeout = 30_000) {
  const deadline = Date.now() + timeout;
  let text = '';
  while (Date.now() < deadline) {
    text = await terminalText(page, ref);
    if (pattern.test(text)) return text;
    await page.waitForTimeout(250);
  }
  throw new Error(`${ref}'s terminal never showed ${pattern}. It shows:\n${text}`);
}

async function focusTerminal(page, ref) {
  await page.locator(`${term(ref)} .xterm-helper-textarea`).focus();
}

async function typeLine(page, ref, line) {
  await focusTerminal(page, ref);
  await page.keyboard.type(line, { delay: 20 });
  await page.keyboard.press('Enter');
}

async function tmuxWindow(page, ref, index) {
  await focusTerminal(page, ref);
  await page.keyboard.press('Control+b');
  await page.keyboard.press(String(index));
  await page.waitForTimeout(400);
}

async function launch(display, videoDir) {
  const app = await electron.launch({
    args: [desktop, '--disable-gpu'],
    cwd: desktop,
    env: { ...env, DISPLAY: display },
    recordVideo: { dir: videoDir, size: { width: 1440, height: 900 } },
  });
  const page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
  return { app, page };
}

let xvfb;
let app;
let page;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  mkdirSync(media, { recursive: true });
  run('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  const token = JSON.parse(readFileSync(join(homedir(), '.claude/.credentials.json'), 'utf8')).claudeAiOauth.accessToken;
  run(bin, ['auth', 'claude', '--token-stdin'], { input: token });
  log('saved an AgentBox Claude Code login in the throwaway state (not printed)');
  run('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  log('built the desktop app');
  xvfb = await startXvfb({ width: 1440, height: 900 });
  log(`started a private X display, ${xvfb.display}`);

  await step('1. Open AgentBox: it starts the daemon and connects', async () => {
    ({ app, page } = await launch(xvfb.display, join(work, 'video-1')));
    await page.getByText('Welcome to AgentBox').waitFor({ timeout: 30_000 });
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
    check('the app started the daemon and follows its event stream', existsSync(join(work, 'data/agentbox/run/agentbox.sock')));
    await page.waitForTimeout(1000);
    await shot(page, '01-welcome');
  });

  await step('2. Add a project', async () => {
    await page.getByRole('button', { name: 'Add project' }).first().click();
    await page.locator('#project-path').fill(repo);
    await page.waitForTimeout(500);
    await shot(page, '02-add-project');
    await page.getByRole('dialog').getByRole('button', { name: 'Add project' }).click();
    await page.getByRole('heading', { name: 'hello-stack' }).waitFor();
    const text = await page.locator('main').innerText();
    check('the project page shows the repository folder and the branch new agents start from', text.includes(repo) && /New agents from\s*main/.test(text), text);
    await shot(page, '03-project');
  });

  await step('3. New agent with Claude Code: progress streams in, then the terminal opens with Claude running', async () => {
    await page.getByRole('button', { name: 'New agent', exact: true }).first().click();
    const dialog = page.getByRole('dialog');
    await page.waitForTimeout(500);
    await dialog.getByRole('button', { name: 'Create agent' }).click();
    await dialog.getByLabel('Job log').getByText('Creating instance').waitFor({ timeout: 30_000 });
    await shot(page, '04-creating');
    await page.locator(term(A1)).waitFor({ timeout: 120_000 });
    check('the create job finished and the app opened the new agent', true);
    const text = await waitForTerminal(page, A1, /Claude Code/, 60_000);
    check("the terminal shows Claude Code running in agent-01's tmux session", true);
    check('Claude Code does not ask for a login', !/Select login method|Please run \/login|Invalid API key|OAuth token has expired/.test(text), text);
    await page.waitForTimeout(2000);
    await shot(page, '05-claude');
  });

  await step("4. Work in the agent's shell, in the same tmux session", async () => {
    await tmuxWindow(page, A1, 0);
    await typeLine(page, A1, 'hostname; git branch --show-current');
    const text = await waitForTerminal(page, A1, /^agentbox\/agent-01\s*$/m, 15_000);
    check('commands run inside agent-01, on its branch', /^ab-hello-stack-agent-01\s*$/m.test(text), text);
    await shot(page, '06-shell');
  });

  await step("5. A second agent; switching between them keeps each terminal's state", async () => {
    await page.getByRole('button', { name: 'New agent in hello-stack' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('[data-ai="none"]').click();
    await dialog.getByRole('button', { name: 'Create agent' }).click();
    await page.locator(term(A2)).waitFor({ timeout: 120_000 });
    await waitForTerminal(page, A2, /[$#] /, 30_000);
    await typeLine(page, A2, 'echo "hello from $(hostname)"');
    await waitForTerminal(page, A2, /^hello from ab-hello-stack-agent-02/m, 15_000);
    await page.locator(`[data-agent="${A1}"]`).click();
    const back = await waitForTerminal(page, A1, /^agentbox\/agent-01\s*$/m, 10_000);
    check("switching back to agent-01 shows its earlier output", /^ab-hello-stack-agent-01\s*$/m.test(back), back);
    await page.locator(`[data-agent="${A2}"]`).click();
    await waitForTerminal(page, A2, /^hello from ab-hello-stack-agent-02/m, 10_000);
    check("agent-02's terminal kept its output too", true);
    await page.waitForTimeout(1000);
    await shot(page, '07-second-agent');
  });

  await step('6. Host resources in the header, per-agent usage in the sidebar', async () => {
    await page.getByLabel(/^Host CPU:/).waitFor({ timeout: 15_000 });
    await page.getByLabel(/^Host memory:/).waitFor();
    check('the header shows host CPU and memory', true);
    await page.waitForTimeout(2500);
    const row = await page.locator(`[data-agent="${A1}"]`).innerText();
    check('the sidebar shows CPU and memory for agent-01', /\d+% · [\d.]+ (B|KiB|MiB|GiB)/.test(row), row);
  });

  await step('7. The Overview tab', async () => {
    await page.locator(`[data-agent="${A1}"]`).click();
    await page.getByRole('tab', { name: 'Overview' }).click();
    await page.getByLabel('Diff summary').waitFor();
    await page.waitForTimeout(1500);
    const text = await page.locator('main').innerText();
    check(
      'the overview shows the branch, IP address, worktree and resource usage',
      text.includes('agentbox/agent-01') && /10\.\d+\.\d+\.\d+/.test(text) && text.includes('worktrees/hello-stack/agent-01') && /memory\s*[\d.]+ (KiB|MiB|GiB)/i.test(text),
      text,
    );
    await shot(page, '08-overview');
  });

  await step('8. Take a snapshot, break the project, restore it', async () => {
    await page.getByRole('tab', { name: 'Snapshots' }).click();
    await page.locator('[data-snapshot="initial"]').waitFor({ timeout: 15_000 });
    await page.locator('#snapshot-name').fill('before-change');
    await page.getByRole('button', { name: 'Take snapshot' }).click();
    await page.locator('[data-snapshot="before-change"]').waitFor({ timeout: 30_000 });
    check('the new snapshot is listed next to "initial"', true);

    await page.getByRole('tab', { name: 'Terminal' }).click();
    await typeLine(page, A1, 'rm server.mjs && echo broken > message.txt && git status --short');
    await waitForTerminal(page, A1, /^ ?D server\.mjs/m, 15_000);
    await shot(page, '09-broken');

    await page.getByRole('tab', { name: 'Snapshots' }).click();
    await page.locator('[data-snapshot="before-change"]').getByRole('button', { name: 'Restore' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Restore' }).click();
    await page.locator('[data-job-status="succeeded"]').first().waitFor({ timeout: 120_000 });
    await page.waitForTimeout(1000);
    await shot(page, '10-restored');
    const diff = ab('diff', A1, '--stat');
    check('after the restore, the worktree has no changes again', diff.trim() === '', diff);

    await page.getByRole('tab', { name: 'Terminal' }).click();
    await page.locator('[data-session="open"]').waitFor({ timeout: 60_000 });
    await tmuxWindow(page, A1, 0);
    await typeLine(page, A1, 'clear; ls server.mjs && cat message.txt');
    const text = await waitForTerminal(page, A1, /^server\.mjs\s*$/m, 20_000);
    check('the terminal reconnected by itself, and server.mjs and message.txt are back', !/^broken\s*$/m.test(text), text);
    await shot(page, '11-after-restore');
  });

  await step('9. Fork a new agent from the snapshot', async () => {
    await page.getByRole('tab', { name: 'Snapshots' }).click();
    await page.locator('[data-snapshot="before-change"]').getByRole('button', { name: 'Fork' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('#fork-name').fill('forked');
    await dialog.getByRole('button', { name: 'Fork', exact: true }).click();
    await page.locator(term(FORK)).waitFor({ timeout: 120_000 });
    check('the fork finished and opened as a new agent', (await page.locator(`[data-agent="${FORK}"]`).count()) === 1);
    await page.waitForTimeout(1500);
    await shot(page, '12-fork');
  });

  await step('10. Pause and resume', async () => {
    await page.locator(`[data-agent="${A2}"]`).click();
    await page.getByRole('button', { name: 'Pause', exact: true }).click();
    await page.locator('main [data-state="paused"]').first().waitFor({ timeout: 15_000 });
    check('pausing shows "paused" on the agent and in the sidebar', (await page.locator(`[data-agent="${A2}"] [title="paused"]`).count()) === 1);
    await page.waitForTimeout(800);
    await shot(page, '13-paused');
    await page.getByRole('button', { name: 'Resume', exact: true }).click();
    await page.locator('main [data-state="running"]').first().waitFor({ timeout: 15_000 });
    check('resuming brings it back to running', true);
  });

  await step('11. Close the app: the agents keep running', async () => {
    await page.locator(`[data-agent="${A1}"]`).click();
    await page.getByRole('tab', { name: 'Terminal' }).click(); // agent-01 was left on its Snapshots tab
    await page.locator('[data-session="open"]').waitFor({ timeout: 30_000 });
    await typeLine(page, A1, 'echo "typed before closing the app"');
    await waitForTerminal(page, A1, /^typed before closing the app\s*$/m, 10_000);
    await page.waitForTimeout(800);
    await app.close();
    app = undefined;
    const list = ab('list');
    console.log(list);
    check('with the app closed, all three agents are still running', (list.match(/ running /g) ?? []).length === 3, list);
  });

  await step('12. Reopen the app: everything reattaches', async () => {
    ({ app, page } = await launch(xvfb.display, join(work, 'video-2')));
    await page.locator(`[data-agent="${A1}"]`).click({ timeout: 30_000 });
    await page.getByRole('tab', { name: 'Terminal' }).click();
    await page.locator('[data-session="open"]').waitFor({ timeout: 30_000 });
    await waitForTerminal(page, A1, /^typed before closing the app\s*$/m, 15_000);
    check("agent-01's terminal reattached to the same tmux session, with its earlier output", true);
    await page.waitForTimeout(1000);
    await shot(page, '14-reopened');
  });

  await step('13. Destroy the fork, with confirmation', async () => {
    await page.locator(`[data-agent="${FORK}"]`).click();
    await page.getByRole('button', { name: 'More actions' }).click();
    await page.getByRole('menuitem', { name: /Destroy agent/ }).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('#destroy-branch').click();
    await page.waitForTimeout(500);
    await shot(page, '15-destroy');
    await dialog.getByRole('button', { name: 'Destroy', exact: true }).click();
    await page.locator(`[data-agent="${FORK}"]`).waitFor({ state: 'detached', timeout: 60_000 });
    const branch = run('git', ['-C', repo, 'branch', '--list', 'agentbox/forked']);
    check('the fork is gone from the sidebar, and its branch was deleted', branch.trim() === '', branch);
    await page.waitForTimeout(1500);
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  for (const ref of [FORK, A2, A1]) ab('destroy', ref, '--force', '--delete-branch');
  ab('remove', 'hello-stack');
  ab('daemon', 'stop');
  xvfb?.stop();

  const videos = ['video-1', 'video-2']
    .map((dir) => join(work, dir))
    .filter((dir) => existsSync(dir))
    .flatMap((dir) => readdirSync(dir).filter((f) => f.endsWith('.webm')).map((f) => join(dir, f)));
  if (videos.length > 0) {
    const list = join(work, 'videos.txt');
    writeFileSync(list, videos.map((v) => `file '${v}'`).join('\n'));
    const mp4 = join(media, 'step-6-desktop.mp4');
    const gif = join(media, 'step-6-desktop.gif');
    run('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', list, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '30', '-preset', 'veryfast', '-movflags', '+faststart', mp4]);
    run('ffmpeg', [
      '-y', '-loglevel', 'error', '-i', mp4,
      '-vf', 'setpts=PTS/2,fps=6,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4',
      gif,
    ]);
    log(`video: ${videos.length} recording(s) joined into ${mp4.replace(root + '/', '')} and a 2x GIF`);
  }
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
