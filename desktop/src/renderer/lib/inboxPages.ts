// The inbox's lists (components/Inbox.tsx) are read a page at a time from the
// daemon, as the Media views are (lib/mediaPages.ts): the questions waiting
// for an answer, the agents ready to review, and the reviewed ones when that
// section is open. Each page says how many the whole list holds, so a badge
// counts them all and not the pages loaded. The daemon's events (lib/events.ts)
// keep them current: a question that arrives goes on top, one seen or an agent
// reviewed is taken out, without reading the pages again.
import { keepPreviousData, useInfiniteQuery, useQuery, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { api } from './api.ts';

export const inboxPageSize = 20;
// The newest finishes the alert for "newly ready" watches: the most a page holds.
const watchSize = 200;

export const questionsKey = ['notifications', 'unseen'] as const;
export const reviewPagesKey = ['review', 'pages'] as const;
export const reviewedPagesKey = ['reviewed', 'pages'] as const;
export const reviewHeadKey = ['review', 'head'] as const;

type Pages<V extends { items: unknown[]; total: number }> = InfiniteData<V, string>;

const query = (size: number, cursor = '') => {
  const q = new URLSearchParams({ limit: String(size) });
  if (cursor) q.set('cursor', cursor);
  return q;
};

// useQuestions is the questions not seen yet, newest first.
export function useQuestions() {
  return useInfiniteQuery({
    queryKey: questionsKey,
    queryFn: ({ pageParam }) => {
      const q = query(inboxPageSize, pageParam);
      q.set('unseen', '1');
      return api.notificationPage(q);
    },
    initialPageParam: '',
    getNextPageParam: (last) => last.next || undefined,
    staleTime: Infinity,
  });
}

// useReviewPages is what's ready to review, or with reviewed the agents
// already reviewed; the Reviewed section reads only once it's open.
export function useReviewPages(reviewed: boolean, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: reviewed ? reviewedPagesKey : reviewPagesKey,
    queryFn: ({ pageParam }) => (reviewed ? api.reviewedPage(query(inboxPageSize, pageParam)) : api.readyToReviewPage(query(inboxPageSize, pageParam))),
    initialPageParam: '',
    getNextPageParam: (last) => last.next || undefined,
    // The events keep it current; the poll is a backstop, and what notices an
    // agent that went back to work.
    refetchInterval: reviewed ? false : 60_000,
    enabled,
    placeholderData: keepPreviousData,
  });
}

// useReviewHead is the newest agents ready to review and how many there are
// in all, whether or not the inbox is open: the badge counts them and the
// alert (lib/useReadyAlerts.ts) watches them for a new one. It reads the
// biggest page there is, so an agent moving up the list as others are
// reviewed isn't mistaken for one that just arrived.
export function useReviewHead(enabled: boolean) {
  return useQuery({ queryKey: reviewHeadKey, queryFn: () => api.readyToReviewPage(query(watchSize)), refetchInterval: 60_000, enabled });
}

export function pageItems<P extends { items: unknown[] }>(data: InfiniteData<P, unknown> | undefined): P['items'] {
  return (data?.pages.flatMap((p) => p.items) ?? []) as P['items'];
}

// pageTotal is how many a list holds in all, as its newest page says.
export const pageTotal = (data: InfiniteData<{ total: number }, unknown> | undefined): number => data?.pages[0]?.total ?? 0;

const isPages = <P>(data: unknown): data is InfiniteData<P, unknown> => !!data && typeof data === 'object' && 'pages' in data;

// withoutAgent is a review list, a page or pages of it, with one agent taken
// off, as a verdict does before the daemon's answer comes back.
export function withoutAgent<D extends T.ReviewPage | Pages<T.ReviewPage> | undefined>(data: D, ref: string): D {
  const drop = (page: T.ReviewPage): T.ReviewPage => ({ ...page, items: page.items.filter((i) => i.ref !== ref), total: Math.max(0, page.total - 1) });
  const has = (page: T.ReviewPage) => page.items.some((i) => i.ref === ref);
  if (!data) return data;
  if (isPages<T.ReviewPage>(data)) return (data.pages.some(has) ? { ...data, pages: data.pages.map(drop) } : data) as D;
  return ((data as T.ReviewPage).items.some((i) => i.ref === ref) ? drop(data as T.ReviewPage) : data) as D;
}

// withQuestion puts a question that just arrived on top of the unseen ones.
export function withQuestion(data: Pages<T.NotificationPage> | undefined, n: T.Notification): Pages<T.NotificationPage> | undefined {
  if (!data || data.pages.some((p) => p.items.some((x) => x.id === n.id))) return data;
  const [first, ...rest] = data.pages;
  return { ...data, pages: [{ ...first, items: [n, ...first.items], total: first.total + 1 }, ...rest.map((p) => ({ ...p, total: p.total + 1 }))] };
}

// withoutSeen takes the questions a SeeNotificationsRequest names off the
// unseen ones.
export function withoutSeen(data: Pages<T.NotificationPage> | undefined, req: T.SeeNotificationsRequest): Pages<T.NotificationPage> | undefined {
  if (!data) return data;
  const ids = new Set(req.ids ?? []);
  const hit = (n: T.Notification) => req.all || req.kind === n.kind || ids.has(n.id);
  const gone = data.pages.reduce((sum, p) => sum + p.items.filter(hit).length, 0);
  // A request by kind may see more than the pages loaded hold.
  const everything = req.all || req.kind === 'question';
  if (!gone && !everything) return data;
  return {
    ...data,
    pages: data.pages.map((p) => ({ ...p, items: p.items.filter((n) => !hit(n)), total: everything ? 0 : Math.max(0, p.total - gone) })),
  };
}

// refresh reads the inbox's lists again.
export function refreshInbox(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: ['notifications'] });
  void queryClient.invalidateQueries({ queryKey: ['review'] });
  void queryClient.invalidateQueries({ queryKey: ['reviewed'] });
}
