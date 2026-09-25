// Builds the app into out/: the main process and the preload script with
// esbuild, the renderer with Vite. Everything is bundled, so a packaged app
// needs no node_modules. out/web is the same renderer for a browser, which
// the hub's --web serves.
import { execFileSync } from 'node:child_process';
import { cpSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build as esbuild } from 'esbuild';
import { build as vite } from 'vite';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
rmSync(join(root, 'out'), { recursive: true, force: true });

// Node's fs.cpSync fails with EACCES on virtiofs — the filesystem an
// AgentBox agent's worktree is mounted through on a Mac — because it tries a
// clone/reflink virtiofs doesn't support before falling back to a plain
// copy. The shell's own `cp -r` doesn't try that, and works there; it isn't
// available on Windows, which never runs this script (D94), so cpSync stays
// as the fallback there.
function copy(src, dst) {
  if (process.platform === 'win32') cpSync(src, dst, { recursive: true });
  else execFileSync('cp', ['-r', src, dst]);
}

// ws can use two optional native modules, which the app doesn't ship.
const node = { bundle: true, platform: 'node', format: 'cjs', target: 'node22', external: ['electron', 'bufferutil', 'utf-8-validate'], sourcemap: true, logLevel: 'warning' };
await esbuild({ ...node, entryPoints: [join(root, 'src/main/index.ts')], outfile: join(root, 'out/main/index.cjs') });
await esbuild({ ...node, entryPoints: [join(root, 'src/preload/index.ts')], outfile: join(root, 'out/preload/index.cjs') });
await vite({ configFile: join(root, 'vite.config.mts'), logLevel: 'warn' });
copy(join(root, 'out/renderer'), join(root, 'out/web'));
rmSync(join(root, 'out/web/index.html'));
renameSync(join(root, 'out/web/web.html'), join(root, 'out/web/index.html'));
// The web app's icon, and a manifest, so a phone can keep it on its home screen.
for (const icon of ['icon.svg', 'icon.png']) copy(join(root, 'build', icon), join(root, 'out/web', icon));
writeFileSync(
  join(root, 'out/web/manifest.webmanifest'),
  JSON.stringify(
    {
      name: 'AgentBox',
      short_name: 'AgentBox',
      start_url: './',
      display: 'standalone',
      background_color: '#07070b',
      theme_color: '#07070b',
      icons: [
        { src: 'icon.svg', sizes: 'any', type: 'image/svg+xml' },
        { src: 'icon.png', type: 'image/png', purpose: 'any' },
      ],
    },
    null,
    2,
  ),
);
console.log('built', join(root, 'out'));
