// Renders the phone app's icons (src/renderer/public, listed in its
// manifest.webmanifest) from build/icon.svg and build/icon-maskable.svg with
// Chromium. They're committed: run this again only when an icon changes.
//
//   node scripts/pwa-icons.mjs
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const icons = [
  { svg: 'icon.svg', size: 192, out: 'icon-192.png' },
  { svg: 'icon.svg', size: 512, out: 'icon-512.png' },
  { svg: 'icon-maskable.svg', size: 512, out: 'icon-maskable-512.png' },
  // iOS rounds a home screen icon's corners itself, and fills what's
  // transparent with black: it gets the one with no edge.
  { svg: 'icon-maskable.svg', size: 180, out: 'apple-touch-icon.png' },
];

const browser = await chromium.launch(existsSync('/usr/bin/chromium') ? { executablePath: '/usr/bin/chromium' } : {});
try {
  for (const icon of icons) {
    const page = await browser.newPage({ viewport: { width: icon.size, height: icon.size } });
    const svg = readFileSync(join(root, 'build', icon.svg), 'utf8');
    await page.setContent(`<style>html,body{margin:0;background:transparent}svg{display:block;width:100vw;height:100vh}</style>${svg}`);
    writeFileSync(join(root, 'src/renderer/public', icon.out), await page.screenshot({ omitBackground: true }));
    await page.close();
    console.log('wrote', icon.out);
  }
} finally {
  await browser.close();
}
