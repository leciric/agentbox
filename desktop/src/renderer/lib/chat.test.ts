// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import {
  changedFiles,
  chatKey,
  contextHint,
  currentPlan,
  entryLabel,
  formatDuration,
  formatTokens,
  isActive,
  isCredentialRequest,
  isSilent,
  isWork,
  lineCounts,
  liveLabel,
  pendingPermissions,
  timelineRows,
  toolOf,
  workSummary,
} from './chat.ts';

let seq = 0;
const item = (over: Partial<T.ChatItem>): T.ChatItem =>
  ({ id: `i${seq++}`, turn: 't1', kind: 'assistant', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z', ...over }) as T.ChatItem;

const tool = (over: Partial<T.ChatTool>): T.ChatTool => ({ callId: 'c1', title: 'a tool', kind: 'other', status: 'completed', ...over }) as T.ChatTool;

const thread = (items: T.ChatItem[]): T.ChatThread => ({ agent: 'p/agent-01', seq: items.length, session: {} as T.ChatSession, items }) as T.ChatThread;

test('chatKey is a stable query key per agent ref', () => {
  assert.deepEqual(chatKey('p/agent-01'), ['chat', 'p/agent-01']);
});

test('formatDuration moves from seconds to minutes to hours', () => {
  assert.equal(formatDuration(5), '5s');
  assert.equal(formatDuration(65), '1m 5s');
  assert.equal(formatDuration(3661), '1h 1m');
});

test('formatDuration never goes negative', () => {
  assert.equal(formatDuration(-5), '0s');
});

test('isWork is true for tools, thoughts and answered permissions only', () => {
  assert.equal(isWork(item({ kind: 'tool' })), true);
  assert.equal(isWork(item({ kind: 'thought' })), true);
  assert.equal(isWork(item({ kind: 'permission', permission: { outcome: 'allow' } as T.ChatPermission })), true);
  assert.equal(isWork(item({ kind: 'permission' })), false);
  assert.equal(isWork(item({ kind: 'assistant' })), false);
});

test('isCredentialRequest matches a request_credential tool call by name or title', () => {
  assert.equal(isCredentialRequest(item({ kind: 'tool', tool: tool({ name: 'mcp__memory__request_credential' }) })), true);
  assert.equal(isCredentialRequest(item({ kind: 'tool', tool: tool({ title: 'memory.request_credential' }) })), true);
  assert.equal(isCredentialRequest(item({ kind: 'tool', tool: tool({ name: 'read_file' }) })), false);
});

test('isSilent reflects the item\'s hidden flag', () => {
  assert.equal(isSilent(item({ hidden: true })), true);
  assert.equal(isSilent(item({})), false);
});

test('isActive is true for a streaming thought, a running tool, or a running subagent', () => {
  assert.equal(isActive(item({ kind: 'thought', streaming: true })), true);
  assert.equal(isActive(item({ kind: 'tool', tool: tool({ status: 'in_progress' }) })), true);
  assert.equal(isActive(item({ kind: 'tool', tool: tool({ status: 'completed' }) })), false);
  assert.equal(isActive(item({ kind: 'subagent', subagent: { state: 'running' } as T.ChatSubagent })), true);
  assert.equal(isActive(undefined), false);
});

test('pendingPermissions keeps only permission items without an outcome', () => {
  const answered = item({ kind: 'permission', permission: { outcome: 'allow' } as T.ChatPermission });
  const pending = item({ kind: 'permission', permission: { options: [] } as unknown as T.ChatPermission });
  const other = item({ kind: 'assistant' });
  assert.deepEqual(pendingPermissions(thread([answered, pending, other])), [pending]);
});

test('currentPlan is the last plan entry, only when it belongs to the last user turn', () => {
  const user1 = item({ kind: 'user' });
  const plan1 = item({ kind: 'plan', turn: user1.id, plan: [{ content: 'old', status: 'done' }] });
  const user2 = item({ kind: 'user' });
  assert.deepEqual(currentPlan(thread([user1, plan1, user2])), undefined);
  const plan2 = item({ kind: 'plan', turn: user2.id, plan: [{ content: 'new', status: 'pending' }] });
  assert.deepEqual(currentPlan(thread([user1, plan1, user2, plan2])), plan2.plan);
});

test('currentPlan is undefined with no plan item at all', () => {
  assert.equal(currentPlan(thread([item({ kind: 'user' })])), undefined);
});

test('toolOf finds the tool with a matching call id, the latest one', () => {
  const older = item({ kind: 'tool', tool: tool({ callId: 'x', status: 'in_progress' }) });
  const newer = item({ kind: 'tool', tool: tool({ callId: 'x', status: 'completed' }) });
  assert.equal(toolOf(thread([older, newer]), 'x'), newer.tool);
  assert.equal(toolOf(thread([older, newer]), 'missing'), undefined);
});

test('workSummary counts each kind of tool call, and files edited by path', () => {
  const items = [
    item({ kind: 'tool', tool: tool({ kind: 'execute' }) }),
    item({ kind: 'tool', tool: tool({ kind: 'read' }) }),
    item({ kind: 'tool', tool: tool({ kind: 'read' }) }),
    item({ kind: 'tool', tool: tool({ kind: 'edit', paths: ['a.ts'] }) }),
    item({ kind: 'tool', tool: tool({ kind: 'edit', paths: ['a.ts'] }) }),
  ];
  assert.equal(workSummary(items), 'Ran 1 command, read 2 files and edited 1 file');
});

test('workSummary falls back to thoughts and answers when there was no tool work', () => {
  assert.equal(workSummary([item({ kind: 'thought' }), item({ kind: 'thought' }), item({ kind: 'permission' })]), 'Thought 2 times and answered 1 request');
});

test('workSummary is empty for no items at all', () => {
  assert.equal(workSummary([]), '');
});

test('entryLabel describes a thought, an answered permission, and a tool call', () => {
  assert.equal(entryLabel(item({ kind: 'thought' })), 'Thought');
  const perm = item({
    kind: 'permission',
    permission: { outcome: 'allow', options: [{ id: 'allow', name: 'Allow', kind: 'allow' }], title: 'run rm -rf' } as T.ChatPermission,
  });
  assert.equal(entryLabel(perm), 'Allowed: run rm -rf');
  const rejected = item({
    kind: 'permission',
    permission: { outcome: 'reject', options: [{ id: 'reject', name: 'Deny', kind: 'reject_once' }], title: 'run rm -rf' } as T.ChatPermission,
  });
  assert.equal(entryLabel(rejected), 'Denied: run rm -rf');
  assert.equal(entryLabel(item({ kind: 'tool', tool: tool({ kind: 'execute', command: 'ls -la' }) })), 'ls -la');
  assert.equal(entryLabel(item({ kind: 'tool', tool: tool({ title: 'Read file.ts' }) })), 'Read file.ts');
});

test('liveLabel names what a running tool is doing, by kind', () => {
  assert.equal(liveLabel(item({ kind: 'thought' })), 'Thinking');
  assert.equal(
    liveLabel(item({ kind: 'tool', tool: tool({ kind: 'edit', status: 'in_progress', paths: ['src/a.ts'] }) })),
    'Editing a.ts',
  );
  assert.equal(liveLabel(item({ kind: 'tool', tool: tool({ kind: 'read', status: 'in_progress', paths: ['src/a.ts'] }) })), 'Reading a.ts');
  assert.equal(liveLabel(item({ kind: 'tool', tool: tool({ kind: 'execute', status: 'pending', command: 'ls' }) })), 'Running ls');
});

test('liveLabel falls back to entryLabel once the tool is no longer active', () => {
  const it = item({ kind: 'tool', tool: tool({ kind: 'execute', status: 'completed', command: 'ls' }) });
  assert.equal(liveLabel(it), entryLabel(it));
});

test('lineCounts sums added and removed lines from a diff', () => {
  const diff = { path: 'a.ts', oldText: 'one\ntwo\nthree\n', newText: 'one\ntwo changed\nthree\nfour\n' } as T.ChatDiff;
  const counts = lineCounts(diff);
  assert.equal(counts.added, 2);
  assert.equal(counts.removed, 1);
});

test('changedFiles sums diffs by path, for completed tool calls only', () => {
  const diff = { path: 'a.ts', oldText: 'one\n', newText: 'one\ntwo\n' } as T.ChatDiff;
  const done = item({ kind: 'tool', tool: tool({ status: 'completed', diffs: [diff] }) });
  const running = item({ kind: 'tool', tool: tool({ status: 'in_progress', diffs: [diff] }) });
  const files = changedFiles([done, running]);
  assert.equal(files.length, 1);
  assert.equal(files[0].path, 'a.ts');
  assert.equal(files[0].added, 1);
});

test('formatTokens abbreviates thousands and millions', () => {
  assert.equal(formatTokens(500), '500');
  assert.equal(formatTokens(1500), '2k');
  assert.equal(formatTokens(2_000_000), '2M');
  assert.equal(formatTokens(2_500_000), '2.5M');
});

test('contextHint reads a context-window size from a description or a value suffix', () => {
  assert.equal(contextHint({ value: 'opus', name: 'Opus', description: 'the 1M context model' } as T.ChatOptionChoice), '1M');
  assert.equal(contextHint({ value: 'opus[200k]', name: 'Opus' } as T.ChatOptionChoice), '200K');
  assert.equal(contextHint({ value: 'opus', name: 'Opus' } as T.ChatOptionChoice), undefined);
});

test('timelineRows turns a settled turn with tool calls into a fold and a work row', () => {
  const user = item({ kind: 'user', result: { state: 'completed', endedAt: '2026-01-01T00:01:00Z' } as T.ChatTurnResult });
  const call = item({ kind: 'tool', turn: user.id, tool: tool({ status: 'completed' }) });
  const assistant = item({ kind: 'assistant', turn: user.id });
  const rows = timelineRows(thread([user, call, assistant]), new Set());
  const kinds = rows.map((r) => r.type);
  assert.deepEqual(kinds, ['user', 'fold', 'assistant']);
});

test('timelineRows opens the fold when the turn is in openTurns', () => {
  const user = item({ kind: 'user', result: { state: 'completed', endedAt: '2026-01-01T00:01:00Z' } as T.ChatTurnResult });
  const call = item({ kind: 'tool', turn: user.id, tool: tool({ status: 'completed' }) });
  const assistant = item({ kind: 'assistant', turn: user.id });
  const rows = timelineRows(thread([user, call, assistant]), new Set([user.id]));
  assert.deepEqual(
    rows.map((r) => r.type),
    ['user', 'fold', 'work', 'assistant'],
  );
});

test('timelineRows shows a running turn as working, then thinking once nothing is active', () => {
  const user = item({ kind: 'user' });
  const rows = timelineRows(thread([user]), new Set());
  assert.deepEqual(
    rows.map((r) => r.type),
    ['user', 'working', 'thinking'],
  );
});
