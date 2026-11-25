import assert from 'node:assert/strict';
import test from 'node:test';
import * as T from '../../shared/api.ts';
import { timelineRows } from './chat.ts';
import { QueryClient } from '@tanstack/react-query';
import { applyOutbox, cancelQueued, isQueued, isRemoteRef, outboxKey, queuedOf, withQueued } from './outbox.ts';
import { api } from './api.ts';
import { failed, findSending, sendMessage, withSending } from './sending.ts';

const at = '2026-11-25T10:00:00Z';
const ref = 'r1~web/lead';
const done = { state: 'completed', endedAt: at };
const item = (over: Partial<T.ChatItem>): T.ChatItem => ({ id: 'i', turn: 'i', kind: 'user', createdAt: at, updatedAt: at, ...over });
const thread = (items: T.ChatItem[]): T.ChatThread => ({ agent: 'r1~web/lead', seq: 1, items, session: { state: 'off', tool: 'claude', options: [], commands: [] } }) as T.ChatThread;
const queued = (over: Partial<T.ShareQueued>): T.ShareQueued => ({ id: 1, remote: 'r1', owner: 'Alice', kind: T.ShareDidMessage, project: 'r1~web', chat: 'r1~web/lead', text: 'hi', createdAt: at, ...over });

test('a message kept for an away owner ends the chat, waiting, and starts nothing here', () => {
  const before = thread([item({ id: 'u1', turn: 'u1', text: 'hello', result: done }), item({ id: 'a1', turn: 'u1', kind: 'assistant', text: 'hi' })]);
  const q = queued({ id: 7, text: 'First, the footer.' });
  const shown = withQueued(before, [q], ref);
  const last = shown.items.at(-1)!;
  assert.deepEqual([last.id, last.kind, last.text, last.delivery], [`${T.ShareQueuedPrefix}7`, 'user', 'First, the footer.', T.ChatAsideAway]);
  assert.ok(isQueued(last));
  assert.equal(queuedOf(last), q);
  // No turn working for it: the owner starts that once it has it.
  assert.deepEqual(
    timelineRows(shown, new Set()).map((r) => r.type),
    ['user', 'assistant', 'user'],
  );
  // The same kept message gives the same item, so its row isn't drawn again.
  assert.equal(withQueued(before, [q], ref).items.at(-1), last);
  // What is on its way after it comes after it.
  const after = withSending(shown, [{ key: 'sending-x', ref: 'r1~web/lead', text: 'next', attachments: [], at, send: async () => item({}) }]);
  assert.deepEqual(after.items.slice(-2).map((it) => it.text), ['First, the footer.', 'next']);
});

test('one refused once the owner was back is a failed message; one to an agent a notice', () => {
  const shown = withQueued(thread([]), [queued({ id: 1, error: 'Alice refused it when it was back: agent-09 is gone' }), queued({ id: 2, to: 'agent-01', text: 'Look' })], ref);
  assert.deepEqual([shown.items[0].kind, shown.items[0].delivery], ['user', failed]);
  assert.deepEqual([shown.items[1].kind, shown.items[1].to, shown.items[1].delivery], ['notice', 'agent-01', T.ChatAsideAway]);
  assert.equal(withQueued(shown, [], ref), shown);
});

test('a message the daemon kept leaves the composer’s stand-in to the outbox', async () => {
  const mine = () => Array.from({ length: 1000 }, (_, i) => findSending(`sending-${i}`)).find((s) => s?.ref === 'r1~web/lead');
  let answer!: (it: T.ChatItem) => void;
  const reply = new Promise<T.ChatItem>((res) => (answer = res));
  const kept = item({ id: `${T.ShareQueuedPrefix}3`, delivery: T.ChatAsideAway, text: 'hi' });
  const sent = sendMessage('r1~web/lead', { text: 'hi', attachments: [] }, () => reply);
  assert.equal(mine()?.text, 'hi');
  answer(kept);
  assert.equal(await sent, kept);
  assert.equal(mine(), undefined);
});

test('only a shared project’s chats have an outbox', () => {
  assert.ok(isRemoteRef('r1~web/lead'));
  assert.ok(isRemoteRef('r1~web/agent-01'));
  assert.ok(!isRemoteRef('web/lead'));
});

test('one sent once its owner is back shows as on its way until the chat holds it', () => {
  const qc = new QueryClient();
  const q = queued({ id: 11, text: 'Ship it.' });
  const gone = queued({ id: 12, text: 'Not this.' });
  qc.setQueryData(outboxKey, [q, gone]);
  // Taken back here: never shown as sent.
  const cancel = api.cancelShareQueued;
  api.cancelShareQueued = async () => {};
  void cancelQueued(qc, gone);
  api.cancelShareQueued = cancel;
  applyOutbox(qc, []);
  assert.deepEqual(qc.getQueryData(outboxKey), []);
  const before = thread([item({ id: 'u1', turn: 'u1', text: 'hello', result: done })]);
  const shown = withQueued(before, [], ref);
  assert.deepEqual(shown.items.slice(1).map((it) => [it.text, it.delivery]), [['Ship it.', 'waiting']]);
  // Not in another chat.
  assert.equal(withQueued(before, [], 'r1~web/agent-01'), before);
  // The chat's own copy arrives: the stand-in goes.
  const arrived = thread([...before.items, item({ id: 'u2', turn: 'u2', text: 'Ship it.' })]);
  assert.equal(withQueued(arrived, [], ref), arrived);
});
