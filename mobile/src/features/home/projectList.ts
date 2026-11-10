// The projects list's search and order, apart from the screen so they can be
// tested: a query matches a project's name as typed, and "recent activity"
// puts the projects needing you first, then the ones whose lead or agents
// answered last.
import type * as T from '../../shared/api.ts';

export type ProjectOrder = 'default' | 'recent';

export const projectOrders: ProjectOrder[] = ['default', 'recent'];

// fold drops case and accents, so "projeto" finds "Projéto".
const fold = (s: string) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();

export function matchesQuery(p: Pick<T.Project, 'name' | 'displayName'>, query: string): boolean {
  const q = fold(query.trim());
  return !q || fold(p.displayName).includes(q) || fold(p.name).includes(q);
}

// lastReply is when a chat last answered: its newest assistant message, the
// only kind peekThread is sure to hold.
export function lastReply(thread: T.ChatThread | undefined): number {
  let at = 0;
  for (const it of thread?.items ?? []) {
    if (it.kind !== 'assistant' || it.parent || it.hidden) continue;
    at = Math.max(at, Date.parse(it.updatedAt || it.createdAt) || 0);
  }
  return at;
}

export interface ProjectEntry {
  needYou: number;
  // A time in milliseconds; 0 when no chat of the project has answered.
  lastActivity: number;
}

// orderProjects keeps the daemon's order for 'default'; for 'recent', those
// needing you come first, then the latest to answer. Ties keep the daemon's
// order, so the list doesn't shuffle between refreshes.
export function orderProjects<E extends ProjectEntry>(entries: E[], order: ProjectOrder): E[] {
  if (order === 'default') return entries;
  return entries
    .map((e, i) => ({ e, i }))
    .sort((x, y) => Number(y.e.needYou > 0) - Number(x.e.needYou > 0) || y.e.lastActivity - x.e.lastActivity || x.i - y.i)
    .map(({ e }) => e);
}
