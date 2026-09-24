#!/usr/bin/env node
// Records a terminal demo as MP4 + GIF, plus PNG screenshots, without touching
// the desktop: ttyd serves a bash session and a headless Chromium (Playwright)
// types into it while recording the page.
//
//   mise exec -- node scripts/record/term.mjs scripts/record/scenes/<scene>.mjs
//
// Run it from the repository root. A scene module default-exports:
//   name       prefix for the output files
//   out        output directory
//   rc         bash run before the first frame (exports, preparation); $RECORD_WORK
//              is a throwaway directory shared with teardown
//   teardown   bash run after recording, even when it fails (optional)
//   steps      [{ comment } | { run, timeout, hold, wait } | { type } | { keys: [...] }
//               | { waitFor: RegExp, timeout } | { screenshot } | { pause }]
//   width, height, fontSize, rcTimeout (optional)

import { execFileSync, spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';

const PROMPT = '❯';

const scene = (await import(pathToFileURL(resolve(process.argv[2] ?? '')).href)).default;
const width = scene.width ?? 1280;
const height = scene.height ?? 780;
const out = resolve(scene.out);
mkdirSync(out, { recursive: true });

const work = mkdtempSync(join(tmpdir(), 'agentbox-record-'));
const env = { ...process.env, RECORD_WORK: work, TERM: 'xterm-256color' };
const rcFile = join(work, 'rc.sh');
writeFileSync(rcFile, `${scene.rc ?? ''}\nPS1='\\[\\e[1;36m\\]${PROMPT}\\[\\e[0m\\] '\nclear\n`);
// ttyd 1.7.7 crashes when the command it starts has arguments of its own, so start bash through a script.
const shellFile = join(work, 'shell.sh');
writeFileSync(shellFile, `#!/bin/sh\nexec bash --rcfile '${rcFile}' -i\n`, { mode: 0o755 });

const outputs = [];
let ttyd;
let browser;
try {
  const port = await freePort();
  const theme = JSON.stringify({ background: '#11111b', foreground: '#cdd6f4', cursor: '#f5e0dc' });
  ttyd = spawn('ttyd', [
    '-p', String(port), '-i', '127.0.0.1', '-W', '-o',
    '-t', `fontSize=${scene.fontSize ?? 17}`, '-t', `theme=${theme}`,
    '-t', 'disableLeaveAlert=true', '-t', 'disableResizeOverlay=true',
    shellFile,
  ], { env, stdio: 'ignore' });
  await waitForHTTP(`http://127.0.0.1:${port}/`);

  browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width, height },
    recordVideo: { dir: work, size: { width, height } },
  });
  const page = await context.newPage();
  const recordingStarted = Date.now();
  await page.goto(`http://127.0.0.1:${port}/`);
  await page.waitForFunction(() => window.term !== undefined, null, { timeout: 30_000 });

  const cursorLine = () => page.evaluate(() => {
    const b = window.term.buffer.active;
    return b.getLine(b.baseY + b.cursorY)?.translateToString(true) ?? '';
  });
  const screenText = () => page.evaluate(() => {
    const b = window.term.buffer.active;
    const lines = [];
    for (let i = 0; i < b.length; i++) lines.push(b.getLine(i)?.translateToString(true) ?? '');
    return lines.join('\n');
  });
  const waitForPrompt = async (timeout) => {
    for (const deadline = Date.now() + timeout; Date.now() < deadline; await page.waitForTimeout(150)) {
      if ((await cursorLine()).trim() === PROMPT) return;
    }
    throw new Error(`no prompt after ${timeout} ms; screen:\n${await screenText()}`);
  };
  const runLine = async (text, timeout, hold) => {
    await page.keyboard.type(text, { delay: 25 });
    await page.waitForTimeout(250);
    await page.keyboard.press('Enter');
    await waitForPrompt(timeout);
    await page.waitForTimeout(hold);
  };

  await waitForPrompt(scene.rcTimeout ?? 300_000);
  const trimStart = Math.max(0, (Date.now() - recordingStarted) / 1000 - 0.3);
  await page.focus('.xterm-helper-textarea');
  await page.waitForTimeout(600);

  for (const step of scene.steps) {
    if (step.comment) {
      await runLine(`# ${step.comment}`, 5_000, 400);
    } else if (step.run) {
      if (step.wait === false) {
        await page.keyboard.type(step.run, { delay: 25 });
        await page.keyboard.press('Enter');
        await page.waitForTimeout(step.hold ?? 500);
      } else {
        await runLine(step.run, step.timeout ?? 120_000, step.hold ?? 1_200);
      }
    } else if (step.type) {
      await page.keyboard.type(step.type, { delay: 25 });
    } else if (step.keys) {
      for (const key of step.keys) {
        await page.keyboard.press(key);
        await page.waitForTimeout(200);
      }
    } else if (step.waitFor) {
      const deadline = Date.now() + (step.timeout ?? 60_000);
      while (!step.waitFor.test(await screenText())) {
        if (Date.now() > deadline) throw new Error(`timed out waiting for ${step.waitFor}`);
        await page.waitForTimeout(250);
      }
      await page.waitForTimeout(step.hold ?? 800);
    } else if (step.prompt) {
      await waitForPrompt(step.timeout ?? 60_000);
    } else if (step.screenshot) {
      const path = join(out, `${scene.name}-${step.screenshot}.png`);
      await page.screenshot({ path });
      outputs.push(path);
    } else if (step.pause) {
      await page.waitForTimeout(step.pause);
    }
  }
  await page.waitForTimeout(1_000);

  const video = page.video();
  await context.close();
  const webm = await video.path();
  const base = join(out, scene.name);
  const ffmpeg = (...args) => execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-ss', trimStart.toFixed(2), '-i', webm, ...args]);
  ffmpeg('-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '24', '-preset', 'veryfast', '-movflags', '+faststart', `${base}.mp4`);
  ffmpeg('-vf', `fps=8,scale=${scene.gifWidth ?? 1000}:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=5`, `${base}.gif`);
  outputs.push(`${base}.mp4`, `${base}.gif`);
} finally {
  await browser?.close();
  ttyd?.kill();
  if (scene.teardown) {
    try {
      execFileSync('bash', ['-c', scene.teardown], { env, stdio: 'inherit' });
    } catch (err) {
      console.error(`teardown failed: ${err.message}`);
    }
  }
  rmSync(work, { recursive: true, force: true });
}
console.log(outputs.join('\n'));

function freePort() {
  return new Promise((resolvePort) => {
    const server = createServer();
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close(() => resolvePort(port));
    });
  });
}

async function waitForHTTP(url) {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(url)).ok) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`${url} did not answer`);
}
