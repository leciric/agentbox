import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { InfiniteData } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { pageItems, pageTotal, withQuestion, withoutAgent, withoutSeen } from './inboxPages.ts';

const item = (agent: string) => ({ project: 'p', agent, ref: `p/${agent}`, finishedAt: '2026-10-15T10:00:00Z', fingerprint: agent }) as T.ReviewItem;
const note = (id: string, seen = false) => ({ id, kind: 'question', project: 'p', agent: 'a', ref: 'p/a', at: '2026-10-15T10:00:00Z', seen }) as T.Notification;
const pages = <P extends { items: unknown[]; total: number }>(...p: P[]): InfiniteData<P, string> => ({ pages: p, pageParams: p.map((_, i) => String(i)) });

test('pages read as one list with the total the first says', () => {
  const data = pages<T.ReviewPage>({ items: [item('a'), item('b')], total: 3, next: '1' }, { items: [item('c')], total: 3 });
  assert.deepEqual(pageItems(data).map((i) => i.agent), ['a', 'b', 'c']);
  assert.equal(pageTotal(data), 3);
  assert.deepEqual(pageItems(undefined), []);
  assert.equal(pageTotal(undefined), 0);
});

test('an agent reviewed leaves its page and the count', () => {
  const data = pages<T.ReviewPage>({ items: [item('a'), item('b')], total: 3, next: '1' }, { items: [item('c')], total: 3 });
  const next = withoutAgent(data, 'p/b');
  assert.deepEqual(pageItems(next).map((i) => i.agent), ['a', 'c']);
  assert.equal(pageTotal(next), 2);
  // A plain page (the newest, which the alert reads) is the same.
  const head = withoutAgent({ items: [item('a'), item('b')], total: 5 } as T.ReviewPage, 'p/a');
  assert.deepEqual(head.items.map((i) => i.agent), ['b']);
  assert.equal(head.total, 4);
  // One that isn't there changes nothing.
  assert.equal(withoutAgent(data, 'p/zzz'), data);
  assert.equal(withoutAgent(undefined, 'p/a'), undefined);
});

test('a question that arrives goes on top, once', () => {
  const data = pages<T.NotificationPage>({ items: [note('q1')], total: 1 });
  const next = withQuestion(data, note('q2'));
  assert.deepEqual(pageItems(next).map((n) => n.id), ['q2', 'q1']);
  assert.equal(pageTotal(next), 2);
  assert.equal(withQuestion(next, note('q2')), next);
  assert.equal(withQuestion(undefined, note('q3')), undefined);
});

test('seeing questions takes them off the unseen ones', () => {
  const data = pages<T.NotificationPage>({ items: [note('q1'), note('q2')], total: 40, next: '2' });
  const one = withoutSeen(data, { ids: ['q1'] });
  assert.deepEqual(pageItems(one).map((n) => n.id), ['q2']);
  assert.equal(pageTotal(one), 39);
  // Every question, loaded or not.
  const all = withoutSeen(data, { kind: 'question' });
  assert.equal(pageItems(all).length, 0);
  assert.equal(pageTotal(all), 0);
  // Media marks aren't questions.
  assert.equal(withoutSeen(data, { media: ['m1'] }), data);
});
