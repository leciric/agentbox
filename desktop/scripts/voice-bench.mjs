// Feeds recorded speech through push-to-talk's own Whisper worker
// (src/renderer/lib/voice) in Chromium, the way the app's microphone would,
// and prints what it heard, its word error rate and how long it took. A
// machine with no microphone — every agent's — tests it this way.
//
//   npm run voice-bench -- --dir <dir> [--models whisper-base,whisper-small] [--gpu] [--language auto]
//
// <dir> holds <id>.f32 files, 16 kHz mono float32 (ffmpeg -ac 1 -ar 16000
// -f f32le), and a sentences.tsv of "<id-prefix>\t<language>\t<what was said>"
// lines; <id>.f32 is scored against the line whose id it starts with. --gpu
// starts Chromium with WebGPU on; without it Whisper runs on the CPU (WASM).
import { spawn } from 'node:child_process';
import { copyFileSync, existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const desktopDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const args = { dir: '', models: 'whisper-base,whisper-small', gpu: false, language: 'auto', out: '' };
for (let i = 2; i < process.argv.length; i++) {
  const a = process.argv[i];
  if (a === '--gpu') args.gpu = true;
  else if (a.startsWith('--') && a.slice(2) in args) args[a.slice(2)] = process.argv[++i];
  else throw new Error(`Unknown argument: ${a}`);
}
if (!args.dir) throw new Error('--dir is required');

const sentences = readFileSync(join(args.dir, 'sentences.tsv'), 'utf8')
  .split('\n')
  .filter(Boolean)
  .map((line) => line.split('\t'));
const said = (id) => sentences.filter(([prefix]) => id.startsWith(prefix)).sort((a, b) => b[0].length - a[0].length)[0];

// wer is the word error rate: word edits (substitutions, insertions,
// deletions) over the words said, case and punctuation aside.
const words = (s) => s.toLowerCase().normalize('NFC').replace(/[^\p{L}\p{N}\s']/gu, ' ').split(/\s+/).filter(Boolean);
function wer(ref, hyp) {
  const r = words(ref);
  const h = words(hyp);
  let prev = Array.from({ length: h.length + 1 }, (_, j) => j);
  for (let i = 1; i <= r.length; i++) {
    const row = [i];
    for (let j = 1; j <= h.length; j++) row[j] = Math.min(prev[j] + 1, row[j - 1] + 1, prev[j - 1] + (r[i - 1] === h[j - 1] ? 0 : 1));
    prev = row;
  }
  return prev[h.length] / r.length;
}

const port = 5199;
copyFileSync(join(desktopDir, '../CHANGELOG.md'), join(desktopDir, 'src/renderer/changelog.md'));
const vite = spawn(join(desktopDir, 'node_modules/.bin/vite'), ['--config', 'vite.config.mts', '--port', String(port), '--strictPort'], { cwd: desktopDir, stdio: 'ignore' });
const url = `http://localhost:${port}/dev/voice-bench.html`;
for (let i = 0; ; i++) {
  try {
    if ((await fetch(url)).ok) break;
  } catch {
    if (i > 100) throw new Error('vite never answered');
  }
  await new Promise((r) => setTimeout(r, 200));
}

const { chromium } = await import('playwright');
const flags = args.gpu ? ['--enable-unsafe-webgpu', '--enable-features=Vulkan', '--use-angle=vulkan', '--ignore-gpu-blocklist'] : [];
const browser = await chromium.launch({ executablePath: existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined, args: flags });
const results = [];
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(0);
  page.on('console', (m) => m.type() === 'error' && !m.text().includes('404') && console.error('  page:', m.text()));
  // Vite's first visit optimizes transformers.js and reloads the page once.
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => window.bench);
  const files = readdirSync(args.dir).filter((f) => f.endsWith('.f32')).sort();
  for (const model of args.models.split(',')) {
    const loaded = await page.evaluate((m) => window.bench.load(m), model);
    console.log(`\n${model} on ${loaded.device}${loaded.adapter ? ` (${loaded.adapter})` : ''}${loaded.reason ? ` (${loaded.reason})` : ''}: loaded in ${(loaded.ms / 1000).toFixed(1)} s`);
    for (const file of files) {
      const id = file.replace(/\.f32$/, '');
      const [, language, text] = said(id) ?? [];
      if (!text) continue;
      const audio = readFileSync(join(args.dir, file)).toString('base64');
      const r = await page.evaluate(([a, l, m]) => window.bench.run(a, l, m), [audio, args.language, model]);
      const row = { model, device: loaded.device, id, language, heard: r.language, seconds: +r.seconds.toFixed(1), pieces: r.pieces, ms: r.total, detectMs: r.ms.detect, rtf: +(r.total / 1000 / r.seconds).toFixed(2), wer: +wer(text, r.text).toFixed(3), text: r.text };
      results.push(row);
      console.log(`  ${id.padEnd(18)} ${row.heard === language ? row.heard : `${row.heard}≠${language}`}  ${String(row.seconds).padStart(5)} s audio  ${String(row.ms).padStart(6)} ms (detect ${row.detectMs})  rtf ${row.rtf}  WER ${(row.wer * 100).toFixed(0)}%  “${r.text}”`);
    }
  }
} finally {
  await browser.close();
  vite.kill();
}
if (args.out) writeFileSync(args.out, JSON.stringify(results, null, 2));
