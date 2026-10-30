// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from './api.ts';
import { inPairOrder, pairUp, sideOf } from './pairs.ts';

const item = (id: string, name: string, at: string, kind = 'screenshot', meta: T.MediaMeta = {}): T.MediaItem =>
  ({ id, name, kind, createdAt: `2026-10-28T10:${at}:00Z`, meta }) as T.MediaItem;

test('sideOf reads the pair from the meta, then from a -before/-after or before-/after- name', () => {
  assert.deepEqual(sideOf(item('a', 'whatever', '00', 'screenshot', { pair: 'settings', side: 'after' })), { pair: 'settings', side: 'after' });
  assert.deepEqual(sideOf(item('a', 'counts-project-dark-1920-before', '00')), { pair: 'counts-project-dark-1920', side: 'before' });
  assert.deepEqual(sideOf(item('a', 'After_settings.png', '00')), { pair: 'settings', side: 'after' });
  assert.deepEqual(sideOf(item('a', 'settings after', '00')), { pair: 'settings', side: 'after' });
  assert.equal(sideOf(item('a', 'before', '00')), null);
  assert.equal(sideOf(item('a', 'beforehand', '00')), null);
  assert.equal(sideOf(item('a', 'settings', '00')), null);
  // A side without a pair in the meta falls back on the name.
  assert.deepEqual(sideOf(item('a', 'x-before', '00', 'screenshot', { side: 'after' })), { pair: 'x', side: 'before' });
});

test("pairUp pairs each before with its after, in the order they were taken, and leaves the rest in theirs", () => {
  const media = [
    item('b-after', 'counts-after', '01'),
    item('lone', 'empty state', '02'),
    item('a-before', 'settings-before', '03'),
    item('b-before', 'counts-before', '04'),
    item('a-after', 'after-settings', '05'),
    item('half', 'menu-before', '06'),
  ];
  const { pairs, rest } = pairUp(media);
  assert.deepEqual(
    pairs.map((p) => [p.name, p.before.id, p.after.id]),
    [
      ['counts', 'b-before', 'b-after'],
      ['settings', 'a-before', 'a-after'],
    ],
  );
  assert.deepEqual(
    rest.map((m) => m.id),
    ['lone', 'half'],
  );
});

test('the meta pairs items whatever their names, and an explicit pair meets an inferred one of the same name', () => {
  const { pairs } = pairUp([
    item('x', 'first look', '00', 'screenshot', { pair: 'Settings Dark', side: 'before' }),
    item('y', 'settings-dark-after', '01'),
  ]);
  assert.deepEqual(
    pairs.map((p) => [p.before.id, p.after.id]),
    [['x', 'y']],
  );
});

test("a pair's halves are of one kind, and a retake replaces the take before it", () => {
  const { pairs, rest } = pairUp([
    item('shot-before', 'flow-before', '00'),
    item('rec-after', 'flow-after', '01', 'recording'),
    item('rec-before-1', 'flow-before', '02', 'recording'),
    item('rec-before-2', 'flow-before', '03', 'recording'),
  ]);
  assert.deepEqual(
    pairs.map((p) => [p.key, p.before.id, p.after.id]),
    [['recording:flow', 'rec-before-2', 'rec-after']],
  );
  assert.deepEqual(
    rest.map((m) => m.id),
    ['shot-before', 'rec-before-1'],
  );
});

test('inPairOrder puts each pair together, before then after, ahead of everything else', () => {
  const order = inPairOrder([item('after', 'x-after', '00'), item('other', 'other', '01'), item('before', 'x-before', '02')]);
  assert.deepEqual(
    order.map((m) => m.id),
    ['before', 'after', 'other'],
  );
});
