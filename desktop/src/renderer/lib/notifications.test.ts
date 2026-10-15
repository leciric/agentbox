// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { byDay, dayLabel, noticeText, noticeView } from './notifications.ts';

const notice = (n: Partial<T.Notification>): T.Notification =>
  ({ id: 'n1', kind: 'question', project: 'shop', agent: 'agent-3', ref: 'shop/agent-3', at: '2026-10-05T10:00:00Z', seen: false, ...n }) as T.Notification;

test('an OS notification names the agent, the project and the task', () => {
  assert.deepEqual(noticeText(notice({ title: 'Checkout page', text: 'Which payment provider?' }), 'Shop'), {
    title: 'agent-3 asks you',
    body: 'Which payment provider?\nShop · Checkout page',
  });
});

test('clicking a question goes to the project chat that answers it', () => {
  assert.deepEqual(noticeView(notice({})), { kind: 'project', project: 'shop', tab: 'chat' });
});

test('history groups by day, newest first', () => {
  const now = new Date(2026, 9, 5, 15).getTime();
  const at = (d: number, h: number) => new Date(2026, 9, d, h).toISOString();
  assert.equal(dayLabel(at(5, 1), now), 'Today');
  assert.equal(dayLabel(at(4, 23), now), 'Yesterday');
  const groups = byDay([at(5, 9), at(5, 2), at(4, 8), at(1, 8)], (s) => s, now);
  assert.deepEqual(groups.map(([day, list]) => [day, list.length]).slice(0, 2), [['Today', 2], ['Yesterday', 1]]);
  assert.equal(groups.length, 3);
});
