// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { byDay, dayLabel, noticeText, noticeView, noticeVerb } from './notifications.ts';

const notice = (n: Partial<T.Notification>): T.Notification =>
  ({ id: 'n1', kind: 'finished', project: 'shop', agent: 'agent-3', ref: 'shop/agent-3', at: '2026-10-05T10:00:00Z', seen: false, ...n }) as T.Notification;
const agents = [{ ref: 'shop/agent-3' }] as T.Agent[];

test('a notification says what the agent did', () => {
  assert.equal(noticeVerb(notice({})), 'finished');
  assert.equal(noticeVerb(notice({ status: 'blocked' })), 'is blocked');
  assert.equal(noticeVerb(notice({ kind: 'question' })), 'asks you');
  assert.equal(noticeVerb(notice({ kind: 'media', media: { kind: 'recording' } as T.MediaItem })), 'saved a recording');
});

test('an OS notification names the project and the task', () => {
  assert.deepEqual(noticeText(notice({ title: 'Checkout page', text: 'Done.' }), 'Shop'), {
    title: 'agent-3 finished',
    body: 'Done.\nShop · Checkout page',
  });
});

test('clicking goes to the agent, its media, or the chat that answers a question', () => {
  assert.deepEqual(noticeView(notice({}), agents), { kind: 'agent', ref: 'shop/agent-3', tab: 'chat' });
  assert.deepEqual(noticeView(notice({ kind: 'media' }), agents), { kind: 'agent', ref: 'shop/agent-3', tab: 'media' });
  assert.deepEqual(noticeView(notice({ kind: 'question' }), agents), { kind: 'project', project: 'shop', tab: 'chat' });
  assert.deepEqual(noticeView(notice({ kind: 'media' }), []), { kind: 'project', project: 'shop', tab: 'media' });
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
