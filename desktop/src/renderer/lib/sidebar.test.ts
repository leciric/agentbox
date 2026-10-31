// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { buildLists } from './sidebar.ts';

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
