// What the app tells you about agents, across projects: a finish, a question
// that reached you, a screenshot or recording kept. The daemon keeps them
// (GET /v1/notifications) and sends each as it happens (EventNotification);
// here is what they say, and where clicking one goes. The bell, the toasts,
// the OS notifications and the all-projects Media view all use these.
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import type { View } from '../App.tsx';
import { api } from './api.ts';
import { formatDate, t } from '../../shared/i18n/index.ts';
import { kindInfo } from './media.ts';

const DAY = 24 * 60 * 60 * 1000;

// What a finish's report status says the agent did.
export function finishVerb(status: string): string {
  switch (status) {
    case 'partial':
      return t('shell.notice.verb.partial');
    case 'blocked':
      return t('shell.notice.verb.blocked');
    case 'failed':
      return t('shell.notice.verb.failed');
    default:
      return t('shell.notice.verb.done');
  }
}

// noticeVerb is what the agent did, after its name: "saved a screenshot".
export function noticeVerb(n: T.Notification): string {
  if (n.kind === 'media') return t('shell.notice.verb.media', { kind: kindInfo(n.media?.kind ?? '').one });
  if (n.kind === 'question') return t('shell.notice.verb.question');
  return finishVerb(n.status ?? '');
}

// noticeText is an OS notification's: its title names the agent, and its body
// the text and where it's from, since the OS shows neither the project nor
// the agent's title on its own.
export function noticeText(n: T.Notification, projectName: string): { title: string; body: string } {
  const title = `${n.agent} ${noticeVerb(n)}`;
  const from = [projectName, n.title].filter(Boolean).join(' · ');
  return { title, body: [n.text, from].filter(Boolean).join('\n') };
}

// noticeView is the page a notification opens: a media item's agent at its
// Media tab (where the item opens in the viewer, see App), a question's
// project chat, where you answer it, and a finish's agent at its chat. An
// agent destroyed since leaves its project.
export function noticeView(n: T.Notification, agents: readonly T.Agent[] | undefined): View {
  if (n.kind === 'question') return { kind: 'project', project: n.project, tab: 'chat' };
  if (agents && !agents.some((a) => a.ref === n.ref)) return { kind: 'project', project: n.project, tab: n.kind === 'media' ? 'media' : undefined };
  return { kind: 'agent', ref: n.ref, tab: n.kind === 'media' ? 'media' : 'chat' };
}

// dayLabel is the heading a time goes under: Today, Yesterday, or its date.
export function dayLabel(iso: string, now = Date.now()): string {
  const start = new Date(now);
  start.setHours(0, 0, 0, 0);
  const at = new Date(iso).getTime();
  if (at >= start.getTime()) return t('shell.notice.today');
  if (at >= start.getTime() - DAY) return t('shell.notice.yesterday');
  return formatDate(iso, { weekday: 'long', day: 'numeric', month: 'long' });
}

// byDay groups a newest-first list under dayLabel's headings, in order.
export function byDay<V>(list: readonly V[], at: (v: V) => string, now = Date.now()): [string, V[]][] {
  const groups = new Map<string, V[]>();
  for (const v of list) {
    const day = dayLabel(at(v), now);
    groups.set(day, [...(groups.get(day) ?? []), v]);
  }
  return [...groups];
}

// markSeen applies a SeeNotificationsRequest to what's on screen at once,
// then tells the daemon, which tells every other window.
export function markSeen(queryClient: QueryClient, req: T.SeeNotificationsRequest): Promise<unknown> {
  const ids = new Set(req.ids ?? []);
  const media = new Set(req.media ?? []);
  const hit = (n: T.Notification) => req.all || ids.has(n.id) || (n.media !== undefined && media.has(n.media.id));
  queryClient.setQueryData<T.Notification[]>(['notifications'], (list) => list?.map((n) => (hit(n) && !n.seen ? { ...n, seen: true } : n)));
  const seenMedia = new Set(media);
  for (const n of queryClient.getQueryData<T.Notification[]>(['notifications']) ?? []) if (n.media && hit(n)) seenMedia.add(n.media.id);
  queryClient.setQueriesData<T.MediaItem[]>({ queryKey: ['allMedia'] }, (items) =>
    items?.map((m) => (m.unseen && (req.all || seenMedia.has(m.id)) ? { ...m, unseen: false } : m)),
  );
  return api.seeNotifications(req).catch(() => queryClient.invalidateQueries({ queryKey: ['notifications'] }));
}

export function useMarkSeen(): (req: T.SeeNotificationsRequest) => Promise<unknown> {
  const queryClient = useQueryClient();
  return (req) => markSeen(queryClient, req);
}

// useSeeMedia marks a media item seen when it's opened anywhere, if a
// notification about it is still unseen; otherwise it costs nothing.
export function useSeeMedia(): (id: string | null) => void {
  const queryClient = useQueryClient();
  return (id) => {
    if (!id) return;
    const unseen = queryClient.getQueryData<T.Notification[]>(['notifications'])?.some((n) => !n.seen && n.media?.id === id);
    const marked = queryClient.getQueriesData<T.MediaItem[]>({ queryKey: ['allMedia'] }).some(([, items]) => items?.some((m) => m.id === id && m.unseen));
    if (unseen || marked) void markSeen(queryClient, { media: [id] });
  };
}
