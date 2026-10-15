// What the app tells you needs you, across projects: a question that reached
// you, which the daemon keeps (GET /v1/notifications) and sends as it happens
// (EventNotification). Finished work isn't one: it's in the review list. The
// toasts, the OS notifications and the inbox all use these. The Media views'
// unseen marks ride on the same seen state (a kept screenshot or recording is
// a notification the daemon doesn't list).
import { useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import type { View } from '../App.tsx';
import { api } from './api.ts';
import { formatDate, t } from '../../shared/i18n/index.ts';
import { questionsKey, refreshInbox, withoutSeen } from './inboxPages.ts';
import { isMediaUnseen, setMediaUnseen } from './mediaPages.ts';

const DAY = 24 * 60 * 60 * 1000;

// noticeVerb is what the agent did, after its name: it asks you, or stopped
// on an error (a notice the app makes itself, from the agent's state).
export const noticeVerb = (n: Pick<T.Notification, 'kind'>): string => t(n.kind === 'error' ? 'shell.notice.verb.error' : 'shell.notice.verb.question');

// noticeText is an OS notification's: its title names the agent, and its body
// the text and where it's from, since the OS shows neither the project nor
// the agent's title on its own.
export function noticeText(n: T.Notification, projectName: string): { title: string; body: string } {
  const title = `${n.agent} ${noticeVerb(n)}`;
  const from = [projectName, n.title].filter(Boolean).join(' · ');
  return { title, body: [n.text, from].filter(Boolean).join('\n') };
}

// noticeView is the page a notification opens: its project's chat, where
// a question is answered.
export function noticeView(n: T.Notification): View {
  return { kind: 'project', project: n.project, tab: 'chat' };
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
  applySeen(queryClient, req);
  return api.seeNotifications(req).catch(() => refreshInbox(queryClient));
}

// applySeen marks what a SeeNotificationsRequest names seen in the cache: the
// questions leave the unseen ones, and the media items it's about lose their
// mark in the Media lists.
export function applySeen(queryClient: QueryClient, req: T.SeeNotificationsRequest): void {
  queryClient.setQueryData<InfiniteData<T.NotificationPage, string>>(questionsKey, (data) => withoutSeen(data, req));
  if (!req.all && !req.allMedia && !req.media?.length) return;
  setMediaUnseen(queryClient, false, new Set(req.media ?? []), req.all || req.allMedia);
  void queryClient.invalidateQueries({ queryKey: ['mediaCounts'] });
}

export function useMarkSeen(): (req: T.SeeNotificationsRequest) => Promise<unknown> {
  const queryClient = useQueryClient();
  return (req) => markSeen(queryClient, req);
}

// useSeeMedia marks a media item seen when it's opened anywhere, if the Media
// lists show it unseen; otherwise it costs nothing.
export function useSeeMedia(): (id: string | null) => void {
  const queryClient = useQueryClient();
  return (id) => {
    if (id && isMediaUnseen(queryClient, id)) void markSeen(queryClient, { media: [id] });
  };
}
