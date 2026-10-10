// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';

(globalThis as unknown as { window: unknown }).window = { agentbox: {} };
const { applyMediaEvent, inScope, isMediaUnseen, loadedItems, mediaQuery, scopeKey, setMediaUnseen, withMediaItem } = await import('./mediaPages.ts');
type MediaPages = import('./mediaPages.ts').MediaPages;

const item = (id: string, minute: number, extra: Partial<T.MediaItem> = {}): T.MediaItem => ({
  id,
  agent: 'shop/agent-1',
  agentName: 'agent-1',
  kind: 'screenshot',
  name: id,
  size: 1,
  source: 'agent',
  meta: {},
  createdAt: new Date(Date.UTC(2026, 9, 10, 12, minute)).toISOString(),
  ...extra,
});

// Two pages read, newest first, with a third still to come.
const pages = (next = 'c2'): MediaPages => ({
  pages: [
    { items: [item('m9', 9), item('m8', 8)], next: 'c1' },
    { items: [item('m6', 6), item('m5', 5)], next: next || undefined },
  ],
  pageParams: ['', 'c1'],
});
const ids = (data: MediaPages) => loadedItems(data).map((m) => m.id);

test('a new item goes where it sorts, without reading the pages again', () => {
  assert.deepEqual(ids(withMediaItem(pages(), {}, item('m10', 10))), ['m10', 'm9', 'm8', 'm6', 'm5']);
  assert.deepEqual(ids(withMediaItem(pages(), {}, item('m7', 7))), ['m9', 'm8', 'm7', 'm6', 'm5']);
});

test('an item older than the pages read waits for its own page, unless there is none', () => {
  assert.deepEqual(ids(withMediaItem(pages(), {}, item('m1', 1))), ['m9', 'm8', 'm6', 'm5']);
  assert.deepEqual(ids(withMediaItem(pages(''), {}, item('m1', 1))), ['m9', 'm8', 'm6', 'm5', 'm1']);
});

test('a list leaves out a new item its filters would not show', () => {
  const before = pages();
  assert.equal(withMediaItem(before, { only: 'recording' }, item('m10', 10)), before);
  assert.equal(withMediaItem(before, { project: 'other' }, item('m10', 10)), before);
  assert.equal(withMediaItem(before, { q: 'checkout' }, item('m10', 10)), before);
  assert.deepEqual(ids(withMediaItem(before, { q: 'checkout' }, item('m10', 10, { name: 'Checkout page' }))).slice(0, 1), ['m10']);
});

test('a deleted item goes, and a changed one is replaced in place', () => {
  assert.deepEqual(ids(withMediaItem(pages(), {}, { ...item('m6', 6), removed: true })), ['m9', 'm8', 'm5']);
  const starred = withMediaItem(pages(), {}, item('m8', 8, { favorite: true }));
  assert.deepEqual(ids(starred), ['m9', 'm8', 'm6', 'm5']);
  assert.equal(loadedItems(starred)[1].favorite, true);
});

test('inScope reads a scope the way the daemon filters', () => {
  const shot = item('a', 1, { favorite: true });
  assert.ok(inScope({ project: 'shop', agent: 'agent-1', kinds: ['screenshot', 'recording'], only: 'screenshot', favorite: true }, shot));
  assert.ok(!inScope({ agent: 'agent-2' }, shot));
  assert.ok(!inScope({ kinds: ['recording'] }, shot));
  assert.ok(!inScope({ favorite: true }, item('b', 1)));
});

test('a scope keeps only what narrows, so equal lists share a cache', () => {
  assert.deepEqual(scopeKey({ project: '', agent: 'agent-1', only: '', q: '  ', kinds: [] }), {});
  assert.deepEqual(scopeKey({ project: 'shop', agent: 'agent-1', q: ' a b ' }), { project: 'shop', agent: 'agent-1', q: 'a b' });
  assert.equal(mediaQuery({ project: 'shop', kinds: ['screenshot', 'recording'], unseen: true }, 'c1').toString(), 'project=shop&kind=screenshot%2Crecording&unseen=1&cursor=c1');
});

test('media events and seen notices change every list in the cache, and only the counts are read again', () => {
  const client = new QueryClient();
  client.setQueryData(['mediaPages', {}], pages());
  client.setQueryData(['mediaPages', { only: 'recording' }], pages());
  client.setQueryData(['mediaCounts', {}], { total: 4 });
  applyMediaEvent(client, item('m10', 10));
  assert.deepEqual(ids(client.getQueryData<MediaPages>(['mediaPages', {}])!), ['m10', 'm9', 'm8', 'm6', 'm5']);
  assert.deepEqual(ids(client.getQueryData<MediaPages>(['mediaPages', { only: 'recording' }])!), ['m9', 'm8', 'm6', 'm5']);
  assert.equal(client.getQueryState(['mediaCounts', {}])?.isInvalidated, true);

  setMediaUnseen(client, true, new Set(['m10']));
  assert.ok(isMediaUnseen(client, 'm10'));
  setMediaUnseen(client, false, new Set(), true);
  assert.ok(!isMediaUnseen(client, 'm10'));
});
