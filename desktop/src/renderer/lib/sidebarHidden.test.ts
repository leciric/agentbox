// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { orderProjects } from './projectOrder.ts';
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

test('the sidebar reads pinned, active, Idle, Hidden, top to bottom, whatever is pinned, hidden or on screen', () => {
  const projects = ['a', 'b', 'c', 'd', 'e', 'f'].map((name) => ({ name, createdAt: '2026-01-01T00:00:00Z' }) as T.Project);
  const read = (pins: string[], active: string[], hidden: string[]) => {
    const got = arrange(orderProjects({ projects, pins, activity: new Map(), active: new Set(active), label: (x) => x.name }), { hidden }, new Set(active));
    return [names(got.pinned), names(got.rest), names(got.idle), names(got.hidden)];
  };
  // The project on screen counts as active, so it joins the rest and never sits in Idle.
  assert.deepEqual(read(['f'], ['a', 'c'], ['e']), [['f'], ['a', 'c'], ['b', 'd'], ['e']]);
  assert.deepEqual(read([], ['d'], ['a', 'd']), [[], ['d'], ['b', 'c', 'e', 'f'], ['a']]);
  assert.deepEqual(read(['b', 'a'], [], []), [['b', 'a'], [], ['c', 'd', 'e', 'f'], []]);
});
