import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { emptyTally } from './agentCounts.ts';
import { askOf, attentionOf, byAttention, keepOnScreen, markProjectSeen, resetProjectSeenForTest, unseenFinished } from './attention.ts';
import type { AgentSt, Row } from './leadFirst.ts';

const row = (project: string, name: string, st: AgentSt, extra: Partial<T.Agent> = {}): Row => ({ agent: { ref: `${project}/${name}`, project, name, title: '', state: 'running', ...extra } as T.Agent, st });

test('needs you outranks finished, and counts the waiting lead and the errors', () => {
  assert.deepEqual(attentionOf({ ...emptyTally(), needs: 1, error: 1 }, true, 3), { level: 'needs', count: 3 });
  assert.deepEqual(attentionOf({ ...emptyTally(), review: 2, working: 1 }, false, 2), { level: 'finished', count: 2 });
  assert.deepEqual(attentionOf({ ...emptyTally(), working: 4 }, false, 0), { count: 0 });
});

test('byAttention puts needs first, then finished, keeping the order within each', () => {
  const ps = ['a', 'b', 'c', 'd', 'e'].map((name) => ({ name }));
  assert.deepEqual(byAttention(ps, new Set(['d', 'b']), new Set(['e', 'b'])).map((p) => p.name), ['b', 'd', 'e', 'a', 'c']);
});

test('keepOnScreen holds the project on screen only if it already had a place', () => {
  const none = new Set<string>();
  assert.deepEqual([...keepOnScreen(new Set(['a']), none, 'a')], ['a']);
  assert.deepEqual([...keepOnScreen(new Set(['a']), none, 'b')], []);
  assert.deepEqual([...keepOnScreen(none, none, 'a')], []);
});

test('unseenFinished is the project agents to review that finished after it was seen', () => {
  const rows = [row('p', 'a1', 'review'), row('p', 'a2', 'review'), row('p', 'a3', 'working'), row('p', 'a5', 'review'), row('q', 'a4', 'review')];
  const at = new Map([['p/a1', '2026-10-30T10:00:00Z'], ['p/a2', '2026-10-30T12:00:00Z'], ['p/a3', '2026-10-30T12:00:00Z'], ['q/a4', '2026-10-30T12:00:00Z']]);
  assert.deepEqual(unseenFinished(rows, 'p', '2026-10-30T11:00:00Z', at), ['p/a2']);
  assert.deepEqual(unseenFinished(rows, 'p', '', at), ['p/a1', 'p/a2']);
});

test('markProjectSeen keeps the time, and a first run sets since', () => {
  const store = new Map<string, string>();
  resetProjectSeenForTest({ getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) });
  assert.ok(store.get('agentbox.v2.projectSeenSince'));
  markProjectSeen('p', new Date('2026-10-30T11:00:00Z'));
  assert.deepEqual(JSON.parse(store.get('agentbox.v2.projectSeen') ?? '{}'), { p: '2026-10-30T11:00:00.000Z' });
  const since = store.get('agentbox.v2.projectSeenSince');
  resetProjectSeenForTest({ getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) });
  assert.equal(store.get('agentbox.v2.projectSeenSince'), since);
});

test('askOf says the question in its own words, else the agent status', () => {
  const rows = [row('p', 'agent-1', 'error', { title: 'Broken', state: 'missing' }), row('p', 'agent-2', 'needs', { title: 'Login', chat: 'waiting' })];
  const q = { ref: 'p/agent-2', status: 'pending', kind: 'secret', question: 'Which\n  API key?' } as T.Question;
  assert.deepEqual(askOf(rows, 'p', [q]), { who: 'Login', text: 'Which API key?' });
  const ask = askOf(rows, 'p', []);
  assert.equal(ask?.who, 'Login');
  assert.ok(ask?.text);
  assert.equal(askOf(rows, 'q', []), undefined);
});
