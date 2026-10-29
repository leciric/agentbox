import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { counted, emptyTally, groupRows, initials, stRows, tally, tallyByProject, tileLabels } from './agentCounts.ts';

const agent = (name: string, over: Partial<T.Agent> = {}) =>
  ({ name, ref: `${over.project ?? 'p'}/${name}`, project: 'p', title: '', branch: `agentbox/${name}`, state: 'running', chat: 'ready', createdAt: '2026-10-15T10:00:00Z', ...over }) as T.Agent;

// The design review's mix: an agent waiting on a permission, one asking a
// question, one ready to review that also asks (it counts once, as asking),
// two ready to review, one failed, two working, one done, and in another
// project one working.
const agents = [
  agent('a1', { chat: 'waiting' }),
  agent('a2'),
  agent('a3', { outcome: 'done' }),
  agent('a4', { outcome: 'done' }),
  agent('a5', { outcome: 'done' }),
  agent('a6', { chat: 'error' }),
  agent('a7', { chat: 'running' }),
  agent('a8', { chat: 'running' }),
  agent('a9', { outcome: 'done', state: 'stopped', chat: '' }),
  agent('b1', { project: 'q', chat: 'running' }),
];
const ctx = { review: new Set(['p/a3', 'p/a4', 'p/a5']), asking: new Set(['p/a2', 'p/a3']) };
const rows = stRows(agents, ctx, new Map([['p/a4', 412]]));

test('every agent is in one state, so the counts add up to the agents', () => {
  const c = tally(rows);
  assert.deepEqual({ ...c }, { needs: 3, error: 1, review: 2, working: 3, done: 1, approvals: 1, leads: 0, agents: 10 });
  assert.equal(c.needs + c.error + c.review + c.working + c.done, c.agents);
  assert.equal(rows.find((r) => r.agent.name === 'a4')?.pr, 412);
});

test('a busy lead is counted apart from the agents at work', () => {
  const leads = new Map([['p', 'running'], ['q', 'ready'], ['r', 'running']]);
  assert.equal(tally(rows, leads).working, 3);
  assert.equal(tally(rows, leads).leads, 2);
  assert.equal(tally(rows, leads, 'p').working, 2);
  assert.equal(tally(rows, leads, 'p').leads, 1);
  assert.equal(tally(rows, leads, 'q').leads, 0);
  // A project with nothing but its lead at work is counted too.
  const by = tallyByProject(rows, leads);
  assert.deepEqual([...by.keys()].sort(), ['p', 'q', 'r']);
  assert.equal(by.get('r')?.leads, 1);
  assert.equal(by.get('r')?.working, 0);
  // The projects' counts add up to the whole.
  const sum = [...by.values()].reduce((n, c) => n + c.working, 0);
  assert.equal(sum, tally(rows, leads).working);
});

test('the approvals are some of the asks, never counted beside them', () => {
  const c = tally(rows, new Map(), 'p');
  assert.ok(c.approvals <= c.needs);
  assert.equal(c.approvals, 1);
});

test('counted is whether a row has anything to show', () => {
  assert.equal(counted(emptyTally()), false);
  assert.equal(counted({ ...emptyTally(), done: 4 }), false);
  assert.equal(counted({ ...emptyTally(), review: 1 }), true);
});

test('groupRows puts what needs you first and the done ones under idle', () => {
  const groups = groupRows(rows.filter((r) => r.agent.project === 'p'));
  assert.deepEqual(
    groups.map((g) => [g.group, g.rows.map((r) => r.agent.name)]),
    [
      ['needs', ['a1', 'a2', 'a3']],
      ['error', ['a6']],
      ['review', ['a4', 'a5']],
      ['working', ['a7', 'a8']],
      ['idle', ['a9']],
    ],
  );
  assert.deepEqual(groupRows([]), []);
});

test('initials tell agents apart by their title, else their number', () => {
  assert.equal(initials({ title: 'PR agent', name: 'agent-12' }), 'PA');
  assert.equal(initials({ title: 'Dark mode for the app', name: 'agent-3' }), 'DM');
  assert.equal(initials({ title: 'invoices', name: 'agent-3' }), 'IN');
  assert.equal(initials({ title: '', name: 'agent-12' }), '12');
  assert.equal(initials({ title: '', name: 'agent-1234' }), '234');
  assert.equal(initials({ title: '', name: 'scout' }), 'SC');
  assert.equal(initials({ title: 'Ação rápida', name: 'agent-1' }), 'AR');
});

test('tileLabels gives agents whose initials match their numbers instead', () => {
  const labels = tileLabels([
    { ref: 'p/agent-94', name: 'agent-94', title: 'Needs a GitHub account' },
    { ref: 'p/agent-95', name: 'agent-95', title: 'Needs a secret' },
    { ref: 'p/agent-41', name: 'agent-41', title: 'Watch CI' },
  ]);
  assert.deepEqual([...labels.values()], ['94', '95', 'WC']);
});
