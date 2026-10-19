import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { latestFinishes } from './finished.ts';

const notice = (id: string, ...finished: string[]) => ({ id, kind: 'notice', finished }) as unknown as T.ChatItem;

test('latestFinishes: an agent that finishes twice belongs to its last notice', () => {
  const latest = latestFinishes([notice('n1', 'p/a1'), notice('n2', 'p/a1', 'p/a2'), notice('n3', 'p/a2'), { id: 'x', kind: 'assistant' } as T.ChatItem]);
  assert.deepEqual([...latest], [['p/a1', 'n2'], ['p/a2', 'n3']]);
});
