import { keepPreviousData, useInfiniteQuery, useQuery, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import type * as T from '../../shared/api';
import { api } from './api.ts';
import { searchMedia } from './media.ts';

// The Media views (an agent's, a project's and every project's) read their
// items a page at a time from GET /v1/media, which filters and searches them,
// newest first; the counts on their filters come from GET /v1/media/counts,
// which counts every item rather than the pages loaded. The daemon's media
// events keep both current without reading the pages again: a new item goes
// in where it sorts, a changed one is replaced, a deleted one taken out.

export const mediaPageSize = 40;

// MediaScope is one Media list: whose items, and what its filters keep.
export interface MediaScope {
  project?: string;
  agent?: string; // an agent's name, in project
  kinds?: string[]; // what the view shows at all
  only?: string; // one kind of those, a filter chip
  favorite?: boolean;
  unseen?: boolean;
  q?: string; // the search
}

export type MediaPages = InfiniteData<T.MediaPage, string>;

// scopeKey is a scope with what doesn't narrow anything left out, so two
// lists asking for the same items share a cache.
export function scopeKey(scope: MediaScope): MediaScope {
  const out: MediaScope = {};
  if (scope.project) out.project = scope.project;
  if (scope.project && scope.agent) out.agent = scope.agent;
  if (scope.kinds?.length) out.kinds = scope.kinds;
  if (scope.only) out.only = scope.only;
  if (scope.favorite) out.favorite = true;
  if (scope.unseen) out.unseen = true;
  if (scope.q?.trim()) out.q = scope.q.trim();
  return out;
}

export function mediaQuery(scope: MediaScope, cursor = ''): URLSearchParams {
  const s = scopeKey(scope);
  const q = new URLSearchParams();
  if (s.project) q.set('project', s.project);
  if (s.agent) q.set('agent', s.agent);
  if (s.kinds) q.set('kind', s.kinds.join(','));
  if (s.only) q.set('only', s.only);
  if (s.favorite) q.set('favorite', '1');
  if (s.unseen) q.set('unseen', '1');
  if (s.q) q.set('q', s.q);
  if (cursor) q.set('cursor', cursor);
  return q;
}

// useMediaPages is a Media list, loaded a page at a time: fetchNextPage reads
// the next. It isn't refetched on a timer: the media events keep it current.
export function useMediaPages(scope: MediaScope) {
  return useInfiniteQuery({
    queryKey: ['mediaPages', scopeKey(scope)],
    queryFn: ({ pageParam }) => {
      const q = mediaQuery(scope, pageParam);
      q.set('limit', String(mediaPageSize));
      return api.mediaPage(q);
    },
    initialPageParam: '',
    getNextPageParam: (last) => last.next || undefined,
    staleTime: Infinity,
    // A new filter or search keeps the last one's items until its own come.
    placeholderData: keepPreviousData,
  });
}

// useNextPageNear reads the next page when the viewer, open on item index of
// count loaded, nears their end, so Next goes on past what's been scrolled to.
export function useNextPageNear(pages: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => Promise<unknown> }, index: number, count: number): void {
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages;
  useEffect(() => {
    if (index >= 0 && index >= count - 3 && hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [index, count, hasNextPage, isFetchingNextPage, fetchNextPage]);
}

// useMediaCounts is the counts on a Media list's filters.
export function useMediaCounts(scope: MediaScope) {
  return useQuery({ queryKey: ['mediaCounts', scopeKey(scope)], queryFn: () => api.mediaCounts(mediaQuery(scope)), placeholderData: keepPreviousData });
}

// inScope is whether a list's filters keep an item. Unseen isn't asked: an
// item becomes unseen after it arrives, when its notification does, and the
// views hide what isn't unseen themselves.
export function inScope(scope: MediaScope, item: T.MediaItem): boolean {
  const [project, agent] = item.agent.split('/');
  return (
    (!scope.project || scope.project === project) &&
    (!scope.agent || scope.agent === agent) &&
    (!scope.kinds?.length || scope.kinds.includes(item.kind)) &&
    (!scope.only || scope.only === item.kind) &&
    (!scope.favorite || item.favorite === true) &&
    searchMedia([item], scope.q ?? '').length > 0
  );
}

// newer is the lists' order: newest first, then by ID, as the daemon pages.
function newer(a: T.MediaItem, b: T.MediaItem): boolean {
  const at = Date.parse(a.createdAt) - Date.parse(b.createdAt);
  return at > 0 || (at === 0 && a.id > b.id);
}

function mapItems(data: MediaPages, fn: (items: T.MediaItem[]) => T.MediaItem[]): MediaPages {
  return { ...data, pages: data.pages.map((page) => ({ ...page, items: fn(page.items) })) };
}

// withMediaItem is a list's pages once an item from a media event is in
// them: taken out when it was deleted, replaced when it changed, and put
// where it sorts when it's new and the list keeps it, if the pages loaded
// reach that far (an older item comes with its own page).
export function withMediaItem(data: MediaPages, scope: MediaScope, item: T.MediaItem): MediaPages {
  const has = data.pages.some((page) => page.items.some((i) => i.id === item.id));
  if (item.removed) return has ? mapItems(data, (items) => items.filter((i) => i.id !== item.id)) : data;
  if (has) return mapItems(data, (items) => items.map((i) => (i.id === item.id ? { ...item, unseen: item.unseen || i.unseen } : i)));
  if (!inScope(scope, item)) return data;
  const pages = data.pages;
  for (let p = 0; p < pages.length; p++) {
    const at = pages[p].items.findIndex((i) => newer(item, i));
    if (at >= 0) {
      const items = [...pages[p].items.slice(0, at), item, ...pages[p].items.slice(at)];
      return { ...data, pages: pages.map((page, i) => (i === p ? { ...page, items } : page)) };
    }
  }
  const last = pages.at(-1);
  if (!last || last.next) return data; // older than what's loaded
  return { ...data, pages: [...pages.slice(0, -1), { ...last, items: [...last.items, item] }] };
}

// eachMediaList calls fn with every Media list in the cache, and stores what
// it returns.
function eachMediaList(queryClient: QueryClient, fn: (data: MediaPages, scope: MediaScope) => MediaPages): void {
  for (const [key, data] of queryClient.getQueriesData<MediaPages>({ queryKey: ['mediaPages'] })) {
    if (!data) continue;
    const next = fn(data, key[1] as MediaScope);
    if (next !== data) queryClient.setQueryData(key, next);
  }
}

// applyMediaEvent puts a media event's item into every list, and has the
// counts read again (one small request each, not the pages).
export function applyMediaEvent(queryClient: QueryClient, item: T.MediaItem): void {
  eachMediaList(queryClient, (data, scope) => withMediaItem(data, scope, item));
  void queryClient.invalidateQueries({ queryKey: ['mediaCounts'] });
}

// setMediaUnseen marks items unseen, or seen, in every list: those whose
// IDs seen says, or, with all, every one.
export function setMediaUnseen(queryClient: QueryClient, unseen: boolean, ids: ReadonlySet<string>, all = false): void {
  eachMediaList(queryClient, (data) => {
    const hit = (m: T.MediaItem) => (all || ids.has(m.id)) && (m.unseen ?? false) !== unseen;
    if (!data.pages.some((page) => page.items.some(hit))) return data;
    return mapItems(data, (items) => items.map((m) => (hit(m) ? { ...m, unseen } : m)));
  });
  void queryClient.invalidateQueries({ queryKey: ['mediaCounts'] });
}

// isMediaUnseen is whether any list shows an item as unseen.
export function isMediaUnseen(queryClient: QueryClient, id: string): boolean {
  return queryClient
    .getQueriesData<MediaPages>({ queryKey: ['mediaPages'] })
    .some(([, data]) => data?.pages.some((page) => page.items.some((m) => m.id === id && m.unseen)));
}

// loadedItems is a list's pages as one list.
export function loadedItems(data: InfiniteData<T.MediaPage, unknown> | undefined): T.MediaItem[] {
  return data?.pages.flatMap((page) => page.items) ?? [];
}
