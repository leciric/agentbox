// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { arrange, emptyLayout, hide, unhide } from './sidebarHidden.ts';

const p = (name: string) => ({ name }) as T.Project;
const names = (ps: T.Project[]) => ps.map((x) => x.name);
const order = { pinned: [p('pin')], rest: [p('a'), p('b')], idle: [p('c'), p('d')] };

test('hide and unhide make a whole new layout, each project once', () => {
  const l = hide(hide(hide(emptyLayout, 'a'), 'b'), 'a');
  assert.deepEqual(l.hidden, ['b', 'a']);
  assert.deepEqual(unhide(l, 'b').hidden, ['a']);
  assert.deepEqual(emptyLayout, { hidden: [] });
});

test('a hidden project leaves its list, Idle included, and comes back while it is active', () => {
  const l = hide(hide(hide(emptyLayout, 'b'), 'c'), 'pin');
  const got = arrange(order, l, new Set());
  assert.deepEqual([names(got.pinned), names(got.rest), names(got.idle), names(got.hidden)], [['pin'], ['a'], ['d'], ['b', 'c']]);
  const live = arrange(order, l, new Set(['b']));
  assert.deepEqual([names(live.rest), names(live.hidden)], [['a', 'b'], ['c']]);
});
