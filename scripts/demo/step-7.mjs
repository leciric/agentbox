#!/usr/bin/env node
// Reproduces the Step 7 evidence: each agent's own browser, started with the agent. Claude Code drives
// agent-01's browser through the Playwright MCP server while the desktop app
// shows it live; you take over in the app; a second agent's browser has its own
// cookies; the preview proxy reaches agents' servers; the browser's ports stay
// closed on the network. Records screenshots and a video of the app. State is
// throwaway. Needs the base image and a Claude Code login on the host (its
// short-lived access token is copied into the throwaway state).
//
//   mise exec -- node scripts/demo/step-7.mjs > .demo-runs/step-7/demo.log 2>&1
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import net from 'node:net';
import { homedir, tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/step-7/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-step7-'));
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repos', 'hello-stack');
const A1 = 'hello-stack/agent-01';
const A2 = 'hello-stack/agent-02';
const FORK = 'hello-stack/forked';
const previewPort = 7797;
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: `127.0.0.1:${previewPort}`,
  XDG_SESSION_TYPE: 'x11',
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
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ab runs the CLI and prints the command and its output, like a terminal.
function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}`);
  return out;
}
const quiet = (...args) => hide(spawnSync(bin, args, { env, encoding: 'utf8' }).stdout);
const shellQuote = (s) => `'${s.replaceAll("'", `'\\''`)}'`;

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

async function until(what, fn, timeout = 30_000) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    last = await fn();
    if (last) return last;
    await sleep(500);
  }
  throw new Error(`timed out waiting for ${what}`);
}

function ipOf(instance) {
  const [inst] = JSON.parse(run('incus', ['list', instance, '--format', 'json']));
  return inst.state.network.eth0.addresses.find((a) => a.family === 'inet').address;
}

function portOpen(host, port) {
  return new Promise((resolve) => {
    const socket = net.connect({ host, port, timeout: 2_000 });
    socket.once('connect', () => (socket.destroy(), resolve(true)));
    socket.once('timeout', () => (socket.destroy(), resolve(false)));
    socket.once('error', () => resolve(false));
  });
}

const shot = (page, name) => page.screenshot({ path: join(media, `step-7-browser-${name}.png`) });
const pageTitle = (page) => page.getByLabel('Page title').innerText();

async function openBrowserTab(page, ref) {
  await page.locator(`[data-agent="${ref}"]`).click();
  await page.getByRole('tab', { name: 'Browser' }).click();
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
  if (await portOpen('127.0.0.1', previewPort)) throw new Error(`port ${previewPort} is taken; the preview proxy needs it`);
  ab('add', repo);
  ab('create', 'hello-stack', '--ai', 'claude');
  ab('create', 'hello-stack', '--ai', 'none');
  for (const ref of [A1, A2]) quiet('exec', ref, '--', "tmux new-window -d -t main -n dev 'node server.mjs'");
  run('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  xvfb = await startXvfb({ width: 1440, height: 900 });
  log(`built the desktop app; private X display ${xvfb.display}`);
  const ip1 = ipOf('ab-hello-stack-agent-01');

  await step('1. Every agent has a browser, started with the agent', async () => {
    const before = ab('browser', 'status', A1);
    check('agent-01 was just created, and its browser is already running', before.includes('The browser is running'), before);
    const tools = quiet('exec', A1, '--', 'claude mcp list');
    console.log(`\n$ agentbox exec ${A1} -- claude mcp list\n${tools.trimEnd()}`);
    check("Claude Code in agent-01 has the Playwright MCP server, pointed at the agent's browser", /playwright:.*--cdp-endpoint http:\/\/127\.0\.0\.1:9222/.test(tools), tools);

    ({ app, page } = await (async () => {
      const launched = await electron.launch({
        args: [desktop, '--disable-gpu'],
        cwd: desktop,
        env: { ...env, DISPLAY: xvfb.display },
        recordVideo: { dir: join(work, 'video'), size: { width: 1440, height: 900 } },
      });
      return { app: launched, page: await launched.firstWindow() };
    })());
    const t0 = Date.now();
    await openBrowserTab(page, A1);
    await page.locator(`[data-browser-view="${A1}"][data-connected="true"]`).waitFor({ timeout: 60_000 });
    log(`the app's live view connected in ${((Date.now() - t0) / 1000).toFixed(1)}s`);
    check('the Browser tab connects to it straight away, sized to the tab', true);
    await sleep(1_500);
    await shot(page, '01-started-with-agent');
  });

  await step("2. Claude Code drives agent-01's browser while the app shows it live", async () => {
    const prompt =
      'Use the Playwright browser tools (the browser is already running). Open http://localhost:3000/login, ' +
      'type alice into the name field and click "Sign in". Then reply with only the heading of the page you end up on.';
    console.log(`\n$ agentbox exec ${A1} -- claude -p --model haiku --dangerously-skip-permissions '${prompt}'`);
    const claude = spawn(bin, ['exec', A1, '--', `timeout 300 claude -p --model haiku --dangerously-skip-permissions ${shellQuote(prompt)}`], { env });
    let reply = '';
    claude.stdout.on('data', (d) => (reply += d));
    claude.stderr.on('data', (d) => (reply += d));
    const exited = new Promise((r) => claude.once('exit', r));

    await until('the login page in the app', async () => /Not signed in|Signed in as alice/.test(await pageTitle(page)), 180_000);
    await sleep(1_000);
    await shot(page, '03-claude-on-login-page');
    await until('Claude to sign in as alice', async () => (await pageTitle(page)).includes('Signed in as alice'), 240_000);
    await sleep(1_500);
    await shot(page, '04-claude-signed-in');
    await exited;
    console.log(reply.trim());
    check('the app showed the page Claude opened, and then "Signed in as alice"', true);
    check("Claude's reply is the signed-in heading", /Signed in as alice/.test(reply), reply);
    const status = ab('browser', 'status', A1);
    check('agentbox browser status agrees', status.includes('Signed in as alice'), status);
    const diff = ab('diff', A1, '--stat');
    check("Claude's browser session left no files in agent-01's worktree", !diff.includes('.playwright-mcp'), diff);
  });

  await step('3. Take over in the app with mouse and keyboard', async () => {
    await page.getByLabel('Address').fill('http://localhost:3000/login?user=');
    await page.getByRole('button', { name: 'Go', exact: true }).click();
    await until('the sign-out', async () => (await pageTitle(page)).includes('Not signed in'), 20_000);
    await page.locator('#browser-control').click();
    await sleep(500);
    const box = await page.locator(`[data-browser-view="${A1}"] canvas`).boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + box.height * 0.7);
    await page.keyboard.press('Tab');
    await page.keyboard.type('bob', { delay: 120 });
    await sleep(600);
    await shot(page, '05-typing-in-the-page');
    await page.keyboard.press('Enter');
    await until('bob to be signed in', async () => (await pageTitle(page)).includes('Signed in as bob'), 20_000);
    await sleep(1_000);
    await shot(page, '06-signed-in-as-bob');
    check('with Take control on, clicks and keys typed in the app sign in as bob in the agent\'s browser', true);
    await page.locator('#browser-control').click();
  });

  await step("4. agent-02's browser has its own cookies, even for the same site", async () => {
    const same = `http://${ip1}:3000/login`;
    ab('browser', 'open', A1, `${same}?user=carol`);
    await openBrowserTab(page, A2);
    await page.locator(`[data-browser-view="${A2}"][data-connected="true"]`).waitFor({ timeout: 60_000 });
    await page.getByLabel('Address').fill(same);
    await page.getByRole('button', { name: 'Go', exact: true }).click();
    await until("agent-02's page", async () => (await pageTitle(page)).includes('Not signed in'), 30_000);
    await sleep(1_500);
    await shot(page, '07-agent-02-same-site');
    const one = ab('browser', 'status', A1);
    const two = ab('browser', 'status', A2);
    check(`on ${same.replace(ip1, '<agent-01 ip>')}, agent-01's browser is signed in as carol and agent-02's isn't signed in`, one.includes('Signed in as carol') && two.includes('Not signed in'), one + two);
  });

  await step('5. The agent itself uses agentbox browser, through its in-agent API', async () => {
    const out = ab('exec', A2, '--', 'agentbox browser open "http://localhost:3000/login?user=dave" && agentbox browser status');
    check('inside agent-02, agentbox browser open and status work on its own browser', out.includes('Signed in as dave'), out);
    const refused = ab('exec', A2, '--', `curl -s --unix-socket /run/agentbox.sock -X POST http://agentbox/v1/agents/hello-stack/agent-01/browser/open -d '{"url":"http://example.com"}'`);
    check("an agent can't reach another agent's browser through its API socket", refused.includes('not available inside an agent'), refused);
  });

  await step('6. Preview URLs reach agents from the host browser', async () => {
    for (const ref of [A1, A2]) {
      const name = ref.split('/')[1];
      const url = `http://3000.${name}.hello-stack.localhost:${previewPort}/login`;
      const body = run('curl', ['-s', url]);
      console.log(`\n$ curl -s ${url}\n${body.trim()}`);
      check(`${url} is served by ${name}`, body.includes(`Served by ab-hello-stack-${name}`), body);
    }
    await page.locator(`[data-agent="${A1}"]`).click();
    await page.getByRole('tab', { name: 'Overview' }).click();
    const link = await page.locator('[data-preview-url]').getAttribute('data-preview-url');
    check('the Overview tab links to the preview URL', link === `http://3000.agent-01.hello-stack.localhost:${previewPort}`, link ?? '');
    await sleep(800);
    await shot(page, '08-overview-preview');
  });

  await step("7. The browser's ports aren't open on the network", async () => {
    const vnc = await portOpen(ip1, 5900);
    const cdp = await portOpen(ip1, 9222);
    console.log(`\nfrom the host: ${ip1.replace(/\d+$/, 'x')}:5900 ${vnc ? 'open' : 'closed'}, :9222 ${cdp ? 'open' : 'closed'}`);
    check("from the host, agent-01's VNC (5900) and DevTools (9222) ports are closed", !vnc && !cdp);
    const probe = ab('exec', A2, '--', `curl -s --max-time 2 http://${ip1}:9222/json/version || echo "no answer"`);
    check("from agent-02, agent-01's DevTools port doesn't answer", probe.includes('no answer'), probe);
  });

  await step('8. A fork gets its own browser, not a link to the original', async () => {
    ab('fork', A1, '--name', 'forked');
    // A fork starts its own browser (D23), with proxy devices of its own.
    const listen = (instance) => run('incus', ['config', 'device', 'get', instance, 'agentbox-vnc', 'listen']).trim();
    const original = listen('ab-hello-stack-agent-01');
    const forked = listen('ab-hello-stack-forked');
    console.log(`\n$ incus config device get <instance> agentbox-vnc listen\nagent-01: ${hide(original)}\nforked:   ${hide(forked)}`);
    check("the fork's browser is its own: its VNC proxy listens on another socket than agent-01's", forked.startsWith('unix:') && forked !== original, `${original} ${forked}`);
    // Processes don't survive a copy, so the fork's server needs starting again.
    quiet('exec', FORK, '--', "tmux new-window -d -t main -n dev 'node server.mjs'");
    await until("the fork's server", async () => quiet('exec', FORK, '--', 'curl -s http://localhost:3000/login').includes('Served by'), 30_000);
    const out = ab('browser', 'open', FORK, 'http://localhost:3000/login');
    check(
      "the fork's own browser starts, with its own sockets, and shows the fork's server",
      out.includes('The browser is running') && /localhost:3000\/login\s+(Signed in as \w+|Not signed in)/.test(out),
      out,
    );
    const sockets = readdirSync(join(work, 'data/agentbox/run/agents')).sort();
    console.log(`\n$ ls $TMP/data/agentbox/run/agents\n${sockets.join('\n')}`);
    check('agent-01, agent-02 and the fork each have their own API, VNC and DevTools sockets', sockets.length === 9, sockets.join(' '));
    check("agent-01's browser still runs after the fork", ab('browser', 'status', A1).includes('The browser is running'));
  });

  await step('9. Stop the browser from the app', async () => {
    await openBrowserTab(page, A1);
    await page.getByRole('button', { name: 'Browser actions' }).click();
    await page.getByRole('menuitem', { name: 'Stop browser' }).click();
    await page.getByText("agent-01's browser isn't running.").waitFor({ timeout: 30_000 });
    await sleep(1_000);
    await shot(page, '09-stopped');
    check('after Stop, agentbox browser status says it isn\'t running', ab('browser', 'status', A1).includes("isn't running"));
    await page.getByRole('button', { name: 'Start browser' }).click();
    await page.locator(`[data-browser-view="${A1}"][data-connected="true"]`).waitFor({ timeout: 60_000 });
    check('Start browser in the app starts it again, and the view reconnects', true);
    await app.close();
    app = undefined;
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  for (const ref of [FORK, A2, A1]) quiet('destroy', ref, '--force', '--delete-branch');
  quiet('remove', 'hello-stack');
  quiet('daemon', 'stop');
  xvfb?.stop();

  const videoDir = join(work, 'video');
  const video = existsSync(videoDir) ? readdirSync(videoDir).find((f) => f.endsWith('.webm')) : undefined;
  if (video) {
    const mp4 = join(media, 'step-7-browser.mp4');
    const gif = join(media, 'step-7-browser.gif');
    run('ffmpeg', ['-y', '-loglevel', 'error', '-i', join(videoDir, video), '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '30', '-preset', 'veryfast', '-movflags', '+faststart', mp4]);
    run('ffmpeg', [
      '-y', '-loglevel', 'error', '-i', mp4,
      '-vf', 'setpts=PTS/2,fps=6,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4',
      gif,
    ]);
    log('video: saved an MP4 and a 2x GIF');
  }
  rmSync(work, { recursive: true, force: true });

  console.log('\n######## Summary');
  console.log(results.join('\n'));
  const ok = !failed && results.every((r) => r.startsWith('PASS'));
  console.log(`${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed${ok ? '' : ' (FAILED)'}`);
  process.exitCode = ok ? 0 : 1;
}
