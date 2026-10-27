// What the phone app's service worker (sw.ts) keeps on the phone: the app's
// shell, the page and the files it's made of, so an installed app opens even
// when AgentBox can't be reached. Never what the daemon answers under /api or
// /lan: chats, pairing and the phone's session always go to the daemon.

// shellPrefix starts the name of every cache the worker makes: one for each
// version of the web app the daemon serves (lanWebVersion in
// internal/daemon/lan.go), so a new one starts empty and the old ones go.
export const shellPrefix = 'agentbox-shell-';

export const shellCache = (version: string) => shellPrefix + version;

// staleCaches are the caches of every other version of the shell.
export function staleCaches(names: readonly string[], version: string): string[] {
  return names.filter((n) => n.startsWith(shellPrefix) && n !== shellCache(version));
}

// shellFiles are fetched as the worker installs, besides the page's own
// scripts and styles (pageAssets): the page, and what installs it as an app.
export const shellFiles = ['./', 'manifest.webmanifest', 'icon.svg', 'icon-192.png', 'icon-512.png', 'icon-maskable-512.png', 'apple-touch-icon.png'];

// How the worker answers a request: "page" from the network, and from the
// cache only when that fails; "asset" from the cache, fetched once (its name
// has its content's hash in it, or it belongs to one version); "pass" not at
// all, the browser goes to the network as if there were no worker.
export type Strategy = 'page' | 'asset' | 'pass';

const never = /^(api|lan|v1)(\/|$)/;
const asset = /^assets\/[A-Za-z0-9._-]+$/;
const files = new Set(shellFiles.filter((f) => f !== './'));

export function strategyOf(req: { url: string; method: string; mode: string }, scope: string): Strategy {
  if (req.method !== 'GET') return 'pass';
  const url = new URL(req.url);
  const base = new URL(scope);
  if (url.origin !== base.origin || !url.pathname.startsWith(base.pathname)) return 'pass';
  const path = url.pathname.slice(base.pathname.length);
  if (never.test(path) || path === 'sw.js') return 'pass';
  if (req.mode === 'navigate') return 'page';
  return asset.test(path) || files.has(path) ? 'asset' : 'pass';
}

// pageAssets are the files of assets/ a page names: its scripts, styles and
// the modules they preload, which Vite lists in it.
export function pageAssets(html: string): string[] {
  const out = new Set<string>();
  for (const m of html.matchAll(/\b(?:src|href)="\.?\/?(assets\/[A-Za-z0-9._-]+)"/g)) out.add(m[1]);
  return [...out];
}
