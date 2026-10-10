// A project's Hatch pages: those its chats published on Hatch, read by the
// daemon out of those chats (internal/hatch, which calls them artifacts). One
// query per project holds them all; the lead's chat shows every one, an
// agent's chat those it made or updated. A page Hatch has let expire is gone
// for good, so no list shows it: only a link to it from before still opens
// its preview, which says so.
import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import * as T from '../../shared/api.ts';

export const pagesKey = (project: string) => ['pages', project] as const;

// isLeadRef is a chat ref that is a project's chat, its lead's.
export const isLeadRef = (ref: string) => {
  const [, name] = ref.split('/');
  return !name || name === T.LeadName;
};

// isExpired is a page Hatch no longer has, by what the daemon read of it.
export function isExpired(a: Pick<T.Artifact, 'expiresAt' | 'permanent' | 'expired'>, now = Date.now()): boolean {
  if (a.expired) return true;
  if (a.permanent || !a.expiresAt) return false;
  return new Date(a.expiresAt).getTime() <= now;
}

// livePages is every page of a project Hatch still has, newest first, and
// nothing without a Hatch connector.
export function livePages(list: T.Artifacts | undefined, now = Date.now()): T.Artifact[] {
  if (!list?.connector) return [];
  return list.artifacts.filter((a) => !isExpired(a, now));
}

// allLivePages is every project's live pages, each with its project, for
// the all-projects Media view: nothing for a project without Hatch.
export function allLivePages(lists: [string, T.Artifacts | undefined][], now = Date.now()): PageAt[] {
  return lists.flatMap(([project, list]) => livePages(list, now).map((page) => ({ project, page })));
}

// pagesFor is what the chat of ref shows: all of them in the project's chat,
// an agent's own in its.
export function pagesFor(list: T.Artifacts | undefined, ref: string, now = Date.now()): T.Artifact[] {
  const live = livePages(list, now);
  return isLeadRef(ref) ? live : live.filter((a) => a.agents.includes(ref));
}

// matchesPage is a page a search finds: every word in its title or in the
// name of the agent that made it, and in its project's names when given
// (global Media, where pages of every project are mixed).
export function matchesPage(a: T.Artifact, query: string, agentLabel = '', project = ''): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const hay = `${a.title} ${a.agent.split('/')[1] ?? ''} ${agentLabel} ${project}`.toLowerCase();
  return words.every((w) => hay.includes(w));
}

// byAgent groups pages under the agent that published each, the group with
// the newest page first, each group newest first.
export function byAgent(list: T.Artifact[]): [string, T.Artifact[]][] {
  const groups = new Map<string, T.Artifact[]>();
  const newest = (a: T.Artifact) => new Date(a.updatedAt).getTime();
  for (const a of [...list].sort((x, y) => newest(y) - newest(x))) groups.set(a.agent, [...(groups.get(a.agent) ?? []), a]);
  return [...groups];
}

// countByAgent is how many live pages each agent published, for its row in
// the Agents list.
export function countByAgent(list: T.Artifact[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const a of list) out.set(a.agent, (out.get(a.agent) ?? 0) + 1);
  return out;
}

// countByProject is how many pages each project has, in the order projects
// first appear in the list, for the project chips of global Media's Pages.
export function countByProject(list: readonly { project: string }[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const e of list) out.set(e.project, (out.get(e.project) ?? 0) + 1);
  return out;
}

// The two Hatch calls that make or change a page, however the AI tool names
// them (internal/hatch.Op).
const pageTool = /(?:^|[^a-z0-9])(publish_page|update_page)$/;
export const isPageCall = (tool: Pick<T.ChatTool, 'name' | 'title'> | undefined): boolean => !!tool && [tool.name, tool.title].some((s) => !!s && pageTool.test(s.trim().toLowerCase()));

// pageIdOf is the page a finished publish_page or update_page made or
// changed: the id it was called with, or the one its answer links to.
const pageLink = /https?:\/\/[^\s"'<>()\\]+\/p\/([A-Za-z0-9_-]+)/;
export function pageIdOf(tool: T.ChatTool | undefined): string | undefined {
  if (!tool || tool.status !== 'completed' || !isPageCall(tool)) return undefined;
  return tool.page?.id || pageLink.exec(tool.output ?? '')?.[1] || undefined;
}

// readPages reads a project's pages with fetch, taking them all as seen the
// first time this browser reads them (knowProject), before anything shows
// them, so they don't flash up as new.
export const readPages = (project: string, fetch: (project: string) => Promise<T.Artifacts>) => async (): Promise<T.Artifacts> => {
  const list = await fetch(project);
  knowProject(project, list);
  return list;
};

// onChatItem reads a project's pages again when one of its chats finished
// a call to publish_page or update_page, whether or not anything on screen
// shows them: a new page is told about wherever you are (PageArrivals).
export function onChatItem(queryClient: QueryClient, chatRef: string, item: T.ChatItem, fetch: (project: string) => Promise<T.Artifacts>): void {
  if (item.kind !== 'tool' || item.tool?.status !== 'completed' || !isPageCall(item.tool)) return;
  const project = chatRef.split('/')[0];
  void queryClient.fetchQuery({ queryKey: pagesKey(project), queryFn: readPages(project, fetch), staleTime: 0 }).catch(() => {});
}

// Which version of each page you have seen, kept in this browser: a page
// is new until you open it, and new again when an agent updates it. The
// first time a project's pages are read here they're all taken as seen, so
// a project that published pages before this is installed doesn't light up.
type Seen = { pages: Record<string, number>; projects: string[] };
const seenKey = 'agentbox.pages.seen';
let seen: Seen = load();
let seenVersion = 0;
const listeners = new Set<() => void>();

function load(): Seen {
  try {
    const raw = globalThis.localStorage?.getItem(seenKey);
    const parsed = raw ? (JSON.parse(raw) as Partial<Seen>) : {};
    return { pages: parsed.pages ?? {}, projects: parsed.projects ?? [] };
  } catch {
    return { pages: {}, projects: [] };
  }
}

function save(next: Seen): void {
  seen = next;
  seenVersion++;
  try {
    globalThis.localStorage?.setItem(seenKey, JSON.stringify(next));
  } catch {
    // Full or unavailable: what's seen lasts until the window closes.
  }
  for (const fn of listeners) fn();
}

// isNew is a page you haven't opened at its current version.
export const isNew = (a: Pick<T.Artifact, 'id' | 'version'>): boolean => (seen.pages[a.id] ?? 0) < a.version;

// markSeen takes pages as seen at their current versions.
export function markSeen(list: Pick<T.Artifact, 'id' | 'version'>[]): void {
  const pages = { ...seen.pages };
  let changed = false;
  for (const a of list) {
    if ((pages[a.id] ?? 0) >= a.version) continue;
    pages[a.id] = a.version;
    changed = true;
  }
  if (changed) save({ ...seen, pages });
}

// knowProject takes a project's pages as seen the first time they're read
// here, and reports whether it did.
export function knowProject(project: string, list: T.Artifacts | undefined): boolean {
  if (!list || seen.projects.includes(project)) return false;
  const pages = { ...seen.pages };
  for (const a of list.artifacts) pages[a.id] = Math.max(pages[a.id] ?? 0, a.version);
  save({ pages, projects: [...seen.projects, project] });
  return true;
}

// resetSeen forgets every page seen, for tests.
export function resetSeen(): void {
  save({ pages: {}, projects: [] });
}

// useSeenPages re-renders when a page is seen; it's a version number.
export function useSeenPages(): number {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => seenVersion,
  );
}

// stackPages is what a chat's stack holds: the pages it shows that you
// haven't opened since they were published or updated, newest first.
export function stackPages(list: T.Artifacts | undefined, ref: string, now = Date.now()): T.Artifact[] {
  return pagesFor(list, ref, now)
    .filter(isNew)
    .sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime());
}

// livePageOf is the page a publish_page or update_page line opens: one the
// project still has, or none, and then the line is a line like any other.
export function livePageOf(list: T.Artifacts | undefined, tool: T.ChatTool | undefined, now = Date.now()): T.Artifact | undefined {
  const id = pageIdOf(tool);
  return id ? livePages(list, now).find((a) => a.id === id) : undefined;
}

// showsArrival is whether a page that just arrived is told about with a
// toast: not while a chat whose stack shows it is on screen.
export function showsArrival(page: T.Artifact, onScreen: Iterable<string>): boolean {
  const project = page.agent.split('/')[0];
  for (const chat of onScreen) {
    if (chat.split('/')[0] !== project) continue;
    if (isLeadRef(chat) || page.agents.includes(chat)) return false;
  }
  return true;
}

// The chats on screen, each counted while its stack is mounted.
const chatsOnScreen = new Map<string, number>();
export function watchChat(ref: string): () => void {
  chatsOnScreen.set(ref, (chatsOnScreen.get(ref) ?? 0) + 1);
  return () => {
    const n = (chatsOnScreen.get(ref) ?? 1) - 1;
    if (n > 0) chatsOnScreen.set(ref, n);
    else chatsOnScreen.delete(ref);
  };
}
export const onScreenChats = (): string[] => [...chatsOnScreen.keys()];

// arrivals is what's new between two reads of a project's pages: a page that
// wasn't there, or is at a later version, and that you haven't seen.
export function arrivals(before: T.Artifacts | undefined, after: T.Artifacts | undefined, now = Date.now()): T.Artifact[] {
  if (!before || !after?.connector) return [];
  const was = new Map(before.artifacts.map((a) => [a.id, a.version]));
  return livePages(after, now).filter((a) => (was.get(a.id) ?? 0) < a.version && isNew(a));
}

// PageAt is a page of a project, for opening it from anywhere.
export type PageAt = { project: string; page: T.Artifact };

// Opening a page: the stack, a toast, the Pages tab, a Media view and a
// publish_page line all ask for it, and App's PagePreviewHost shows it.
let opened: PageAt | null = null;
const openListeners = new Set<() => void>();

export function openPage(project: string, page: T.Artifact): void {
  opened = { project, page };
  markSeen([page]);
  for (const fn of openListeners) fn();
}

export function closePage(): void {
  opened = null;
  for (const fn of openListeners) fn();
}

export function useOpenedPage(): PageAt | null {
  return useSyncExternalStore(
    (fn) => {
      openListeners.add(fn);
      return () => openListeners.delete(fn);
    },
    () => opened,
  );
}

// pageFromCall is what a publish_page line knows of its page when the
// project's list doesn't have it (yet): enough for the preview to ask Hatch.
export function pageFromCall(item: T.ChatItem, chatRef: string, id: string): T.Artifact {
  return {
    id,
    title: item.tool?.page?.title || id,
    url: pageLink.exec(item.tool?.output ?? '')?.[0] ?? '',
    agent: chatRef,
    item: item.id,
    agents: [chatRef],
    version: 1,
    createdAt: item.createdAt,
    updatedAt: item.updatedAt,
  };
}

// thumbKey is a thumbnail's cache key: a version of a page never changes.
export const thumbKey = (a: Pick<T.Artifact, 'id' | 'version'>) => `${a.id}@${a.version}`;

// tileHue is a page's colour on the tile drawn when its HTML can't be had,
// the same for the same page every time.
export function tileHue(id: string): number {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return h % 360;
}
