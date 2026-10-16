// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { addGroup, arrange, emptyLayout, groupOf, hide, moveGroup, moveToGroup, removeGroup, renameGroup, unhide } from './sidebarGroups.ts';

const p = (name: string) => ({ name }) as T.Project;
const names = (ps: T.Project[]) => ps.map((x) => x.name);
const order = { pinned: [p('pin')], rest: [p('a'), p('b'), p('c')], idle: [p('d'), p('e')] };

test('the edits make a whole new layout, a project in one group at most', () => {
  let l = addGroup(emptyLayout, 'w', ' Work ', ['a', 'd']);
  l = addGroup(l, 'p', 'Personal', ['d']);
  assert.deepEqual(l.groups, [
    { id: 'w', name: 'Work', projects: ['a'] },
    { id: 'p', name: 'Personal', projects: ['d'] },
  ]);
  assert.equal(groupOf(moveToGroup(l, 'a', null), 'a'), undefined);
  l = renameGroup(moveGroup(l, 'p', -1), 'p', 'Home');
  assert.deepEqual(l.groups.map((g) => g.name), ['Home', 'Work']);
  assert.equal(moveGroup(l, 'p', -1), l);
  assert.deepEqual(removeGroup(l, 'p').groups.map((g) => g.id), ['w']);
  l = hide(hide(l, ['a', 'b']), ['a']);
  assert.deepEqual(l.hidden, ['b', 'a']);
  assert.deepEqual(unhide(l, 'b').hidden, ['a']);
  assert.deepEqual(emptyLayout, { groups: [], hidden: [] });
});

const lists = (ls: { group: T.SidebarGroup | null; projects: T.Project[] }[]) => ls.map((x) => [x.group?.id ?? null, names(x.projects)]);

test('arrange lays out groups in order, then the ungrouped, and Idle after them all', () => {
  const l = addGroup(addGroup(emptyLayout, 'w', 'Work', ['c', 'e']), 'x', 'Empty');
  const got = arrange(order, l, new Set());
  assert.deepEqual(names(got.pinned), ['pin']);
  assert.deepEqual(lists(got.active), [['w', ['c']], ['x', []], [null, ['a', 'b']]]);
  assert.deepEqual(lists(got.idle), [['w', ['e']], ['x', []], [null, ['d']]]);
});

test('a hidden project leaves its list, and comes back while it is active', () => {
  const l = hide(addGroup(emptyLayout, 'w', 'Work', ['c']), ['c', 'd', 'pin']);
  const got = arrange(order, l, new Set());
  assert.deepEqual(names(got.hidden), ['c', 'd']);
  assert.deepEqual(names(got.pinned), ['pin']);
  assert.deepEqual(names(got.active[0].projects), []);
  const live = arrange(order, l, new Set(['c']));
  assert.deepEqual(names(live.active[0].projects), ['c']);
  assert.deepEqual(names(live.hidden), ['d']);
});

test('one group in the switcher narrows everything to its projects', () => {
  const l = hide(addGroup(addGroup(emptyLayout, 'w', 'Work', ['b', 'pin', 'd']), 'p', 'Play', ['a']), ['d']);
  const got = arrange(order, l, new Set(), 'w');
  assert.deepEqual(names(got.pinned), ['pin']);
  assert.deepEqual(lists(got.active), [['w', ['b']]]);
  assert.deepEqual(lists(got.idle), [['w', []]]);
  assert.deepEqual(names(got.hidden), ['d']);
  // A group that's gone shows everything.
  assert.equal(arrange(order, l, new Set(), 'gone').active.length, 3);
});
