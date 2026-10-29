import assert from 'node:assert/strict';
import test from 'node:test';
import type * as T from '../../shared/api';
import { timelineRows } from './chat.ts';
import { editSending, failed, findSending, retrySending, sendMessage, settleSending, unsentEvent, withSending, type Sending } from './sending.ts';

const at = '2026-10-28T10:00:00Z';
const item = (over: Partial<T.ChatItem>): T.ChatItem => ({ id: 'i', turn: 'i', kind: 'user', createdAt: at, updatedAt: at, ...over });
const session = (over: Partial<T.ChatSession> = {}): T.ChatSession => ({ state: 'ready', tool: 'claude', options: [], commands: [], ...over }) as T.ChatSession;
const thread = (items: T.ChatItem[], over: Partial<T.ChatSession> = {}): T.ChatThread => ({ seq: 1, items, session: session(over) }) as T.ChatThread;
const done = { state: 'completed', endedAt: at };

// pending is a message on its way, as useSending lists it, without sending it.
let n = 0;
const pending = (over: Partial<Sending> = {}): Sending => ({ key: `sending-t${++n}`, ref: 'p/lead', text: 'hello', attachments: [], at, send: async () => item({}), ...over });

const later = () => {
  let resolve!: (v: T.ChatItem) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<T.ChatItem>((res, rej) => ((resolve = res), (reject = rej)));
  return { promise, resolve, reject };
};

test('a message to an idle chat heads a turn that is already working', () => {
  const before = thread([item({ id: 'u1', turn: 'u1', text: 'hi', result: done }), item({ id: 'a1', turn: 'u1', kind: 'assistant', text: 'hello' })]);
  const s = pending({ text: 'next' });
  const shown = withSending(before, [s]);
  assert.equal(shown.items.length, 3);
  assert.deepEqual([shown.items[2].kind, shown.items[2].text, shown.items[2].delivery], ['user', 'next', 'waiting']);
  assert.equal(shown.session.turnStartedAt, s.at);
  const rows = timelineRows(shown, new Set()).map((r) => r.type);
  assert.deepEqual(rows.slice(-3), ['user', 'working', 'thinking']);
  // The same message gives the same item, so its row isn't drawn again.
  assert.equal(withSending(before, [s]).items[2], shown.items[2]);
});

test('a message sent while a turn runs joins it, as an aside', () => {
  const running = thread([item({ id: 'u1', turn: 'u1', text: 'go' }), item({ id: 'a1', turn: 'u1', kind: 'assistant', text: 'on it', streaming: true })], { turnStartedAt: at, state: 'running' });
  const shown = withSending(running, [pending({ text: 'also this' })]);
  assert.deepEqual([shown.items[2].kind, shown.items[2].turn], ['aside', 'u1']);
  // Two sent at once into an idle chat: the first starts the turn, the second joins it.
  const idle = withSending(thread([]), [pending({ text: 'one' }), pending({ text: 'two' })]);
  assert.deepEqual(
    idle.items.map((it) => [it.kind, it.turn === idle.items[0].id]),
    [
      ['user', true],
      ['aside', true],
    ],
  );
});

test("the daemon's copy replaces the stand-in, by id or, before it answered, by text after the last item", () => {
  const base = [item({ id: 'u1', turn: 'u1', text: 'same', result: done })];
  // The same text earlier in the chat isn't the copy.
  const waiting = pending({ text: 'same', after: 'u1' });
  assert.equal(withSending(thread(base), [waiting]).items.length, 2);
  const echoed = [...base, item({ id: 'u2', turn: 'u2', text: 'same' })];
  assert.equal(withSending(thread(echoed), [waiting]).items.length, 2);
  // By id once the daemon answered, whatever text it kept.
  const answered = pending({ text: '$skill do it', after: 'u1', item: 'u2' });
  assert.equal(withSending(thread(echoed), [answered]).items.length, 2);
  assert.equal(withSending(thread(base), [answered]).items.length, 2);
});

test('a message that failed starts nothing, and says so', () => {
  const s = pending({ error: 'the daemon is gone' });
  const shown = withSending(thread([]), [s]);
  assert.deepEqual([shown.items[0].kind, shown.items[0].delivery, shown.items[0].result?.state], ['user', failed, failed]);
  assert.equal(shown.session.turnStartedAt, undefined);
  assert.deepEqual(
    timelineRows(shown, new Set()).map((r) => r.type),
    ['user'],
  );
});

test('messages go in the order they were written, and one that fails can be sent again or edited', async () => {
  const ref = 'p/agent-1';
  const first = later();
  const order: string[] = [];
  const one = sendMessage(ref, { text: 'one', attachments: [] }, () => (order.push('one'), first.promise));
  const two = sendMessage(ref, { text: 'two', attachments: [] }, async () => (order.push('two'), item({ id: 'd2', text: 'two' })));
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(order, ['one'], 'the second waits for the first');
  first.reject(new Error('refused'));
  assert.equal(await one, undefined);
  assert.equal((await two)?.id, 'd2');
  assert.deepEqual(order, ['one', 'two']);

  // The one that went is forgotten once the chat has it; the other waits, marked.
  settleSending(ref, thread([item({ id: 'd2', text: 'two' })]));
  const left = ['sending-1', 'sending-2', 'sending-3'].map(findSending).filter((s): s is Sending => !!s && s.ref === ref);
  assert.deepEqual(
    left.map((s) => [s.text, s.error]),
    [['one', 'refused']],
  );

  // Sending it again goes as it was.
  const failedOne = left[0];
  failedOne.send = async () => item({ id: 'd1', text: 'one' });
  retrySending(failedOne.key);
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(findSending(failedOne.key)?.error, undefined);
  assert.equal(findSending(failedOne.key)?.item, 'd1');

  // Edit takes it out of the chat and back to the composer.
  const third = sendMessage(ref, { text: 'three', attachments: [] }, async () => {
    throw new Error('no');
  });
  await third;
  const target = new EventTarget();
  (globalThis as { window?: unknown }).window = target;
  const heard: unknown[] = [];
  target.addEventListener(unsentEvent, (e) => heard.push((e as CustomEvent).detail));
  const stuck = ['sending-1', 'sending-2', 'sending-3', 'sending-4', 'sending-5'].map(findSending).find((s) => s?.text === 'three')!;
  editSending(stuck.key);
  assert.equal(findSending(stuck.key), undefined);
  assert.deepEqual(heard, [{ ref, text: 'three', attachments: [] }]);
});
