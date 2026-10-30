import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { latestFinishes, mergedFinishes, visibleFinishes } from './finished.ts';

const notice = (id: string, ...finished: string[]) => ({ id, kind: 'notice', finished }) as unknown as T.ChatItem;

test('latestFinishes: an agent that finishes twice belongs to its last notice', () => {
  const latest = latestFinishes([notice('n1', 'p/a1'), notice('n2', 'p/a1', 'p/a2'), notice('n3', 'p/a2'), { id: 'x', kind: 'assistant' } as T.ChatItem]);
  assert.deepEqual([...latest], [['p/a1', 'n2'], ['p/a2', 'n3']]);
});

test('mergedFinishes: only an agent whose pull request is merged', () => {
  const prs = new Map<string, { state: string } | null>([['p/a1', { state: 'merged' }], ['p/a2', { state: 'open' }], ['p/a3', { state: 'closed' }], ['p/a4', null]]);
  assert.deepEqual([...mergedFinishes(['p/a1', 'p/a2', 'p/a3', 'p/a4', 'p/a5'], prs)], ['p/a1']);
});

test('visibleFinishes: merged cards go and are counted; showing merged brings them back', () => {
  const merged = new Set(['p/a1', 'p/a3']);
  assert.deepEqual(visibleFinishes(['p/a1', 'p/a2', 'p/a3'], merged, false), { shown: ['p/a2'], hidden: 2 });
  assert.deepEqual(visibleFinishes(['p/a1', 'p/a2', 'p/a3'], merged, true), { shown: ['p/a1', 'p/a2', 'p/a3'], hidden: 0 });
  assert.deepEqual(visibleFinishes(['p/a2'], new Set(), false), { shown: ['p/a2'], hidden: 0 });
});
