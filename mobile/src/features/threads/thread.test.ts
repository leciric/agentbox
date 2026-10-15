import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { agentStatus, leadStatus, liveStatus, rank, workLabel } from './activity.ts';
import { advance, prependPage } from './thread.ts';

const at = (s: number) => new Date(Date.UTC(2026, 9, 15, 12, 0, s)).toISOString();
const item = (id: string, kind: string, more: Partial<T.ChatItem> = {}): T.ChatItem => ({ id, turn: 'u1', kind, createdAt: at(Number(id.replace(/\D/g, '')) || 0), updatedAt: at(0), ...more });
const session = (state = 'ready'): T.ChatSession => ({ state, tool: 'claude', options: [], commands: [] });
const thread = (items: T.ChatItem[], seq = 1, state = 'ready'): T.ChatThread => ({ agent: 'web/lead', seq, session: session(state), items });
const agent = (more: Partial<T.Agent>): T.Agent => ({ ref: 'web/a', project: 'web', name: 'a', state: 'running', ...more }) as T.Agent;

test('events apply in order, and a missing one asks for the thread again', () => {
  const th = thread([item('u1', 'user', { text: 'hi' })]);
  const next = advance(th, [
    { agent: 'web/lead', seq: 2, item: item('a2', 'assistant', { text: 'Hel', streaming: true }) },
    { agent: 'web/lead', seq: 3, append: { id: 'a2', text: 'lo' } },
  ]);
  assert.notEqual(next, 'gap');
  if (next === 'gap') return;
  assert.equal(next.seq, 3);
  assert.equal(next.items.at(-1)?.text, 'Hello');
  // Old events are skipped; a jump is a gap.
  assert.equal(advance(next, [{ agent: 'web/lead', seq: 2, cleared: true }]), next);
  assert.equal(advance(next, [{ agent: 'web/lead', seq: 5, cleared: true }]), 'gap');
});

test('a rollback drops what came after, and clearing drops everything', () => {
  const th = thread([item('u1', 'user'), item('a2', 'assistant'), item('u3', 'user')]);
  const back = advance(th, [{ agent: 'web/lead', seq: 2, after: 'a2' }]);
  assert.deepEqual(back !== 'gap' && back.items.map((i) => i.id), ['u1', 'a2']);
  const cleared = advance(th, [{ agent: 'web/lead', seq: 2, cleared: true }]);
  assert.deepEqual(cleared !== 'gap' && cleared.items, []);
});

test('an older page goes in front, without repeating what is held', () => {
  const th = { ...thread([item('u3', 'user'), item('a4', 'assistant')]), older: true };
  const page = { ...thread([item('u1', 'user'), item('a2', 'assistant'), item('u3', 'user')]), older: false };
  const joined = prependPage(th, page);
  assert.deepEqual(joined.items.map((i) => i.id), ['u1', 'a2', 'u3', 'a4']);
  assert.equal(joined.older, false);
});

test('the status line says what the running turn does', () => {
  const tool = item('t2', 'tool', { tool: { callId: 'c', title: 'npm test', kind: 'execute', status: 'in_progress', command: 'npm test' } });
  const running = liveStatus(thread([item('u1', 'user'), tool], 2, 'running'));
  assert.equal(running?.command, 'npm test');
  assert.equal(running?.tone, 'live');
  assert.equal(running?.since, at(1));

  const perm = item('p3', 'permission', { permission: { callId: 'c', title: 'Edit main.go', options: [] } });
  assert.equal(liveStatus(thread([item('u1', 'user'), perm], 3, 'waiting'))?.tone, 'urgent');

  const writing = liveStatus(thread([item('u1', 'user'), item('a2', 'assistant', { streaming: true })], 2, 'running'));
  assert.equal(writing?.text, 'Writing…');

  const done = thread([item('u1', 'user', { result: { stopReason: 'end_turn' } as T.ChatTurnResult }), item('a2', 'assistant')]);
  assert.equal(liveStatus(done), undefined);
  assert.equal(leadStatus('ready', done).text, 'Idle');
  assert.equal(leadStatus(undefined, undefined).text, 'Not started');
});

test("an agent's line is the rail's word when its chat shows nothing live", () => {
  assert.equal(agentStatus(agent({ chat: 'waiting' }), undefined).tone, 'urgent');
  assert.equal(agentStatus(agent({ chat: 'running', stalledSince: at(0) }), undefined).text, 'Stalled');
  assert.equal(agentStatus(agent({ state: 'stopped' }), undefined).text, 'Stopped');
  assert.ok(rank(agent({ chat: 'waiting' })) < rank(agent({ chat: 'running' })));
  assert.ok(rank(agent({ chat: 'running' })) < rank(agent({ state: 'stopped' })));
});

test('work rows name the file or command', () => {
  assert.equal(workLabel(item('t', 'tool', { tool: { callId: 'c', title: 'Read', kind: 'read', status: 'in_progress', paths: ['/w/src/app.ts'] } })), 'Reading app.ts');
  assert.equal(workLabel(item('t', 'tool', { tool: { callId: 'c', title: 'Run', kind: 'execute', status: 'completed', command: 'go test ./...' } })), 'go test ./...');
});
