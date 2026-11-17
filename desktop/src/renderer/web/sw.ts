// The phone app's service worker, built into sw.js (scripts/build.mjs) and
// registered by web/pwa.ts on a page a daemon serves over https. It keeps the
// app's shell (web/shell.ts says what), so the installed app opens without a
// network; everything else goes to the daemon as it would without it.
import { assetResponse, pageResponse, precacheShell, shellCache, staleCaches, strategyOf } from './shell';

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
const shell = () => caches.open(shellCache(version));

async function precache(): Promise<void> {
  const cache = await shell();
  try {
    await precacheShell(cache, fetch, scope());
  } catch (err) {
    // Not whole: drop it and fail the install, so the worker and the cache
    // before it stay.
    await caches.delete(shellCache(version));
    throw err;
  }
}

worker.addEventListener('install', (e) => {
  e.waitUntil(precache().then(() => worker.skipWaiting()));
});

worker.addEventListener('activate', (e) => {
  e.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(staleCaches(names, version).map((n) => caches.delete(n))))
      .then(() => worker.clients.claim()),
  );
});

worker.addEventListener('fetch', (e) => {
  const how = strategyOf(e.request, scope());
  if (how === 'page') e.respondWith(shell().then((c) => pageResponse(c, fetch, e.request, scope())));
  else if (how === 'asset') e.respondWith(shell().then((c) => assetResponse(c, fetch, e.request)));
});
