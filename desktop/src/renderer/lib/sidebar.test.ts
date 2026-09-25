// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { buildLists, drop, flatten, moveProject, moveSection, place, targetKey, toLayout, type SidebarList } from './sidebar.ts';

const project = (name: string, section = ''): T.Project => ({ name, section }) as T.Project;
const section = (id: string, name = id): T.Section => ({ id, name }) as T.Section;

test('buildLists groups projects by section, keeping every section even if empty', () => {
  const lists = buildLists([project('a', 's1'), project('b'), project('c', 's1')], [section('s1'), section('s2')]);
  assert.deepEqual(
    lists.map((l) => [l.section?.id ?? null, l.projects.map((p) => p.name)]),
    [
      ['s1', ['a', 'c']],
      ['s2', []],
      [null, ['b']],
    ],
  );
});

test('the list with no section is always last', () => {
  const lists = buildLists([project('loose')], []);
  assert.equal(lists.length, 1);
  assert.equal(lists[0].section, null);
});

test('toLayout is buildLists inverted', () => {
  const lists = buildLists([project('a', 's1'), project('b')], [section('s1')]);
  assert.deepEqual(toLayout(lists), { sections: [{ id: 's1', projects: ['a'] }], loose: ['b'] });
});

test('targetKey identifies every kind of drop target distinctly', () => {
  assert.equal(targetKey({ kind: 'project', name: 'a', edge: 'before' }), 'project:a:before');
  assert.equal(targetKey({ kind: 'project', name: 'a', edge: 'after' }), 'project:a:after');
  assert.equal(targetKey({ kind: 'section', id: 's1' }), 'section:s1');
  assert.equal(targetKey({ kind: 'sectionOrder', id: 's1', edge: 'after' }), 'sectionOrder:s1:after');
  assert.equal(targetKey({ kind: 'list', section: null }), 'list:');
  assert.equal(targetKey({ kind: 'list', section: 's1' }), 'list:s1');
});

test('moveProject reorders within its own list', () => {
  const lists = buildLists([project('a'), project('b'), project('c')], []);
  const moved = moveProject(lists, 'b', -1)!;
  assert.deepEqual(
    moved[0].projects.map((p) => p.name),
    ['b', 'a', 'c'],
  );
});

test('moveProject past the end of a list continues into the next one', () => {
  const lists = buildLists([project('a', 's1'), project('b')], [section('s1')]);
  const moved = moveProject(lists, 'a', 1)!;
  assert.deepEqual(
    moved.map((l) => l.projects.map((p) => p.name)),
    [[], ['a', 'b']],
  );
});

test('moveProject past the top of a list continues into the one before', () => {
  const lists = buildLists([project('a', 's1'), project('b')], [section('s1')]);
  const moved = moveProject(lists, 'b', -1)!;
  assert.deepEqual(
    moved.map((l) => l.projects.map((p) => p.name)),
    [['a', 'b'], []],
  );
});

test('moveProject is null at the very top or bottom of the sidebar', () => {
  const lists = buildLists([project('a')], []);
  assert.equal(moveProject(lists, 'a', -1), null);
  assert.equal(moveProject(lists, 'a', 1), null);
});

test('moveProject is null for a name that is not in any list', () => {
  const lists = buildLists([project('a')], []);
  assert.equal(moveProject(lists, 'ghost', 1), null);
});

test('moveSection reorders the sections, leaving the loose list out of it', () => {
  const lists = buildLists([], [section('s1'), section('s2')]);
  const moved = moveSection(lists, 's2', -1)!;
  assert.deepEqual(
    moved.map((l) => l.section?.id ?? null),
    ['s2', 's1', null],
  );
});

test('moveSection is null at either end', () => {
  const lists = buildLists([], [section('s1'), section('s2')]);
  assert.equal(moveSection(lists, 's1', -1), null);
  assert.equal(moveSection(lists, 's2', 1), null);
});

test('drop moves a project beside another project', () => {
  const lists = buildLists([project('a'), project('b'), project('c')], []);
  const next = drop(lists, { kind: 'project', name: 'c' }, { kind: 'project', name: 'a', edge: 'before' })!;
  assert.deepEqual(
    next[0].projects.map((p) => p.name),
    ['c', 'a', 'b'],
  );
});

test('drop moves a project onto a section, landing at its top', () => {
  const lists = buildLists([project('a', 's1'), project('b')], [section('s1')]);
  const next = drop(lists, { kind: 'project', name: 'b' }, { kind: 'section', id: 's1' })!;
  assert.deepEqual(
    next.map((l) => l.projects.map((p) => p.name)),
    [['b', 'a'], []],
  );
});

test('drop moves a project into an empty list', () => {
  const lists = buildLists([project('a', 's1')], [section('s1'), section('s2')]);
  const next = drop(lists, { kind: 'project', name: 'a' }, { kind: 'list', section: 's2' })!;
  assert.deepEqual(
    next.map((l) => l.projects.map((p) => p.name)),
    [[], ['a'], []],
  );
});

test('drop is null dropping a project back where it already is', () => {
  const lists = buildLists([project('a'), project('b')], []);
  assert.equal(drop(lists, { kind: 'project', name: 'a' }, { kind: 'project', name: 'a', edge: 'before' }), null);
});

test('drop reorders a section among the sections', () => {
  const lists = buildLists([], [section('s1'), section('s2'), section('s3')]);
  const next = drop(lists, { kind: 'section', id: 's3' }, { kind: 'sectionOrder', id: 's1', edge: 'before' })!;
  assert.deepEqual(
    next.map((l) => l.section?.id ?? null),
    ['s3', 's1', 's2', null],
  );
});

test('drop is null for a section dropped on itself, or a mismatched kind', () => {
  const lists = buildLists([], [section('s1'), section('s2')]);
  assert.equal(drop(lists, { kind: 'section', id: 's1' }, { kind: 'sectionOrder', id: 's1', edge: 'after' }), null);
  assert.equal(drop(lists, { kind: 'section', id: 's1' }, { kind: 'list', section: null }), null);
});

test('drop is null for a project dropped on a target only a section has', () => {
  const lists = buildLists([project('a')], []);
  assert.equal(drop(lists, { kind: 'project', name: 'a' }, { kind: 'sectionOrder', id: 's1', edge: 'after' }), null);
});

test('place describes where a project landed, in its section or in none', () => {
  const lists = buildLists([project('a', 's1'), project('b', 's1')], [section('s1', 'Work')]);
  assert.equal(place(lists, 'b'), 'b is now 2 of 2 in Work');
  const loose = buildLists([project('c')], []);
  assert.equal(place(loose, 'c'), 'c is now 1 of 1 in no section');
});

test('place is empty for a project that is not in any list', () => {
  const lists = buildLists([project('a')], []);
  assert.equal(place(lists, 'ghost'), '');
});

test('flatten writes back each project position and section, and renumbers sections', () => {
  const lists: SidebarList[] = [
    { section: section('s1'), projects: [project('a', 'old'), project('b', 'old')] },
    { section: null, projects: [project('c')] },
  ];
  const flat = flatten(lists);
  assert.deepEqual(
    flat.projects.map((p) => [p.name, p.section, p.position]),
    [
      ['a', 's1', 1],
      ['b', 's1', 2],
      ['c', '', 1],
    ],
  );
  assert.deepEqual(
    flat.sections.map((s) => [s.id, s.position]),
    [['s1', 1]],
  );
});
