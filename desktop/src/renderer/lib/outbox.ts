// What was written in a shared project while its owner couldn't be reached
// (internal/daemon/shareoutbox.go). The daemon keeps it and sends it, in the
// order it was written, once the owner is back; until then the chat it was
// written in shows it at its end as waiting, with a way to take it back. One
// the owner refused once back (the project unshared, the agent gone) stays
// there with why, to take back into the composer or dismiss.
import { useQuery, type QueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
import * as T from '../../shared/api.ts';
import { api } from './api.ts';
import { failed, unsentEvent } from './sending.ts';

export const outboxKey = ['shareOutbox'];

const none: T.ShareQueued[] = [];

// isRemoteRef says a chat is in a project another AgentBox shares: its name
// here is "<remote>~<project>".
export const isRemoteRef = (ref: string) => ref.split('/')[0].includes('~');

// useOutbox is everything waiting for an owner, read only once a shared
// project's chat is open; the daemon's events keep it current (lib/events.ts).
export function useOutbox(enabled: boolean): T.ShareQueued[] {
  const q = useQuery({ queryKey: outboxKey, queryFn: api.shareOutbox, enabled });
  return q.data ?? none;
}

// useChatOutbox is what waits for ref's chat to be sent.
export function useChatOutbox(ref: string): T.ShareQueued[] {
  const all = useOutbox(isRemoteRef(ref));
  return useMemo(() => {
    const mine = all.filter((q) => q.chat === ref);
    return mine.length ? mine : none;
  }, [all, ref]);
}

// keptAway, given what a message was answered with, reads the outbox again
// when the daemon kept it for an owner that is away, so its stand-in there is
// ready before the one the composer made goes (lib/sending.ts).
export async function keptAway(queryClient: QueryClient, item: T.ChatItem): Promise<T.ChatItem> {
  if (item.delivery === T.ChatAsideAway) await queryClient.fetchQuery({ queryKey: outboxKey, queryFn: api.shareOutbox, staleTime: 0 }).catch(() => {});
  return item;
}

// applyOutbox takes the outbox as the daemon's event says it is now. A kept
// message that left it because it was sent (not taken back, not refused)
// stays shown, as on its way, until its chat's own copy arrives over the
// owner's events a moment later (withQueued), or for a minute at most: the
// chat doesn't go without it in between.
const sent = new Map<number, { q: T.ShareQueued; at: number }>();
const cancelled = new Set<number>();
const sentFor = 60_000;

export function applyOutbox(queryClient: QueryClient, list: T.ShareQueued[]): void {
  const now = Date.now();
  for (const [id, s] of sent) if (now - s.at > sentFor) sent.delete(id);
  const still = new Set(list.map((q) => q.id));
  for (const q of queryClient.getQueryData<T.ShareQueued[]>(outboxKey) ?? []) {
    if (!still.has(q.id) && !q.error && q.chat && !cancelled.has(q.id)) sent.set(q.id, { q, at: now });
  }
  queryClient.setQueryData(outboxKey, list);
}

export const isQueued = (it: Pick<T.ChatItem, 'id'>) => it.id.startsWith(T.ShareQueuedPrefix);

const queuedID = (q: T.ShareQueued) => `${T.ShareQueuedPrefix}${q.id}`;

// The stand-ins, made once per state of each kept message, so the
// timeline's rows see the same item until it changes.
const standIns = new WeakMap<T.ShareQueued, T.ChatItem>();
const byItem = new Map<string, T.ShareQueued>();

// withQueued is ref's thread with what waits for its owner at the end, after
// what the chat holds: each a message of its own that starts no turn here
// (the owner starts it once it has it), or, refused, one that failed. One
// just sent shows as on its way until the chat holds it.
export function withQueued(thread: T.ChatThread, queued: T.ShareQueued[], ref: string): T.ChatThread {
  const arriving = [...sent.values()].filter((s) => s.q.chat === ref && Date.now() - s.at < sentFor && !echoed(thread, s.q)).map((s) => s.q);
  if (queued.length === 0 && arriving.length === 0) return thread;
  const items = [...thread.items];
  for (const q of [...arriving, ...queued]) {
    let item = standIns.get(q);
    if (!item || (item.delivery === T.ChatAsideAway) !== queued.includes(q)) {
      item = standIn(q, !queued.includes(q));
      standIns.set(q, item);
    }
    byItem.set(item.id, q);
    items.push(item);
  }
  return { ...thread, items };
}

// echoed says the chat holds a message sent from the outbox: the same words,
// near its end.
function echoed(thread: T.ChatThread, q: T.ShareQueued): boolean {
  return thread.items.slice(-20).some((it) => (q.to ? it.kind === 'notice' && it.to === q.to : it.kind === 'user' || it.kind === 'aside') && (it.text ?? '').trim() === q.text.trim());
}

function standIn(q: T.ShareQueued, sending = false): T.ChatItem {
  const id = queuedID(q);
  const delivery = q.error ? failed : sending ? 'waiting' : T.ChatAsideAway;
  const base = { id, turn: id, text: q.text, createdAt: q.createdAt, updatedAt: q.createdAt, delivery };
  if (q.to) return { ...base, kind: 'notice', to: q.to };
  return { ...base, kind: 'user', result: { state: q.error ? 'failed' : 'waiting', endedAt: q.createdAt } };
}

// queuedOf is the kept message a stand-in stands for.
export const queuedOf = (item: Pick<T.ChatItem, 'id'>): T.ShareQueued | undefined => byItem.get(item.id);

// cancelQueued takes a kept message back: it is never sent. Refused, it is
// forgotten.
export function cancelQueued(queryClient: QueryClient, q: T.ShareQueued): Promise<void> {
  // Its leaving the outbox, which the daemon's event may say first, isn't
  // its being sent.
  cancelled.add(q.id);
  return api.cancelShareQueued(q.id).then(
    () => void queryClient.setQueryData<T.ShareQueued[]>(outboxKey, (list) => list?.filter((it) => it.id !== q.id)),
    (err: unknown) => {
      cancelled.delete(q.id);
      throw err;
    },
  );
}

// editQueued takes a kept message back into its chat's composer, to change
// and send again. Its pictures and files stay with the daemon, which drops
// them: the text comes back.
export async function editQueued(queryClient: QueryClient, q: T.ShareQueued): Promise<void> {
  await cancelQueued(queryClient, q);
  if (q.chat) window.dispatchEvent(new CustomEvent(unsentEvent, { detail: { ref: q.chat, text: q.text, attachments: [] } }));
}
