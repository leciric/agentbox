#!/usr/bin/env node
// Reproduces the packaging evidence: builds the AppImage, runs it the way a new
// user would (a fresh home, no agentbox on PATH), installs the command-line
// tool from the Setup page, and checks that the daemon runs the app's own
// binary. Records screenshots and a video. Needs the machine set up for
// AgentBox (Incus and the base image); your own home and state aren't used.
//
//   mise exec -- node scripts/demo/packaging.mjs > .demo-runs/packaging/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, readlinkSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/packaging/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');
const { version } = JSON.parse(readFileSync(join(desktop, 'package.json'), 'utf8'));

const work = mkdtempSync(join(tmpdir(), 'agentbox-pkg-'));
const home = join(work, 'home');
// A new user's environment: nothing from this session's PATH or AgentBox variables.
const env = {
  PATH: '/usr/local/sbin:/usr/local/bin:/usr/bin:/bin',
  HOME: home,
  LANG: 'C.UTF-8',
  XDG_DATA_HOME: join(work, 'data'),
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_SESSION_TYPE: 'x11',
  AGENTBOX_PREVIEW_ADDR: '127.0.0.1:7799',
};

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$TMP').replaceAll(root, '$REPO');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// sh runs a command in a login shell of the new user, and prints it like a terminal.
function sh(command) {
  const r = spawnSync('bash', ['-lc', command], { env, encoding: 'utf8', cwd: home });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ ${command}\n${out.trimEnd()}`);
  return out;
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

const shot = (page, name) => page.screenshot({ path: join(media, `packaging-${name}.png`) });

let xvfb;
let app;
let page;
let failed = false;
const appImage = join(desktop, 'dist', `AgentBox-${version}-x86_64.AppImage`);
try {
  console.log('######## Build the AppImage (npm run dist)');
  mkdirSync(media, { recursive: true });
  mkdirSync(home, { recursive: true });
  execFileSync('npm', ['run', 'dist'], { cwd: desktop, stdio: ['ignore', 'ignore', 'inherit'] });
  const digest = createHash('sha256').update(readFileSync(appImage)).digest('hex');
  console.log(`${hide(appImage)}\n  ${(statSync(appImage).size / 2 ** 20).toFixed(0)} MiB, sha256 ${digest}`);
  // Debian's and Ubuntu's default ~/.profile put ~/.local/bin on PATH; this home does the same.
  writeFileSync(join(home, '.bash_profile'), 'export PATH="$HOME/.local/bin:$PATH"\n');
  xvfb = await startXvfb({ width: 1440, height: 900 });

  await step('1. The AppImage has the command-line tool inside', async () => {
    execFileSync(appImage, ['--appimage-extract', 'resources/bin/agentbox'], { cwd: work, stdio: 'ignore' });
    const inside = join(work, 'squashfs-root/resources/bin/agentbox');
    const out = spawnSync(inside, ['--version'], { encoding: 'utf8' }).stdout.trim();
    console.log(`\n$ ./AgentBox-${version}-x86_64.AppImage --appimage-extract resources/bin/agentbox && squashfs-root/resources/bin/agentbox --version\n${out}`);
    check(`the AppImage carries agentbox ${version}`, out === `agentbox version ${version}`, out);
    const before = sh('command -v agentbox || echo "agentbox: not found"');
    check('the new user has no agentbox command yet', before.includes('not found'), before);
  });

  await step('2. Run the AppImage: it starts the daemon with its own copy of agentbox', async () => {
    app = await electron.launch({
      executablePath: appImage,
      args: ['--disable-gpu'],
      env: { ...env, DISPLAY: xvfb.display },
      recordVideo: { dir: join(work, 'video'), size: { width: 1440, height: 900 } },
    });
    page = await app.firstWindow();
    await page.getByText('Welcome to AgentBox').waitFor({ timeout: 60_000 });
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 60_000 });
    await sleep(1_500);
    await shot(page, '01-welcome');
    const daemon = execFileSync('pgrep', ['-af', 'agentbox daemon'], { encoding: 'utf8' })
      .split('\n')
      .find((line) => line.includes(work));
    console.log(`\n$ pgrep -af "agentbox daemon"\n${hide(daemon)}`);
    check("the daemon runs from the app's copy in ~/.local/share/agentbox/bin", daemon?.includes(join(work, 'data/agentbox/bin/agentbox daemon')), daemon);
  });

  await step('3. Install the command-line tool from the Setup page', async () => {
    await page.getByRole('button', { name: 'Setup', exact: true }).click();
    await page.locator('[data-setup-step="Command-line tool"][data-status="missing"]').waitFor({ timeout: 30_000 });
    await sleep(2_500);
    await shot(page, '02-setup-before');
    await page.getByRole('button', { name: 'Install command-line tool' }).click();
    await page.locator('[data-setup-step="Command-line tool"][data-status="ok"]').waitFor({ timeout: 30_000 });
    await sleep(1_500);
    await shot(page, '03-setup-installed');
    const link = join(home, '.local/bin/agentbox');
    const target = lstatSync(link).isSymbolicLink() ? readlinkSync(link) : '';
    console.log(`\n~/.local/bin/agentbox -> ${hide(target)}`);
    check("Install links ~/.local/bin/agentbox to the app's copy", target === join(work, 'data/agentbox/bin/agentbox'), target);
    const out = sh('agentbox --version && agentbox host check');
    check(`in a new shell, agentbox is version ${version}, and host check passes`, out.includes(`agentbox version ${version}`) && !/missing|not ready/i.test(out), out);
    const progress = await page.locator('[data-setup-progress]').getAttribute('data-setup-progress');
    check('every required step on the Setup page is ready', progress === '4/4', progress ?? '');
    const footer = await page.getByText(/^AgentBox \d/).innerText();
    check(`the Setup page shows the packaged app's version, ${version}`, footer.includes(`AgentBox ${version}`), footer);
  });

  await step('4. Closing the app leaves the daemon running; the CLI talks to it', async () => {
    await app.close();
    app = undefined;
    const out = sh('agentbox list; agentbox daemon stop');
    check('after the app closed, agentbox list reaches the same daemon, and stops it', out.includes('Stopped the AgentBox daemon'), out);
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Cleanup (off camera)');
  await app?.close().catch(() => {});
  spawnSync('bash', ['-lc', 'agentbox daemon stop'], { env });
  xvfb?.stop();
  const videoDir = join(work, 'video');
  const video = existsSync(videoDir) ? readdirSync(videoDir).find((f) => f.endsWith('.webm')) : undefined;
  if (video) {
    const mp4 = join(media, 'packaging.mp4');
    execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-i', join(videoDir, video), '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '30', '-preset', 'veryfast', '-movflags', '+faststart', mp4]);
    execFileSync('ffmpeg', [
      '-y', '-loglevel', 'error', '-i', mp4,
      '-vf', 'setpts=PTS/2,fps=6,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4',
      join(media, 'packaging.gif'),
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
