// A chat's thread as the phone holds it: its latest page, read once, then
// kept current by the daemon's chat events (EventChat), each numbered (seq)
// so a missed one is noticed and the thread read again. This is the core of
// the desktop's renderer/lib/chat.ts (advance, applyEvent), without its
// search windows; the two should move to desktop/src/shared together.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api.ts';

export const chatKey = (ref: string) => ['chat', ref];

// A chat opens on its latest messages; older ones come a page at a time.
export const pageSize = 20;

// isMessage is what counts towards a page, as in package chat.
export const isMessage = (it: T.ChatItem) => (it.kind === 'user' || it.kind === 'aside' || it.kind === 'assistant') && !it.parent && !it.hidden;

// advance applies events in order. Events the thread already includes are
// skipped; a missing one means the thread has to be read again ('gap').
export function advance(thread: T.ChatThread, events: T.ChatEvent[]): T.ChatThread | 'gap' {
  let next = thread;
  for (const ev of events) {
    if (ev.seq <= next.seq) continue;
    if (ev.seq !== next.seq + 1) return 'gap';
    next = applyEvent(next, ev);
  }
  return next;
}

export function applyEvent(thread: T.ChatThread, ev: T.ChatEvent): T.ChatThread {
  const next = { ...thread, seq: ev.seq };
  if (ev.cleared) {
    next.items = [];
  } else if (ev.session) {
    next.session = ev.session;
  } else if (ev.item) {
    const i = indexOf(thread.items, ev.item.id);
    // An item older than everything held is on a page not read yet.
    if (i < 0 && thread.older && thread.items[0] && Date.parse(ev.item.createdAt) < Date.parse(thread.items[0].createdAt)) return next;
    next.items = i < 0 ? [...thread.items, ev.item] : thread.items.with(i, ev.item);
  } else if (ev.append) {
    const i = indexOf(thread.items, ev.append.id);
    if (i >= 0) next.items = thread.items.with(i, { ...thread.items[i], text: (thread.items[i].text ?? '') + ev.append.text });
  } else if (ev.after) {
    // A rollback: what came after the turn it went back to is gone.
    next.items = thread.items.slice(0, indexOf(thread.items, ev.after) + 1);
  }
  return next;
}

// indexOf searches from the end, where changes happen.
function indexOf(items: T.ChatItem[], id: string): number {
  for (let i = items.length - 1; i >= 0; i--) if (items[i].id === id) return i;
  return -1;
}

// prependPage puts an older page in front of what's held.
export function prependPage(thread: T.ChatThread, page: T.ChatThread): T.ChatThread {
  const held = new Set(thread.items.map((it) => it.id));
  return { ...thread, items: [...page.items.filter((it) => !held.has(it.id)), ...thread.items], older: page.older };
}

// applyChatEvent is what a chat event does to the phone's queries: the
// thread, when it's held, and the chat's state on the lists (the agent's
// chat, the lead's on its project).
export function applyChatEvent(queryClient: QueryClient, ev: T.ChatEvent): void {
  if (ev.session) {
    const { state, stalledSince } = ev.session;
    const background = ev.session.background?.length ? ev.session.background : undefined;
    queryClient.setQueryData<T.Agent[]>(['agents'], (agents) =>
      agents?.some((a) => a.ref === ev.agent && (a.chat !== state || a.stalledSince !== stalledSince))
        ? agents.map((a) => (a.ref === ev.agent ? { ...a, chat: state, stalledSince, background } : a))
        : agents,
    );
    queryClient.setQueryData<T.ProjectChat>(['projectChat', ev.agent.split('/')[0]], (info) =>
      info && info.ref === ev.agent && info.chat !== state ? { ...info, chat: state } : info,
    );
  }
  const thread = queryClient.getQueryData<T.ChatThread>(chatKey(ev.agent));
  if (!thread) return;
  const next = advance(thread, [ev]);
  if (next === 'gap') void queryClient.invalidateQueries({ queryKey: chatKey(ev.agent) });
  else if (next !== thread) queryClient.setQueryData(chatKey(ev.agent), next);
}

// pendingPermissions are the permission requests waiting for an answer.
export function pendingPermissions(thread: T.ChatThread): T.ChatItem[] {
  return thread.items.filter((it) => it.kind === 'permission' && !it.permission?.outcome);
}

// isActive says a work item is happening now.
export function isActive(it: T.ChatItem | undefined): boolean {
  return (
    !!it &&
    ((it.kind === 'thought' && !!it.streaming) ||
      (it.kind === 'tool' && (it.tool?.status === 'pending' || it.tool?.status === 'in_progress')) ||
      (it.kind === 'subagent' && it.subagent?.state === 'running'))
  );
}
