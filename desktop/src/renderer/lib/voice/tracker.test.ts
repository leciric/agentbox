// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../../shared/api';
import { ReplyTracker } from './tracker.ts';

const item = (id: string, kind: string, text: string, more: Partial<T.ChatItem> = {}): T.ChatItem => ({ id, turn: 't', kind, text, createdAt: '', updatedAt: '', ...more });

test('what was there when reading started is not read', () => {
  const t = new ReplyTracker('en');
  assert.deepEqual(t.next([item('u1', 'user', 'Hi'), item('a1', 'assistant', 'Hello. How can I help?')]), []);
});

test('a new reply is read as it streams, and only the assistant says anything', () => {
  const t = new ReplyTracker('en');
  const old = [item('u1', 'user', 'Hi'), item('a1', 'assistant', 'Hello.')];
  t.next(old);
  const user = item('u2', 'user', 'Fix it');
  assert.deepEqual(
    t.next([
      ...old,
      user,
      item('th', 'thought', 'Let me think. Hmm.'),
      item('tool', 'tool', 'Ran it. Done.'),
      item('sub', 'assistant', 'Subagent says. Something.', { parent: 'x' }),
      item('hid', 'assistant', 'Hidden. Text.', { hidden: true }),
      item('a2', 'assistant', 'Fixed the bug. Now', { streaming: true }),
    ]),
    ['Fixed the bug.'],
  );
  assert.deepEqual(t.next([...old, user, item('a2', 'assistant', 'Fixed the bug. Now the tests pass.')]), ['Now the tests pass.']);
});

test('older messages loaded in front are not read', () => {
  const t = new ReplyTracker('en');
  const page = [item('u2', 'user', 'Again'), item('a2', 'assistant', 'Sure.')];
  t.next(page);
  assert.deepEqual(t.next([item('u1', 'user', 'Hi'), item('a1', 'assistant', 'Older reply.'), ...page]), []);
});

test('a new chat replacing the thread is read', () => {
  const t = new ReplyTracker('en');
  t.next([item('u1', 'user', 'Hi'), item('a1', 'assistant', 'Hello.')]);
  assert.deepEqual(t.next([item('u9', 'user', 'New'), item('a9', 'assistant', 'Fresh start.')]), ['Fresh start.']);
});
