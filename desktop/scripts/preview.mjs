// Serves src/renderer/dev/preview.tsx — the standing preview of the rail and
// sidebar under wide content — and, with --shots, drives it headlessly
// through Playwright and saves one screenshot per scenario in
// src/renderer/dev/scenarios.json.
//
//   npm run preview                          serve it, print the URL, stay running
//   npm run preview -- --shots out           capture every scenario into out/*.png, then exit
//   npm run preview -- --shots out --lang pt-BR
//                                             every scenario in another language, whose
//                                             strings are longer than English's
//   npm run preview -- --shots out --against HEAD~1
//                                             also capture --against's ref, from a throwaway
//                                             git worktree, into out/before/*.png, and the
//                                             working tree into out/after/*.png
//
// --against needs the ref to already have this preview harness (dev/preview.html
// and scenarios.json) — it was added after AgentRail's overflow fix, so it can
// diff any two points after that, not further back.
//
// Playwright drives the system's Chromium at /usr/bin/chromium when there is
// one, as in every agent's machine, so it needn't download its own (658 MB).
// Elsewhere it uses Playwright's own: run `npx playwright install chromium`
// once if launching it fails.
import { execFileSync, spawn } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, symlinkSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const desktopDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = join(desktopDir, '..');
const scenarios = JSON.parse(readFileSync(join(desktopDir, 'src/renderer/dev/scenarios.json'), 'utf8'));

function parseArgs(argv) {
  const args = { shots: null, against: null, lang: null };
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--shots') args.shots = argv[++i];
    else if (argv[i] === '--against') args.against = argv[++i];
    else if (argv[i] === '--lang') args.lang = argv[++i];
    else throw new Error(`Unknown argument: ${argv[i]}`);
  }
  return args;
}

function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.unref();
    server.on('error', reject);
    server.listen(0, () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

// startVite serves dev/preview.html for the tree at cwd (the working tree, or
// a worktree's desktop/ directory), and resolves once it's actually
// answering — vite prints "ready" before its HTTP server necessarily is.
// The What's new view imports CHANGELOG.md from where build.mjs copies it,
// so it's copied in here too: a fresh checkout doesn't have it.
async function startVite(cwd, port) {
  copyFileSync(join(cwd, '../CHANGELOG.md'), join(cwd, 'src/renderer/changelog.md'));
  const bin = join(cwd, 'node_modules/.bin/vite');
  const proc = spawn(bin, ['--config', 'vite.config.mts', '--port', String(port), '--strictPort'], { cwd, stdio: ['ignore', 'pipe', 'pipe'] });
  let output = '';
  proc.stdout.on('data', (d) => (output += d));
  proc.stderr.on('data', (d) => (output += d));

  const url = `http://localhost:${port}`;
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) throw new Error(`vite exited early (${proc.exitCode}) in ${cwd}:\n${output}`);
    try {
      const res = await fetch(`${url}/dev/preview.html`);
      if (res.ok) return { proc, url };
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  proc.kill();
  throw new Error(`vite never became ready on ${url}:\n${output}`);
}

function stopVite(proc) {
  return new Promise((resolve) => {
    proc.once('exit', resolve);
    proc.kill('SIGTERM');
  });
}

// worktreeAt checks out ref into a throwaway directory alongside this one —
// the same primitive AgentBox gives every agent — and symlinks node_modules
// in rather than reinstalling: fine for the adjacent commits this is meant
// to diff, since package.json rarely moves between them.
function worktreeAt(ref) {
  const dir = join(tmpdir(), `agentbox-preview-${Date.now()}`);
  execFileSync('git', ['worktree', 'add', '--detach', dir, ref], { cwd: repoRoot, stdio: 'inherit' });
  const desktop = join(dir, 'desktop');
  if (!existsSync(join(desktop, 'src/renderer/dev/preview.html'))) {
    execFileSync('git', ['worktree', 'remove', dir, '--force'], { cwd: repoRoot, stdio: 'inherit' });
    throw new Error(`${ref} has no src/renderer/dev/preview.html — --against needs a ref that already carries this preview harness.`);
  }
  symlinkSync(join(desktopDir, 'node_modules'), join(desktop, 'node_modules'), 'dir');
  return desktop;
}

function removeWorktree(desktop) {
  execFileSync('git', ['worktree', 'remove', dirname(desktop), '--force'], { cwd: repoRoot, stdio: 'inherit' });
}

// overflowing lists what sticks out of the page's fixed-width columns — the
// rail and the sidebar are <aside>s — so a shot that breaks one says so
// without anybody having to spot it in the picture.
function overflowing() {
  const found = [];
  for (const aside of document.querySelectorAll('aside')) {
    const box = aside.getBoundingClientRect();
    if (box.width === 0) continue;
    for (const el of aside.querySelectorAll('*')) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || (r.right <= box.right + 1 && r.left >= box.left - 1)) continue;
      // Only the outermost element that sticks out, not each of its children.
      if (el.parentElement && found.some((f) => f.el.contains(el))) continue;
      found.push({ el, text: (el.textContent ?? '').trim().slice(0, 60), by: Math.round(Math.max(r.right - box.right, box.left - r.left)) });
    }
  }
  return found.map(({ text, by }) => `${by}px: ${text || '(no text)'}`);
}

async function capture(baseUrl, outDir, lang) {
  const { chromium } = await import('playwright');
  mkdirSync(outDir, { recursive: true });
  const systemChromium = '/usr/bin/chromium';
  const browser = await chromium.launch(existsSync(systemChromium) ? { executablePath: systemChromium } : {});
  try {
    for (const s of scenarios) {
      const page = await browser.newPage({ viewport: { width: s.width, height: s.height }, reducedMotion: s.reducedMotion ?? 'no-preference' });
      await page.goto(`${baseUrl}/dev/preview.html?${s.query}${lang ? `&lang=${lang}` : ''}`, { waitUntil: 'networkidle' });
      // A scenario can hover an element, to show its tooltip.
      if (s.hover) {
        await page.hover(s.hover, { timeout: 3_000 }).catch(() => {});
        await page.waitForSelector('[data-radix-popper-content-wrapper]', { timeout: 3_000 }).catch(() => {});
      }
      // A scenario can click an element, to show a popover it opens.
      if (s.click) {
        await page.click(s.click, { timeout: 3_000 }).catch(() => {});
        await page.waitForSelector('[data-radix-popper-content-wrapper]', { timeout: 3_000 }).catch(() => {});
      }
      // A scenario can type into a field, to show what a search leaves:
      // "fill": [selector, text].
      if (s.fill) {
        await page.fill(s.fill[0], s.fill[1], { timeout: 3_000 }).catch(() => {});
        await page.waitForTimeout(300);
      }
      // Animations (the avatars') are stopped at their start, so a shot is the
      // same every time and a before/after diff shows changes, not timing.
      await page.screenshot({ path: join(outDir, `${s.id}.png`), animations: 'disabled' });
      const over = await page.evaluate(overflowing);
      await page.close();
      console.log(`  ${s.id}.png — ${s.description}`);
      for (const o of over) console.log(`    overflows the column by ${o}`);
    }
  } finally {
    await browser.close();
  }
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const port = await freePort();
  const server = await startVite(desktopDir, port);
  console.log(`Serving the working tree at ${server.url}/dev/preview.html`);

  if (!args.shots) {
    console.log('Leave this running and open scenario URLs directly (see the comment atop preview.tsx), or Ctrl-C to stop.');
    return; // process stays alive with vite still attached to it
  }

  if (!args.against) {
    console.log(`Capturing ${scenarios.length} scenarios into ${args.shots}/`);
    await capture(server.url, args.shots, args.lang);
    await stopVite(server.proc);
    return;
  }

  console.log(`Capturing the working tree into ${args.shots}/after/`);
  await capture(server.url, join(args.shots, 'after'), args.lang);
  await stopVite(server.proc);

  console.log(`Checking out ${args.against} into a throwaway worktree…`);
  const beforeDesktop = worktreeAt(args.against);
  try {
    const beforePort = await freePort();
    const beforeServer = await startVite(beforeDesktop, beforePort);
    console.log(`Capturing ${args.against} into ${args.shots}/before/`);
    await capture(beforeServer.url, join(args.shots, 'before'), args.lang);
    await stopVite(beforeServer.proc);
  } finally {
    rmSync(join(beforeDesktop, 'node_modules'), { force: true }); // the symlink, not its target
    removeWorktree(beforeDesktop);
  }

  console.log(`Done — compare ${args.shots}/before/ against ${args.shots}/after/.`);
}

await main();
