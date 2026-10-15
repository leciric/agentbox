import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { graceMs, hold } from './grace.ts';
import { activeProjects, agentSt, type Row } from './leadFirst.ts';

test('hold keeps a name for the grace after it left, and says when to look again', () => {
  const seen = new Map<string, number>();
  assert.deepEqual([...hold(seen, ['a', 'b'], 0).held], ['a', 'b']);
  let out = hold(seen, ['b'], 1000);
  assert.deepEqual([...out.held].sort(), ['a', 'b']);
  assert.equal(out.until, graceMs);
  out = hold(seen, ['b'], graceMs);
  assert.deepEqual([...out.held], ['b']);
  assert.equal(out.until, undefined);
  assert.equal(seen.has('a'), false);
});

// The event sequence a busy project goes through, as the sidebar's queries
// see it: the lead's session flips running → ready → running between queued
// turns, with no agent yet, and to waiting on a permission, and the agent it
// started finishes (ready, done) a moment before the review list puts it on
// review. Without a grace the project is idle at each gap; with one it never
// leaves.
test('a busy project going through its gaps never reads idle under the grace', () => {
  const agent = (chat: string, outcome = '') => ({ ref: 'p/a1', name: 'a1', project: 'p', state: 'running', chat, outcome, createdAt: '' }) as unknown as T.Agent;
  const steps: { at: number; lead: string; agent?: T.Agent; review?: boolean }[] = [
    { at: 0, lead: 'running' },
    { at: 400, lead: 'ready' },
    { at: 450, lead: 'running' },
    { at: 2000, lead: 'running', agent: agent('starting') },
    { at: 3000, lead: 'waiting', agent: agent('running') },
    { at: 5000, lead: 'ready', agent: agent('ready', 'done') },
    { at: 5600, lead: 'ready', agent: agent('ready', 'done'), review: true },
  ];
  const seen = new Map<string, number>();
  const raw: boolean[] = [];
  for (const s of steps) {
    const ctx = { review: new Set(s.review ? ['p/a1'] : []), asking: new Set<string>() };
    const rows: Row[] = s.agent ? [{ agent: s.agent, st: agentSt(s.agent, ctx) }] : [];
    const now = activeProjects(rows, new Map([['p', s.lead]]), []);
    raw.push(now.has('p'));
    assert.equal(hold(seen, now, s.at).held.has('p'), true, `idle at ${s.at}ms`);
  }
  // Without it, it dropped out at the lead's turn gap and as the agent finished.
  assert.deepEqual(raw, [true, false, true, true, true, false, true]);
});
