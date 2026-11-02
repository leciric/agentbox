import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { lastReply, matchesQuery, orderProjects } from './projectList.ts';

const item = (kind: string, at: string, more: Partial<T.ChatItem> = {}): T.ChatItem => ({ id: at, turn: 't', kind, createdAt: at, updatedAt: at, ...more });
const thread = (items: T.ChatItem[]): T.ChatThread => ({ agent: 'web/a', seq: 1, session: { state: 'idle', tool: 'claude', options: [], commands: [] }, items });

test('a query matches the name or the display name, ignoring case and accents', () => {
  const p = { name: 'agentbox', displayName: 'Projéto Web' };
  assert.ok(matchesQuery(p, ''));
  assert.ok(matchesQuery(p, '  '));
  assert.ok(matchesQuery(p, 'BOX'));
  assert.ok(matchesQuery(p, 'projeto'));
  assert.ok(matchesQuery(p, ' web '));
  assert.ok(!matchesQuery(p, 'mobile'));
  assert.ok(matchesQuery({ name: 'api', displayName: '' }, 'ap'));
});

test('a chat last answered with its newest assistant message', () => {
  assert.equal(lastReply(undefined), 0);
  assert.equal(lastReply(thread([item('user', '2026-11-02T10:00:00Z')])), 0);
  const at = Date.parse('2026-11-02T09:00:00Z');
  assert.equal(lastReply(thread([item('assistant', '2026-11-02T09:00:00Z'), item('user', '2026-11-02T10:00:00Z'), item('assistant', '2026-11-02T11:00:00Z', { hidden: true })])), at);
  assert.equal(lastReply(thread([item('assistant', '2026-11-02T08:00:00Z', { updatedAt: '2026-11-02T09:00:00Z' })])), at);
});

test('the default order is the daemon’s', () => {
  const list = [
    { name: 'a', needYou: 0, lastActivity: 1 },
    { name: 'b', needYou: 2, lastActivity: 9 },
  ];
  assert.deepEqual(orderProjects(list, 'default'), list);
});

test('recent activity puts those needing you first, then the latest to answer', () => {
  const list = [
    { name: 'quiet', needYou: 0, lastActivity: 0 },
    { name: 'old', needYou: 0, lastActivity: 100 },
    { name: 'asks', needYou: 1, lastActivity: 50 },
    { name: 'new', needYou: 0, lastActivity: 300 },
    { name: 'asksMore', needYou: 3, lastActivity: 200 },
    { name: 'quiet2', needYou: 0, lastActivity: 0 },
  ];
  assert.deepEqual(
    orderProjects(list, 'recent').map((p) => p.name),
    ['asksMore', 'asks', 'new', 'old', 'quiet', 'quiet2'],
  );
  assert.equal(list[0]?.name, 'quiet', 'the input is left as it was');
});
