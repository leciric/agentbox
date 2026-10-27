// The phone app's service worker, built into sw.js (scripts/build.mjs) and
// registered by web/pwa.ts on a page a daemon serves over https. It keeps the
// app's shell (web/shell.ts says what), so the installed app opens without a
// network; everything else goes to the daemon as it would without it.
import { pageAssets, shellCache, shellFiles, staleCaches, strategyOf } from './shell';

// The daemon writes the version of the web app it serves here as it serves
// this file (serveLANWeb in internal/daemon/lan.go): a new version is a new
// worker, which starts a new cache and drops the old ones.
const version = '__AGENTBOX_LAN_WEB_VERSION__';

// The worker's own globals, which the DOM's types don't have.
interface LifecycleEvent extends Event {
  waitUntil(p: Promise<unknown>): void;
}
interface FetchEvent extends LifecycleEvent {
  request: Request;
  respondWith(r: Promise<Response>): void;
}
interface Worker {
  registration: { scope: string };
  clients: { claim(): Promise<void> };
  skipWaiting(): Promise<void>;
  addEventListener(type: 'install' | 'activate', fn: (e: LifecycleEvent) => void): void;
  addEventListener(type: 'fetch', fn: (e: FetchEvent) => void): void;
}
const worker = self as unknown as Worker;
const scope = () => worker.registration.scope;
const cacheable = (res: Response) => res.ok && res.type === 'basic';

async function precache(): Promise<void> {
  const cache = await caches.open(shellCache(version));
  const put = async (path: string, inspect?: (res: Response) => Promise<void>) => {
    const url = new URL(path, scope()).href;
    const res = await fetch(url, { cache: 'reload', credentials: 'same-origin' }).catch(() => null);
    if (!res || !cacheable(res)) return;
    await inspect?.(res.clone());
    await cache.put(url, res);
  };
  // A file that doesn't come is fetched when the page asks for it instead.
  let assets: string[] = [];
  await put('./', async (res) => {
    assets = pageAssets(await res.text());
  });
  await Promise.all([...shellFiles.filter((f) => f !== './'), ...assets].map((f) => put(f)));
}

worker.addEventListener('install', (e) => {
  e.waitUntil(precache().finally(() => worker.skipWaiting()));
});

worker.addEventListener('activate', (e) => {
  e.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(staleCaches(names, version).map((n) => caches.delete(n))))
      .then(() => worker.clients.claim()),
  );
});

// page is every page from the network, the newest the daemon has, kept as the
// shell's page; from the cache only when the network fails.
async function page(req: Request): Promise<Response> {
  const cache = await caches.open(shellCache(version));
  try {
    const res = await fetch(req);
    if (cacheable(res)) await cache.put(scope(), res.clone());
    return res;
  } catch (err) {
    const kept = await cache.match(scope());
    if (kept) return kept;
    throw err;
  }
}

async function asset(req: Request): Promise<Response> {
  const cache = await caches.open(shellCache(version));
  const kept = await cache.match(req);
  if (kept) return kept;
  const res = await fetch(req);
  if (cacheable(res)) await cache.put(req, res.clone());
  return res;
}

worker.addEventListener('fetch', (e) => {
  const how = strategyOf(e.request, scope());
  if (how === 'page') e.respondWith(page(e.request));
  else if (how === 'asset') e.respondWith(asset(e.request));
});
