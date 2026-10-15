// What UI v2's sidebar remembers about the projects on this computer: which
// are pinned and in what order, the sort switch, whether the Idle list is
// open, and the state and project filters (none until the user picks).
// v2 is how this app looks here (uiV2.ts), so these live in localStorage
// beside it, not in the daemon, and v1's order (D79) never sees them. The
// reading of them is projectOrder.ts's.
import { useSyncExternalStore } from 'react';
import { noFilters, stOrder, type AgentSt } from './leadFirst.ts';
import { defaultSort, isSortBy, type SortBy } from './projectOrder.ts';

type V2Projects = {
  pins: string[];
  sort: SortBy;
  idleOpen: boolean;
  st: AgentSt[];
  projects: string[];
};

const strings = (x: unknown): string[] | undefined => (Array.isArray(x) ? x.filter((p): p is string => typeof p === 'string') : undefined);

const storageKey = 'agentbox.v2.projects';
const listeners = new Set<() => void>();
let current: V2Projects | undefined;

function read(): V2Projects {
  const empty: V2Projects = { pins: [], sort: defaultSort, idleOpen: false, st: noFilters.st, projects: [] };
  try {
    const raw = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<V2Projects>;
    return {
      pins: strings(raw.pins) ?? [],
      sort: isSortBy(raw.sort) ? raw.sort : defaultSort,
      idleOpen: raw.idleOpen === true,
      st: strings(raw.st)?.filter((st): st is AgentSt => stOrder.includes(st as AgentSt)) ?? empty.st,
      projects: strings(raw.projects) ?? [],
    };
  } catch {
    return empty;
  }
}

export function v2Projects(): V2Projects {
  current ??= read();
  return current;
}

function update(change: (s: V2Projects) => Partial<V2Projects>) {
  const prev = v2Projects();
  current = { ...prev, ...change(prev) };
  try {
    localStorage.setItem(storageKey, JSON.stringify(current));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const listener of listeners) listener();
}

export const setPins = (pins: string[]) => update(() => ({ pins }));
export const setSort = (sort: SortBy) => update(() => ({ sort }));
export const setIdleOpen = (idleOpen: boolean) => update(() => ({ idleOpen }));
export const setFilterChoice = (st: AgentSt[], projects: string[]) => update(() => ({ st, projects }));

export function useV2Projects(): V2Projects {
  return useSyncExternalStore((listener) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  }, v2Projects);
}
