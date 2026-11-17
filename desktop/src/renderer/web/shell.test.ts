import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assetResponse, pageAssets, pageResponse, precacheShell, shellCache, staleCaches, strategyOf, type Fetcher, type ShellCache } from './shell.ts';

const scope = 'https://chat.example.com/';
const get = (path: string, mode = 'cors') => ({ url: new URL(path, scope).href, method: 'GET', mode });

test('strategyOf never keeps what the daemon answers', () => {
  for (const path of ['/api/v1/projects', '/api/v1/events?ping=1', '/lan/session', '/lan/pair', '/lan', '/v1/me', '/sw.js']) {
    assert.equal(strategyOf(get(path), scope), 'pass', path);
    assert.equal(strategyOf(get(path, 'navigate'), scope), 'pass', `${path}, navigated to`);
  }
  assert.equal(strategyOf({ ...get('/assets/web-abc.js'), method: 'POST' }, scope), 'pass');
  assert.equal(strategyOf(get('https://avatars.githubusercontent.com/u/1'), scope), 'pass');
});

test('strategyOf keeps the shell', () => {
  assert.equal(strategyOf(get('/', 'navigate'), scope), 'page');
  assert.equal(strategyOf(get('/some/where', 'navigate'), scope), 'page');
  for (const path of ['/assets/web-abc.js', '/assets/inter-latin-wght-normal-Dx4kXJAl.woff2', '/manifest.webmanifest', '/icon-192.png', '/icon.svg']) {
    assert.equal(strategyOf(get(path), scope), 'asset', path);
  }
  assert.equal(strategyOf(get('/assets/a/b.js'), scope), 'pass');
  assert.equal(strategyOf(get('/favicon.ico'), scope), 'pass');
});

test('strategyOf leaves an asset opened as a page to the network', () => {
  for (const path of ['/icon.svg', '/assets/web-abc.js', '/manifest.webmanifest']) {
    assert.equal(strategyOf(get(path, 'navigate'), scope), 'pass', path);
  }
});

test('strategyOf stays inside the scope', () => {
  const sub = 'https://chat.example.com/phone/';
  assert.equal(strategyOf(get('/phone/assets/web-abc.js'), sub), 'asset');
  assert.equal(strategyOf(get('/phone/api/v1/projects'), sub), 'pass');
  assert.equal(strategyOf(get('/assets/web-abc.js'), sub), 'pass');
});

test('staleCaches are every other version of the shell', () => {
  const names = [shellCache('0.14.0-aaa'), shellCache('0.15.0-bbb'), 'something-else'];
  assert.deepEqual(staleCaches(names, '0.15.0-bbb'), [shellCache('0.14.0-aaa')]);
  assert.deepEqual(staleCaches(names, '0.16.0-ccc'), [shellCache('0.14.0-aaa'), shellCache('0.15.0-bbb')]);
});

test('pageAssets are the files of assets/ a page names', () => {
  const html = `<script type="module" crossorigin src="./assets/web-B1.js"></script>
    <link rel="modulepreload" crossorigin href="./assets/client-C2.js">
    <link rel="stylesheet" crossorigin href="./assets/web-D3.css">
    <link rel="icon" href="./icon.svg"><script src="./assets/web-B1.js"></script>`;
  assert.deepEqual(pageAssets(html), ['assets/web-B1.js', 'assets/client-C2.js', 'assets/web-D3.css']);
});

// The worker's decisions, against a cache and a network made of maps.
const html = '<script src="./assets/web-B1.js"></script><link href="./assets/web-D3.css">';
const reply = (body: string, init: ResponseInit & { type?: string } = {}) => {
  const res = new Response(body, { headers: { 'content-type': 'text/html' }, ...init });
  if (init.type) Object.defineProperty(res, 'type', { value: init.type });
  else Object.defineProperty(res, 'type', { value: 'basic' });
  return res;
};
const memory = (failPuts = false) => {
  const store = new Map<string, Response>();
  const cache: ShellCache = {
    match: async (k) => store.get(k)?.clone(),
    put: async (k, r) => {
      if (failPuts) throw new Error('QuotaExceededError');
      store.set(k, r);
    },
  };
  return { store, cache };
};
const site = (files: Record<string, () => Response>): Fetcher => async (input) => {
  const url = typeof input === 'string' ? input : input.url;
  const make = files[new URL(url).pathname];
  if (!make) throw new TypeError('offline');
  return make();
};
const all = {
  '/': () => reply(html),
  '/assets/web-B1.js': () => reply('js'),
  '/assets/web-D3.css': () => reply('css'),
};
const request = new Request(scope);

test('precacheShell keeps the page and its scripts, and the icons that come', async () => {
  const { store, cache } = memory();
  await precacheShell(cache, site(all), scope);
  assert.deepEqual([...store.keys()].sort(), [scope, scope + 'assets/web-B1.js', scope + 'assets/web-D3.css']);
});

test('precacheShell fails when the page or a script it needs does not come', async () => {
  for (const missing of ['/', '/assets/web-B1.js', '/assets/web-D3.css']) {
    const { cache } = memory();
    const files = { ...all, [missing]: () => reply('no', { status: 503 }) };
    await assert.rejects(precacheShell(cache, site(files), scope), missing);
  }
  const { cache } = memory();
  const { '/assets/web-D3.css': _, ...lacking } = all;
  await assert.rejects(precacheShell(cache, site(lacking), scope), 'a script that fails to load');
});

test('pageResponse keeps the page it gets and falls back to it offline', async () => {
  const { store, cache } = memory();
  const res = await pageResponse(cache, site(all), request, scope);
  assert.equal(await res.text(), html);
  assert.equal(await store.get(scope)?.clone().text(), html);
  const offline = await pageResponse(cache, site({}), request, scope);
  assert.equal(await offline.text(), html);
  await assert.rejects(pageResponse(memory().cache, site({}), request, scope), TypeError);
});

test('pageResponse prefers the cached shell to a server error', async () => {
  const { store, cache } = memory();
  store.set(scope, reply(html));
  const bad = site({ '/': () => reply('bad gateway', { status: 502 }) });
  assert.equal(await (await pageResponse(cache, bad, request, scope)).text(), html);
  assert.equal(await store.get(scope)?.clone().text(), html, 'the error is not kept');
  const none = await pageResponse(memory().cache, bad, request, scope);
  assert.equal(none.status, 502, 'with no shell the error is all there is');
  const missing = site({ '/': () => reply('nope', { status: 404 }) });
  assert.equal((await pageResponse(cache, missing, request, scope)).status, 404, 'a 404 is the daemon speaking');
});

test('pageResponse does not keep what is not a page', async () => {
  const { store, cache } = memory();
  store.set(scope, reply(html));
  const svg = site({ '/': () => reply('<svg/>', { headers: { 'content-type': 'image/svg+xml' } }) });
  assert.equal(await (await pageResponse(cache, svg, request, scope)).text(), '<svg/>');
  assert.equal(await store.get(scope)?.clone().text(), html);
});

test('a cache that cannot be written still gives the response', async () => {
  const { cache } = memory(true);
  assert.equal(await (await pageResponse(cache, site(all), request, scope)).text(), html);
  const js = new Request(scope + 'assets/web-B1.js');
  assert.equal(await (await assetResponse(cache, site(all), js)).text(), 'js');
});

test('assetResponse fetches once', async () => {
  const { cache } = memory();
  let fetched = 0;
  const counting: Fetcher = async (r) => {
    fetched++;
    return site(all)(r);
  };
  const js = new Request(scope + 'assets/web-B1.js');
  assert.equal(await (await assetResponse(cache, counting, js)).text(), 'js');
  assert.equal(await (await assetResponse(cache, counting, js)).text(), 'js');
  assert.equal(fetched, 1);
});
