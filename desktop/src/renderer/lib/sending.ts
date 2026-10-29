// Messages on their way to a chat. The composer hands a message here and the
// chat shows it at once, with the turn it starts already working, instead of
// waiting for the daemon to answer and announce it: that round trip goes
// through the main process, the daemon's handler and its event stream, and the
// handler readies the chat first (a lead's worktree, an agent's machine), so it
// can take seconds. The daemon's own item replaces the stand-in when it
// arrives; a message that doesn't go stays in the chat, marked, to send again
// or take back into the composer.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';
import type { Pending } from './chatFiles';

export interface Sending {
  // key is the stand-in item's id, never one the daemon gives.
  key: string;
  ref: string;
  text: string;
  attachments: Pending[];
  // to is the agent a message written in a project's chat goes to instead
  // of its lead (shared/messageTarget.ts): it starts no turn of the lead's.
  to?: string;
  // at is when it was sent: the stand-in's time, and the turn's start.
  at: string;
  // after is the last item the chat held when it was sent: the daemon's copy
  // comes after it.
  after?: string;
  // item is the daemon's id for it, once the daemon has answered.
  item?: string;
  error?: string;
  send: () => Promise<T.ChatItem>;
}

export const sendingPrefix = 'sending-';

const sending = new Map<string, Sending[]>();
const listeners = new Set<() => void>();
const empty: Sending[] = [];
// The last send of each chat, so the next one waits for it: two messages
// written one after the other reach the chat in that order.
const queues = new Map<string, Promise<unknown>>();
let next = 0;

function set(ref: string, list: Sending[]): void {
  if (list.length) sending.set(ref, list);
  else sending.delete(ref);
  for (const l of listeners) l();
}

function update(key: string, change: (s: Sending) => Sending | undefined): void {
  for (const [ref, list] of sending) {
    const i = list.findIndex((s) => s.key === key);
    if (i < 0) continue;
    const changed = change(list[i]);
    set(ref, changed ? list.with(i, changed) : list.filter((_, j) => j !== i));
    return;
  }
}

export function findSending(key: string): Sending | undefined {
  for (const list of sending.values()) {
    const found = list.find((s) => s.key === key);
    if (found) return found;
  }
  return undefined;
}

// sendMessage shows a message in ref's chat now and sends it, after any
// message still on its way there. It settles with the daemon's item, or the
// error that stopped it, which the stand-in then shows.
export function sendMessage(
  ref: string,
  message: { text: string; attachments: Pending[]; after?: string; to?: string },
  send: () => Promise<T.ChatItem>,
): Promise<T.ChatItem | undefined> {
  const entry: Sending = { key: `${sendingPrefix}${++next}`, ref, text: message.text, attachments: message.attachments, at: new Date().toISOString(), after: message.after, to: message.to, send };
  set(ref, [...(sending.get(ref) ?? []), entry]);
  return deliver(entry);
}

function deliver(entry: Sending): Promise<T.ChatItem | undefined> {
  const run = (queues.get(entry.ref) ?? Promise.resolve()).then(entry.send).then(
    (item) => {
      update(entry.key, (s) => ({ ...s, item: item.id, error: undefined }));
      return item;
    },
    (err: unknown) => {
      update(entry.key, (s) => ({ ...s, error: err instanceof Error ? err.message : String(err) }));
      return undefined;
    },
  );
  queues.set(
    entry.ref,
    run.catch(() => {}),
  );
  return run;
}

// retrySending sends a message that didn't go again, as it was.
export function retrySending(key: string): void {
  const entry = findSending(key);
  if (!entry?.error) return;
  const again = { ...entry, error: undefined, at: new Date().toISOString() };
  update(key, () => again);
  void deliver(again);
}

// unsentEvent hands a message that didn't go back to its chat's composer
// (Composer listens), with its pictures and files.
export const unsentEvent = 'agentbox:unsent';

// editSending takes a message that didn't go out of the chat and puts it back
// in the composer, to change before sending it again.
export function editSending(key: string): void {
  const entry = findSending(key);
  if (!entry?.error) return;
  update(key, () => undefined);
  window.dispatchEvent(new CustomEvent(unsentEvent, { detail: { ref: entry.ref, text: entry.text, attachments: entry.attachments } }));
}

// settleSending forgets the messages of ref's chat that the chat now holds
// itself: by the daemon's id, or before its answer, as the user's message
// with the same text after the one the chat ended with when it was sent.
export function settleSending(ref: string, thread: T.ChatThread): void {
  const list = sending.get(ref);
  if (!list) return;
  const kept = list.filter((s) => !echoed(thread, s));
  if (kept.length !== list.length) set(ref, kept);
}

function echoed(thread: T.ChatThread, s: Sending): boolean {
  if (s.error) return false;
  if (s.item) return thread.items.some((it) => it.id === s.item);
  const from = s.after ? thread.items.findIndex((it) => it.id === s.after) + 1 : 0;
  return thread.items.slice(from).some((it) => (s.to ? it.kind === 'notice' && it.to === s.to : it.kind === 'user' || it.kind === 'aside') && (it.text ?? '').trim() === s.text);
}

// useSending is what is on its way to ref's chat, as it changes.
export function useSending(ref: string): Sending[] {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => sending.get(ref) ?? empty,
  );
}

// The stand-ins, made once per state of each message, so the timeline's rows
// see the same item until it changes.
const standIns = new WeakMap<Sending, T.ChatItem>();

// withSending is thread with what is on its way to it at the end, as the
// timeline shows it: a message to an idle chat heads a turn that is already
// working, one sent while a turn runs joins that turn. One that failed, or
// that goes to an agent rather than the lead, doesn't start anything.
export function withSending(thread: T.ChatThread, list: Sending[]): T.ChatThread {
  const shown = list.filter((s) => !echoed(thread, s));
  if (shown.length === 0) return thread;
  const items = [...thread.items];
  let session = thread.session;
  let turn = session.turnStartedAt ? items.findLast((it) => it.kind === 'user')?.turn : undefined;
  // A message to an agent is a notice of the lead's chat, in the turn the
  // daemon will put it in: the running one, or the last.
  const last = items.findLast((it) => it.kind === 'user')?.turn ?? '';
  for (const s of shown) {
    const into = s.to ? (turn ?? last) : s.error ? undefined : turn;
    let item = standIns.get(s);
    if (!item || item.turn !== (into ?? s.key)) {
      item = standIn(s, into);
      standIns.set(s, item);
    }
    items.push(item);
    if (!turn && !s.error && !s.to) {
      turn = item.turn;
      session = { ...session, turnStartedAt: s.at };
    }
  }
  return { ...thread, items, session };
}

// standIn is the item the timeline shows for a message on its way: one that
// heads its own turn is the user's, one inside a running turn an aside, as
// the daemon will make them. One that failed heads a turn that has ended.
function standIn(s: Sending, turn: string | undefined): T.ChatItem {
  const base = { id: s.key, text: s.text, createdAt: s.at, updatedAt: s.at };
  if (s.to) return { ...base, turn: turn ?? '', kind: 'notice', to: s.to, delivery: s.error ? failed : 'waiting' };
  if (s.error) return { ...base, turn: s.key, kind: 'user', delivery: failed, result: { state: failed, endedAt: s.at } };
  if (turn) return { ...base, turn, kind: 'aside', delivery: 'waiting' };
  return { ...base, turn: s.key, kind: 'user', delivery: 'waiting' };
}

// failed is a stand-in's delivery, and its turn's end, when it didn't go.
export const failed = 'failed';

export const isSending = (it: T.ChatItem) => it.id.startsWith(sendingPrefix);
