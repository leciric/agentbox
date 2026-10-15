import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { timelineRows } from './timeline.ts';

const item = (id: string, kind: string, more: Partial<T.ChatItem> = {}): T.ChatItem => ({ id, turn: 'u1', kind, createdAt: '', updatedAt: '', ...more });
const tool = (id: string, status: string): T.ChatItem => item(id, 'tool', { tool: { callId: id, title: 'x', kind: 'read', status } });

test('work between messages folds into one row, live while any of it runs', () => {
  const rows = timelineRows({
    agent: 'web/a',
    seq: 1,
    session: { state: 'running', tool: 'claude', options: [], commands: [] },
    items: [
      item('u1', 'user', { text: 'fix it' }),
      tool('t1', 'completed'),
      item('th', 'thought', { text: '…' }),
      item('a1', 'assistant', { text: 'Looking.' }),
      tool('t2', 'in_progress'),
      item('p1', 'permission', { permission: { callId: 'c', title: 'Edit', options: [] } }),
      item('n1', 'note', { agentPermission: { agent: 'b', ref: 'web/b', ai: 'claude', item: 'x', permission: { callId: 'c', title: 'Run', options: [] } } }),
      item('sub', 'tool', { parent: 't2', tool: { callId: 's', title: 'y', kind: 'read', status: 'completed' } }),
      item('h', 'assistant', { text: 'hidden', hidden: true }),
      item('e1', 'error', { text: 'boom' }),
    ],
  });
  assert.deepEqual(
    rows.map((r) => r.kind),
    ['user', 'work', 'assistant', 'work', 'permission', 'agentPermission', 'error'],
  );
  const [, settled, , running] = rows;
  assert.ok(settled.kind === 'work' && settled.items.length === 2 && !settled.live);
  assert.ok(running.kind === 'work' && running.live);
});
