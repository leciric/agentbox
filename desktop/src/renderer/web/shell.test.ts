import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pageAssets, shellCache, staleCaches, strategyOf } from './shell.ts';

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
