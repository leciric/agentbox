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
  const isAsset = asset.test(path) || files.has(path);
  // Opening an asset itself (/icon.svg) isn't the app: it must not replace the shell's page.
  if (req.mode === 'navigate') return isAsset ? 'pass' : 'page';
  return isAsset ? 'asset' : 'pass';
}

// pageAssets are the files of assets/ a page names: its scripts, styles and
// the modules they preload, which Vite lists in it.
export function pageAssets(html: string): string[] {
  const out = new Set<string>();
  for (const m of html.matchAll(/\b(?:src|href)="\.?\/?(assets\/[A-Za-z0-9._-]+)"/g)) out.add(m[1]);
  return [...out];
}

// The worker's decisions, with the cache and the network handed in so they can
// be tested; sw.ts gives them the real ones.
export interface ShellCache {
  match(key: string): Promise<Response | undefined>;
  put(key: string, res: Response): Promise<void>;
}
export type Fetcher = (input: string | Request, init?: RequestInit) => Promise<Response>;

export const cacheable = (res: Response) => res.ok && res.type === 'basic';
const isHtml = (res: Response) => (res.headers.get('content-type') ?? '').toLowerCase().includes('text/html');

// A cache that can't be written (storage full) never costs the page a response
// the network already gave.
const keep = (cache: ShellCache, key: string | Request, res: Response) =>
  Promise.resolve()
    .then(() => cache.put(typeof key === 'string' ? key : key.url, res))
    .catch(() => undefined);

// precacheShell fetches the page and what it needs to start (pageAssets) and
// fails unless every one comes: a shell missing a script is worse than the
// one before it, which stays until this one is whole. The icons and the
// manifest are best effort.
export async function precacheShell(cache: ShellCache, fetcher: Fetcher, scope: string): Promise<void> {
  const get = async (path: string): Promise<{ url: string; res: Response }> => {
    const url = new URL(path, scope).href;
    const res = await fetcher(url, { cache: 'reload', credentials: 'same-origin' });
    if (!cacheable(res)) throw new Error(`${url}: ${res.status}`);
    return { url, res };
  };
  const home = await get('./');
  const assets = pageAssets(await home.res.clone().text());
  const required = await Promise.all(assets.map(get));
  const optional = await Promise.all(shellFiles.filter((f) => f !== './').map((f) => get(f).catch(() => null)));
  for (const f of [home, ...required, ...optional]) if (f) await cache.put(f.url, f.res);
}

// page is every page from the network, the newest the daemon has, kept as the
// shell's page; from the cache when the network fails or the daemon behind it
// does (a tunnel's 502).
export async function pageResponse(cache: ShellCache, fetcher: Fetcher, req: Request, scope: string): Promise<Response> {
  let res: Response;
  try {
    res = await fetcher(req);
  } catch (err) {
    const kept = await cache.match(scope);
    if (kept) return kept;
    throw err;
  }
  if (cacheable(res) && isHtml(res)) await keep(cache, scope, res.clone());
  else if (res.status >= 500) return (await cache.match(scope)) ?? res;
  return res;
}

// assetResponse is an asset from the cache, fetched once.
export async function assetResponse(cache: ShellCache, fetcher: Fetcher, req: Request): Promise<Response> {
  const kept = await cache.match(req.url);
  if (kept) return kept;
  const res = await fetcher(req);
  if (cacheable(res)) await keep(cache, req, res.clone());
  return res;
}
