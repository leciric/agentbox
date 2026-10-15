import assert from 'node:assert/strict';
import { test } from 'node:test';
import { seenStore } from './seen.ts';

const memory = () => {
  const data = new Map<string, string>();
  return { getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v) };
};

test('a chat is unread once it says something after it was last read here', () => {
  const store = seenStore(memory());
  // Never shown here: taken as read at its latest reply.
  assert.equal(store.unread('p/a', '2026-01-01T00:00:05Z'), false);
  assert.equal(store.unread('p/a', '2026-01-01T00:00:06Z'), true);
  store.read('p/a', '2026-01-01T00:00:06Z');
  assert.equal(store.unread('p/a', '2026-01-01T00:00:06Z'), false);
  assert.equal(store.unread('p/a', undefined), false);
});

test('reading never goes back in time, tells its listeners, and is kept in storage', () => {
  const storage = memory();
  const store = seenStore(storage);
  let told = 0;
  store.subscribe(() => told++);
  store.read('p/a', '2026-01-01T00:00:10Z');
  store.read('p/a', '2026-01-01T00:00:05Z');
  assert.equal(told, 1);
  assert.equal(store.version(), 1);
  const again = seenStore(storage);
  assert.equal(again.unread('p/a', '2026-01-01T00:00:09Z'), false);
  assert.equal(again.unread('p/a', '2026-01-01T00:00:11Z'), true);
});

test('a store with no storage, or a broken one, still works for the page', () => {
  const store = seenStore({ getItem: () => 'not json', setItem: () => { throw new Error('full'); } });
  store.read('p/a', '2026-01-01T00:00:01Z');
  assert.equal(store.unread('p/a', '2026-01-01T00:00:02Z'), true);
  assert.equal(seenStore(undefined).unread('p/a', '2026-01-01T00:00:02Z'), false);
});
