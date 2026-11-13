// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { awaitingReview, byDay, dayLabel, dayOf, isWorkFilter, mergeHead, outcomeOf, workQuery, workRows } from './work.ts';

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
  assert.equal(workQuery('', '  ', 50).toString(), 'limit=50&brief=1');
  assert.equal(workQuery('review', ' dark ', 20, '1:x').toString(), 'limit=20&brief=1&filter=review&q=dark&cursor=1%3Ax');
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

test('workRows is one flat list: each day, then its work with its stacks together', () => {
  const day = (d: number, h: number) => new Date(2026, 9, d, h).toISOString();
  const on = (agent: string, at: string, branch: string, baseRef?: string) => item(agent, at, { item: { project: 'pawly', agent, ref: `pawly/${agent}`, finishedAt: at, fingerprint: '', branch, baseRef } });
  const rows = workRows([on('child', day(23, 18), 'b-child', 'b-parent'), on('other', day(23, 12), 'b-other', 'main'), on('parent', day(23, 9), 'b-parent', 'main'), on('old', day(21, 9), 'b-old', 'b-parent')]);
  assert.deepEqual(
    rows.map((r) => (r.kind === 'day' ? r.day : `${r.work.item.agent}:${r.depth}${r.first ? ':first' : ''}`)),
    // A day lists a parent with what stands on it, where the parent is; under another day that parent isn't listed, so that work stands alone.
    ['2026-10-23', 'other:0:first', 'parent:0', 'child:1', '2026-10-21', 'old:0:first'],
  );
  assert.deepEqual(
    rows.map((r) => r.key),
    ['day:2026-10-23', 'pawly/other', 'pawly/parent', 'pawly/child', 'day:2026-10-21', 'pawly/old'],
  );
  assert.deepEqual(workRows([]), []);
});

test('mergeHead refreshes the first page and keeps the later ones without repeating or losing work', () => {
  const at = (n: number) => new Date(2026, 9, 23, 12, 0, 60 - n).toISOString(); // n = 1 is the newest
  const page = (agents: number[], next?: string): T.WorkPage => ({ items: agents.map((n) => item(`a${String(n).padStart(2, '0')}`, at(n))), next, total: 9, counts: { '': 9 } });
  const agents = (d: { pages: T.WorkPage[] } | undefined) => d?.pages.map((p) => p.items.map((w) => w.item.agent).join(' '));
  const loaded = { pages: [page([2, 3, 4], 'c4'), page([5, 6, 7], 'c7'), page([8, 9])], pageParams: ['', 'c4', 'c7'] };

  // Nothing moved: the pages are the ones loaded, the later ones untouched.
  const same = mergeHead(loaded, page([2, 3, 4], 'c4'));
  assert.deepEqual(agents(same), ['a02 a03 a04', 'a05 a06 a07', 'a08 a09']);
  assert.equal(same?.pages[1], loaded.pages[1]);

  // A new piece of work pushes the first page's last onto the second: it is neither lost nor listed twice.
  const pushed = mergeHead(loaded, { ...page([1, 2, 3], 'c3'), total: 10 });
  assert.deepEqual(agents(pushed), ['a01 a02 a03', 'a05 a06 a07', 'a08 a09']);
  assert.equal(pushed?.pages[0].total, 10);
  assert.deepEqual(pushed?.pageParams, ['', 'c4', 'c7']);

  // One left the first page (deleted), so it reaches further down: what it now holds leaves the later pages.
  assert.deepEqual(agents(mergeHead(loaded, page([2, 4, 5], 'c5'))), ['a02 a04 a05', 'a06 a07', 'a08 a09']);

  // The same moment is ordered by project and agent, as the daemon orders it.
  const tied = { pages: [page([2], 'c'), { ...page([]), items: [item('b', at(3)), item('c', at(3))] }], pageParams: ['', 'c'] };
  assert.deepEqual(agents(mergeHead(tied, { ...page([]), items: [item('a', at(3)), item('b', at(3))], next: 'n' })), ['a b', 'c']);

  // With one page loaded, or a first page that is the whole list, it is the list.
  assert.deepEqual(agents(mergeHead({ pages: [page([2, 3], 'c3')], pageParams: [''] }, page([1, 2], 'c2'))), ['a01 a02']);
  assert.deepEqual(mergeHead(loaded, page([1, 2])), { pages: [page([1, 2])], pageParams: [''] });
  assert.equal(mergeHead(undefined, page([1])), undefined);
});
