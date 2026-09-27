// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { clock, describeAll, kindInfo, mediaKinds, searchMedia, searchTerms } from './media.ts';

test('kindInfo finds the kind by name', () => {
  assert.equal(kindInfo('screenshot'), mediaKinds[0]);
  assert.equal(kindInfo('recording').kind, 'recording');
});

test('kindInfo falls back to the last kind for one it does not know', () => {
  assert.equal(kindInfo('bogus'), mediaKinds[mediaKinds.length - 1]);
});

test('describeAll pluralizes and names the kind and the agent', () => {
  assert.equal(describeAll(1, 'screenshot', 'agent-12'), 'all 1 screenshot of agent-12');
  assert.equal(describeAll(42, 'screenshot', 'agent-12'), 'all 42 screenshots of agent-12');
});

test('describeAll drops the agent clause when there is none', () => {
  assert.equal(describeAll(3, 'log', ''), 'all 3 logs');
});

test('describeAll falls back to "item" for no kind filter', () => {
  assert.equal(describeAll(2, '', ''), 'all 2 items');
  assert.equal(describeAll(1, '', ''), 'all 1 item');
});

test('describeAll says what a search narrowed it to', () => {
  assert.equal(describeAll(3, 'note', 'agent-12', ' flaky '), 'all 3 notes of agent-12 matching “flaky”');
  assert.equal(describeAll(3, 'note', 'agent-12', '  '), 'all 3 notes of agent-12');
});

function item(id: string, over: Partial<T.MediaItem>): T.MediaItem {
  return { id, agent: 'p/agent-1', kind: 'file', name: id, size: 0, source: 'agent', meta: {}, createdAt: '', ...over };
}

const gallery = [
  item('shot', { kind: 'screenshot', name: 'Checkout page', file: 'checkout-page.png', mime: 'image/png', meta: { url: 'http://localhost:5173/cart' } }),
  item('video', { kind: 'recording', name: 'Checkout flow', file: 'checkout.webm', mime: 'video/webm' }),
  item('note', { kind: 'note', name: 'note', text: 'The flaky test was a goroutine outliving its turn. Résumé attached.' }),
  item('log', { kind: 'log', name: 'go test', file: 'go-test.log', mime: 'text/plain', text: 'goroutine' }),
  item('zip', { kind: 'file', name: 'bundle', file: 'photo.jpg', mime: 'image/jpeg', agentName: 'agent-12', agentTitle: 'Fix the sidebar' }),
];
const ids = (query: string) => searchMedia(gallery, query).map((m) => m.id);

test('searchMedia keeps everything for an empty search', () => {
  assert.equal(searchMedia(gallery, ''), gallery);
  assert.equal(searchMedia(gallery, '   '), gallery);
});

test('searchMedia finds by name and by file name, whatever the case', () => {
  assert.deepEqual(ids('CHECKOUT'), ['shot', 'video']);
  assert.deepEqual(ids('checkout-page.png'), ['shot']);
  assert.deepEqual(ids('localhost:5173/cart'), ['shot']);
});

test('searchMedia finds by type: the kind, its label, and what it is', () => {
  assert.deepEqual(ids('image'), ['shot', 'zip']); // a screenshot, and a file that is an image
  assert.deepEqual(ids('video'), ['video']);
  assert.deepEqual(ids('notes'), ['note']);
  assert.deepEqual(ids('screenshot'), ['shot']);
});

test('searchMedia finds a note by its text, and only a note', () => {
  assert.deepEqual(ids('goroutine'), ['note']); // the log's text isn't searched
  assert.deepEqual(ids('resume'), ['note']); // accents folded
});

test('searchMedia needs every word, and a quoted phrase whole', () => {
  assert.deepEqual(ids('checkout video'), ['video']);
  assert.deepEqual(ids('"flaky test"'), ['note']);
  assert.deepEqual(ids('"test flaky"'), []);
});

test('searchMedia finds by the agent an item came from', () => {
  assert.deepEqual(ids('sidebar'), ['zip']);
});

test('searchTerms splits words and keeps quoted phrases', () => {
  assert.deepEqual(searchTerms('  Foo  "bar baz" qux '), ['foo', 'bar baz', 'qux']);
  assert.deepEqual(searchTerms('""'), []);
});

test('searchMedia is quick on a big gallery', () => {
  const many = Array.from({ length: 5_000 }, (_, i) => item(`n${i}`, { kind: 'note', name: `note ${i}`, text: 'lorem ipsum dolor '.repeat(20) + i }));
  searchMedia(many, 'warm');
  const start = performance.now();
  for (let i = 0; i < 20; i++) searchMedia(many, `ipsum ${i}`);
  assert.ok(performance.now() - start < 500, 'twenty searches of 5,000 notes take under half a second');
});

test('clock formats seconds as m:ss', () => {
  assert.equal(clock(0), '0:00');
  assert.equal(clock(5), '0:05');
  assert.equal(clock(65), '1:05');
  assert.equal(clock(3599), '59:59');
});

test('clock never goes negative and rounds to the nearest second', () => {
  assert.equal(clock(-5), '0:00');
  assert.equal(clock(4.6), '0:05');
});
