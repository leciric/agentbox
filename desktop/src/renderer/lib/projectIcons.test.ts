import assert from 'node:assert/strict';
import { test } from 'node:test';
import { keepIcon, keepLimit, keptIcon, resetKept } from './projectIcons.ts';

const storage = (init: Record<string, string> = {}) => {
  const store = new Map(Object.entries(init));
  return { store, getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) };
};

test('an icon kept is there for the next run, at its version', () => {
  resetKept();
  const s = storage();
  keepIcon('pawly', 7, { source: 'chosen', emoji: '🐶' }, s);
  keepIcon('logo', 3, { source: 'detected', image: 'data:image/svg+xml;base64,AA==' }, s);
  resetKept();
  assert.deepEqual(keptIcon('pawly', s), { v: 7, icon: { source: 'chosen', emoji: '🐶' } });
  assert.equal(keptIcon('logo', s)?.icon.image, 'data:image/svg+xml;base64,AA==');
  assert.equal(keptIcon('other', s), undefined);
});

test('a big image is kept for this run only', () => {
  resetKept();
  const s = storage();
  const big = 'data:image/png;base64,' + 'A'.repeat(keepLimit);
  keepIcon('photos', 1, { source: 'detected', image: big }, s);
  assert.equal(keptIcon('photos', s)?.icon.image, big);
  resetKept();
  assert.equal(keptIcon('photos', s), undefined);
});

test('a big image is stored as its smaller copy', () => {
  resetKept();
  const s = storage();
  const big = 'data:image/png;base64,' + 'A'.repeat(keepLimit);
  keepIcon('photos', 2, { source: 'detected', image: big }, s, { source: 'detected', image: 'data:image/png;base64,small' });
  assert.equal(keptIcon('photos', s)?.icon.image, big);
  resetKept();
  assert.deepEqual(keptIcon('photos', s), { v: 2, icon: { source: 'detected', image: 'data:image/png;base64,small' } });
});

test('unreadable or blocked storage keeps nothing and throws nothing', () => {
  resetKept();
  assert.equal(keptIcon('pawly', storage({ 'agentbox.projectIcons': '{not json' })), undefined);
  const blocked = { getItem: () => null, setItem: () => { throw new Error('quota'); } };
  keepIcon('pawly', 1, { source: 'none' }, blocked);
  assert.equal(keptIcon('pawly', blocked)?.v, 1);
});
