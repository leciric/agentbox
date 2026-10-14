// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { registerRender, renderPolicy, renderResponse } from './renders.ts';

test('a page has no network unless allowed, and always a sandbox', () => {
  const off = renderPolicy(false);
  assert.match(off, /connect-src 'none'/);
  assert.doesNotMatch(off, /https:/);
  assert.match(off, /script-src 'unsafe-inline'/);
  assert.match(off, /sandbox allow-scripts$/);
  const on = renderPolicy(true);
  assert.match(on, /connect-src https:/);
  assert.match(on, /img-src data: blob: https:/);
  assert.match(on, /sandbox allow-scripts$/);
  assert.doesNotMatch(on, /allow-same-origin/);
});

test('a page is served at an address of its HTML and network, with its policy', async () => {
  const a = registerRender('<p>a</p>', false);
  assert.match(a, /^agentbox-media:\/\/render\/[0-9a-f]{32}$/);
  assert.equal(registerRender('<p>a</p>', false), a);
  assert.notEqual(registerRender('<p>a</p>', true), a);
  const res = renderResponse(a.split('/').pop()!);
  assert.equal(res.status, 200);
  assert.equal(await res.text(), '<p>a</p>');
  assert.equal(res.headers.get('content-security-policy'), renderPolicy(false));
  assert.equal(res.headers.get('x-content-type-options'), 'nosniff');
  assert.equal(renderResponse('nope').status, 404);
});

test('only the newest pages are kept', () => {
  const first = registerRender('<p>first</p>', false).split('/').pop()!;
  for (let i = 0; i < 70; i++) registerRender(`<p>${i}</p>`, false);
  assert.equal(renderResponse(first).status, 404);
});
