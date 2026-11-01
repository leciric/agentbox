// The one Media view's filters (components/media/MediaView): whose work it
// shows, an agent's, a project's or every project's, and which kind of it,
// the media the daemon keeps or the pages Hatch has. Every place that shows
// media opens that view, each on its own filter: an agent's Media tab on the
// agent, a project's on the project, the sidebar's Media on everything, and
// the rail's Pages on the project's pages.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';
import { isNew, matchesPage, type PageAt } from './pages.ts';

// pageKind is the kind chip for pages, beside the media's own kinds.
export const pageKind = 'page';

export interface MediaFilter {
  project: string; // '' for every project
  agent: string; // an agent's name in project, '' for all of them
  kind: string; // a media kind, pageKind, or '' for everything
  unseen: boolean;
  favorite: boolean;
}

export type MediaScopeName = 'agent' | 'project' | 'all';

// MediaHome is where a view was opened: the agent of an agent's tab, the
// project of a project's. Its scope buttons go back to them.
export interface MediaHome {
  project?: string;
  agent?: string;
}

export const noFilter: MediaFilter = { project: '', agent: '', kind: '', unseen: false, favorite: false };

// scopeOf is how wide a filter reaches.
export function scopeOf(f: Pick<MediaFilter, 'project' | 'agent'>): MediaScopeName {
  if (f.project && f.agent) return 'agent';
  return f.project ? 'project' : 'all';
}

// scopesOf is the scope buttons a view has: the agent only from an agent's
// tab, the project from inside one, and everything always.
export function scopesOf(home: MediaHome): MediaScopeName[] {
  if (home.project && home.agent) return ['agent', 'project', 'all'];
  return home.project ? ['project', 'all'] : ['all'];
}

// withScope is a filter widened or narrowed to scope, from home: the kind
// and the toggles stay.
export function withScope(f: MediaFilter, scope: MediaScopeName, home: MediaHome): MediaFilter {
  switch (scope) {
    case 'agent':
      return { ...f, project: home.project ?? '', agent: home.agent ?? '' };
    case 'project':
      return { ...f, project: home.project || f.project, agent: '' };
    default:
      return { ...f, project: '', agent: '' };
  }
}

// atHome is whether a filter is the scope's own: the agent's tab on its
// agent, so its buttons (screenshot, record, note, export) belong.
export function atHome(f: MediaFilter, scope: MediaScopeName, home: MediaHome): boolean {
  const own = withScope(f, scope, home);
  return own.project === f.project && own.agent === f.agent;
}

// showsMedia and showsPages are whether a filter keeps any of each: pages
// are never favorites, and the media have no page kind.
export const showsMedia = (f: MediaFilter) => f.kind !== pageKind;
export const showsPages = (f: MediaFilter) => (f.kind === '' || f.kind === pageKind) && !f.favorite;

// pagesIn is the pages a filter keeps, newest first: the project's (or
// every project's), those an agent published or updated when one is
// chosen, the new ones only for Unseen, and those a search finds.
export function pagesIn(entries: readonly PageAt[], f: MediaFilter, query: string, label: (ref: string) => string = () => '', projectLabel: (name: string) => string = () => ''): PageAt[] {
  if (!showsPages(f)) return [];
  return entries
    .filter(
      (e) =>
        (!f.project || e.project === f.project) &&
        (!f.agent || e.page.agents.includes(`${e.project}/${f.agent}`)) &&
        (!f.unseen || isNew(e.page)) &&
        matchesPage(e.page, query, label(e.page.agent), f.project ? '' : `${e.project} ${projectLabel(e.project)}`),
    )
    .toSorted((a, b) => b.page.updatedAt.localeCompare(a.page.updatedAt));
}

export type Entry = { at: string; item: T.MediaItem; page?: undefined; project?: undefined } | { at: string; page: T.Artifact; project: string; item?: undefined };

// interleave is the media and the pages as one list, newest first. The
// media come a page at a time: until the last has, a Hatch page older than
// the oldest item read waits for it, so scrolling down never finds one
// slotted in above what was already passed.
export function interleave(items: readonly T.MediaItem[], pages: readonly PageAt[], complete: boolean): Entry[] {
  const oldest = items.at(-1)?.createdAt;
  const kept = complete || !oldest ? pages : pages.filter((p) => p.page.updatedAt >= oldest);
  const out: Entry[] = [...items.map((item) => ({ at: item.createdAt, item })), ...kept.map(({ project, page }) => ({ at: page.updatedAt, project, page }))];
  return out.toSorted((a, b) => b.at.localeCompare(a.at));
}

// A request to open the Media view on a filter: made by a link before the
// view it goes to exists, read by that view as it mounts, or as it comes in
// while it's already on screen. Too old a request is nobody's.
const waitMs = 6_000;
let pending: (Partial<MediaFilter> & { at: number }) | null = null;
const listeners = new Set<() => void>();

export function requestMediaFilter(f: Partial<MediaFilter>): void {
  pending = { ...f, at: Date.now() };
  for (const listener of listeners) listener();
}

// peekMediaFilter is the request waiting, left for takeMediaFilter: what a
// view starts on, read while it renders.
export function peekMediaFilter(now = Date.now()): Partial<MediaFilter> | null {
  if (!pending || now - pending.at > waitMs) return null;
  const { at: _, ...f } = pending;
  return f;
}

// takeMediaFilter is the request waiting, once: it's gone after.
export function takeMediaFilter(now = Date.now()): Partial<MediaFilter> | null {
  const f = peekMediaFilter(now);
  pending = null;
  return f;
}

export function useMediaFilterRequests(): number {
  return useSyncExternalStore(subscribe, () => pending?.at ?? 0);
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
