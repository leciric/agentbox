// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { activityStatus, agentChatStatus, lastReply, leadChatStatus, liveActivity } from './chatActivity.ts';

let seq = 0;
const at = (s: number) => new Date(Date.UTC(2026, 0, 1, 0, 0, s)).toISOString();
const item = (over: Partial<T.ChatItem>): T.ChatItem =>
  ({ id: `i${seq++}`, turn: 'u1', kind: 'assistant', createdAt: at(0), updatedAt: at(0), ...over }) as T.ChatItem;
const tool = (over: Partial<T.ChatTool>): T.ChatTool => ({ callId: `c${seq++}`, title: 'a tool', kind: 'other', status: 'in_progress', ...over }) as T.ChatTool;
const thread = (items: T.ChatItem[], session: Partial<T.ChatSession> = { state: 'running' }): T.ChatThread =>
  ({ agent: 'p/agent-01', seq: items.length, session: { state: 'ready', options: [], commands: [], tool: 'claude', ...session }, items }) as T.ChatThread;
const user = (over: Partial<T.ChatItem> = {}) => item({ id: 'u1', kind: 'user', turn: '', createdAt: at(1), ...over });
const agent = (over: Partial<T.Agent>): T.Agent => ({ ref: 'p/agent-01', project: 'p', name: 'agent-01', state: 'running', chat: 'ready', ...over }) as T.Agent;

test('liveActivity is thinking at the start of a turn, with when it started', () => {
  assert.deepEqual(liveActivity(thread([user()])), { kind: 'thinking', since: at(1) });
});

test('liveActivity names the tool call that started last, and the command it runs', () => {
  const read = item({ kind: 'tool', tool: tool({ kind: 'read', paths: ['/w/src/main.go'] }) });
  assert.deepEqual(liveActivity(thread([user(), read])), { kind: 'tool', label: 'Reading main.go', command: undefined, since: at(1) });
  const run = item({ kind: 'tool', tool: tool({ kind: 'execute', command: 'go test ./...', title: 'Run tests' }) });
  const done = item({ kind: 'tool', tool: tool({ kind: 'edit', status: 'completed' }) });
  assert.deepEqual(liveActivity(thread([user(), read, run, done])), { kind: 'tool', label: 'Running go test ./...', command: 'go test ./...', since: at(1) });
});

test('liveActivity is writing while the answer streams, and thinking between tool calls', () => {
  const done = item({ kind: 'tool', tool: tool({ status: 'completed' }) });
  assert.equal(liveActivity(thread([user(), done]))?.kind, 'thinking');
  assert.equal(liveActivity(thread([user(), done, item({ kind: 'assistant', streaming: true, text: 'So' })]))?.kind, 'writing');
});

test('liveActivity leaves out a subagent\'s own calls, and names the subagent', () => {
  const sub = item({ kind: 'subagent', subagent: { name: 'Explore', task: 'Find the router', state: 'running' } });
  const nested = item({ kind: 'tool', parent: sub.id, tool: tool({ kind: 'search', title: 'routes' }) });
  assert.deepEqual(liveActivity(thread([user(), sub, nested])), { kind: 'subagent', label: 'Find the router', since: at(1) });
});

test('a permission to give comes before whatever else the turn does', () => {
  const run = item({ kind: 'tool', tool: tool({ kind: 'execute', command: 'rm -rf build' }) });
  const ask = item({ kind: 'permission', permission: { callId: 'c', title: 'rm -rf build', options: [] } });
  const got = liveActivity(thread([user(), run, ask], { state: 'waiting' }));
  assert.deepEqual(got, { kind: 'permission', title: 'rm -rf build' });
  assert.deepEqual(activityStatus(got!), { text: 'Needs your permission: rm -rf build', tone: 'urgent', live: true });
});

test('liveActivity is nothing once the turn has ended, unless work was left in the background', () => {
  const ended = user({ result: { state: 'completed', endedAt: at(9) } });
  assert.equal(liveActivity(thread([ended, item({ text: 'Done.' })], { state: 'ready' })), undefined);
  assert.deepEqual(liveActivity(thread([ended], { state: 'ready', background: ['gh run watch'] })), { kind: 'background', tasks: ['gh run watch'] });
  assert.deepEqual(liveActivity(thread([], { state: 'starting', detail: 'Starting Claude Code' })), { kind: 'starting', detail: 'Starting Claude Code' });
});

test('a running session whose turn isn\'t held yet is thinking since the session says', () => {
  assert.deepEqual(liveActivity(thread([], { state: 'running', turnStartedAt: at(4) })), { kind: 'thinking', since: at(4) });
});

test('activityStatus sets the command apart, with the elapsed time from the turn', () => {
  assert.deepEqual(activityStatus({ kind: 'tool', label: 'Running go test', command: 'go test', since: at(1) }), {
    text: 'Running',
    tone: 'live',
    live: true,
    since: at(1),
    command: 'go test',
  });
  assert.equal(activityStatus({ kind: 'tool', label: 'Editing main.go', since: at(1) }).text, 'Editing main.go');
});

test('agentChatStatus shows the live activity, or the rail\'s finish state once the turn is over', () => {
  assert.equal(agentChatStatus(agent({ chat: 'running' }), thread([user()])).text, 'Thinking…');
  const ended = thread([user({ result: { state: 'completed', endedAt: at(9) } })], { state: 'ready' });
  assert.deepEqual(agentChatStatus(agent({ outcome: 'asking' }), ended), { text: 'Waiting for an answer', tone: 'urgent', live: true });
  assert.equal(agentChatStatus(agent({ outcome: 'done' }), ended).live, false);
  assert.equal(agentChatStatus(agent({ outcome: 'failed' }), ended).tone, 'error');
  // A stalled turn says so, whatever the thread shows it doing.
  assert.equal(agentChatStatus(agent({ chat: 'running', stalledSince: at(5) }), thread([user()])).tone, 'error');
  // A stopped agent has nothing live, even with a turn left open.
  assert.equal(agentChatStatus(agent({ state: 'stopped' }), thread([user()])).live, false);
});

test('leadChatStatus is live while the project\'s chat works, and says so when it was never started', () => {
  assert.equal(leadChatStatus('running', thread([user()])).text, 'Thinking…');
  assert.deepEqual(leadChatStatus('off', undefined), { text: 'Not started', tone: 'muted', live: false });
  assert.equal(leadChatStatus('ready', thread([user({ result: { state: 'completed', endedAt: at(9) } })], { state: 'ready' })).text, 'Idle');
});

test('lastReply is the latest answer, error or turn end, never your own message or a subagent\'s', () => {
  const u = user({ createdAt: at(1), result: { state: 'completed', endedAt: at(8) } });
  assert.equal(lastReply(thread([u, item({ updatedAt: at(5) })])), at(8));
  assert.equal(lastReply(thread([u, item({ updatedAt: at(9) }), item({ updatedAt: at(20), parent: 'x' }), item({ updatedAt: at(30), hidden: true })])), at(9));
  assert.equal(lastReply(thread([user()])), undefined);
  assert.equal(lastReply(undefined), undefined);
});
