import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { afterDelete, byActivity, isUnread, isWorking, neighbour, pickSession, topicFrom } from './sessions.ts';

const session = (id: string, activeAt: string, over: Partial<T.LeadSession> = {}): T.LeadSession => ({
  project: 'web',
  id,
  ref: id === 'main' ? 'web/lead' : `web/lead.${id}`,
  topic: id,
  chat: 'off',
  agents: 0,
  createdAt: '2026-10-01T00:00:00Z',
  activeAt,
  ...over,
});

const main = session('main', '2026-10-28T10:00:00Z');
const prod = session('prod', '2026-10-29T09:00:00Z');
const docs = session('docs', '2026-10-27T09:00:00Z');
const all = [main, prod, docs];

test('the chat shows the session chosen last, else the most recently active', () => {
  assert.equal(pickSession(all, 'docs')?.id, 'docs');
  assert.equal(pickSession(all, undefined)?.id, 'prod');
  assert.equal(pickSession(all, 'gone')?.id, 'prod', 'a deleted choice falls back');
  assert.equal(pickSession([], 'main'), undefined);
  assert.deepEqual(byActivity(all).map((s) => s.id), ['prod', 'main', 'docs']);
});

test('next and previous go round the tabs', () => {
  assert.equal(neighbour(all, 'main', 1)?.id, 'prod');
  assert.equal(neighbour(all, 'docs', 1)?.id, 'main');
  assert.equal(neighbour(all, 'main', -1)?.id, 'docs');
  assert.equal(neighbour(all, 'gone', 1)?.id, 'main');
  assert.equal(neighbour([], 'main', 1), undefined);
});

test('deleting a session shows the one beside it', () => {
  assert.equal(afterDelete(all, 'prod')?.id, 'docs');
  assert.equal(afterDelete(all, 'docs')?.id, 'prod');
  assert.equal(afterDelete([main], 'main'), undefined);
});

test('a session is unread when it said something after it was last seen', () => {
  const said = session('prod', '2026-10-29T09:00:00Z', { last: { kind: 'assistant', text: 'done', at: '2026-10-29T09:00:00Z' } });
  assert.ok(isUnread(said, '2026-10-29T08:00:00Z'));
  assert.ok(!isUnread(said, '2026-10-29T09:30:00Z'));
  assert.ok(isUnread(said, undefined), 'never seen counts from when it was opened');
  assert.ok(!isUnread(prod, undefined), 'nothing said, nothing unread');
  assert.ok(isWorking(session('x', '', { chat: 'running' })));
  assert.ok(!isWorking(session('x', '', { chat: 'ready' })));
});

test('a session started from a message is named after its first line', () => {
  assert.equal(topicFrom('  Fix the checkout\nand then the cart'), 'Fix the checkout');
  assert.equal(topicFrom('Look into why the login page returns 500s for users without a session cookie'), 'Look into why the login page returns 500s for users without…');
  assert.equal(topicFrom('   '), undefined);
});
