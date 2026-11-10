// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { awaitingReview, byDay, dayLabel, dayOf, isWorkFilter, outcomeOf, workQuery } from './work.ts';

const item = (ref: string, at: string, more: Partial<T.WorkItem> = {}): T.WorkItem => ({
  item: { project: 'pawly', agent: ref, ref: `pawly/${ref}`, finishedAt: at, fingerprint: '' },
  status: 'done',
  at,
  ...more,
});

test('byDay keeps the order and starts a group at each new local day', () => {
  const items = [item('a', new Date(2026, 9, 23, 18).toISOString()), item('b', new Date(2026, 9, 23, 9).toISOString()), item('c', new Date(2026, 9, 21, 12).toISOString())];
  const groups = byDay(items);
  assert.deepEqual(
    groups.map((g) => [g.day, g.items.map((i) => i.item.agent)]),
    [
      ['2026-10-23', ['a', 'b']],
      ['2026-10-21', ['c']],
    ],
  );
  assert.equal(dayOf(new Date(2026, 0, 5, 0, 30).toISOString()), '2026-01-05');
});

test('dayLabel says today, yesterday, a weekday, then the date', () => {
  const now = new Date(2026, 9, 23, 12);
  const words = { today: 'Today', yesterday: 'Yesterday' };
  assert.equal(dayLabel('2026-10-23', now, 'en-US', words), 'Today');
  assert.equal(dayLabel('2026-10-22', now, 'en-US', words), 'Yesterday');
  assert.equal(dayLabel('2026-10-20', now, 'en-US', words), 'Tuesday');
  assert.equal(dayLabel('2026-09-01', now, 'en-US', words), 'September 1');
  assert.equal(dayLabel('2025-09-01', now, 'en-US', words), 'September 1, 2025');
});

test('workQuery leaves out what is empty', () => {
  assert.equal(workQuery('', '  ', 50).toString(), 'limit=50');
  assert.equal(workQuery('review', ' dark ', 20, '1:x').toString(), 'limit=20&filter=review&q=dark&cursor=1%3Ax');
  assert.ok(isWorkFilter('merged') && isWorkFilter('') && isWorkFilter(undefined) && !isWorkFilter('nope'));
});

test('only finished work without a verdict awaits review', () => {
  const at = '2026-10-23T10:00:00Z';
  assert.equal(awaitingReview({ ...item('a', at).item, fingerprint: '{"f":"1"}' }), true);
  assert.equal(awaitingReview({ ...item('a', at).item, fingerprint: '{"f":"1"}', verdict: 'reviewed' }), false);
  assert.equal(awaitingReview(item('a', at).item), false);
});

test('outcomeOf is the report, else whether it is still at work', () => {
  const at = '2026-10-23T10:00:00Z';
  const report: T.AgentReport = { id: 'r', project: 'pawly', agent: 'a', createdAt: at, status: 'partial', summary: 's' };
  assert.equal(outcomeOf(item('a', at, { report })), 'partial');
  assert.equal(outcomeOf(item('a', at, { status: 'working' })), 'working');
  assert.equal(outcomeOf(item('a', at)), '');
});
