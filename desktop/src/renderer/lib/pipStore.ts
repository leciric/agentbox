// The dock of each lead's chat, as you left it (lib/pip.ts): hidden or not,
// stacked or not, and where you floated which agents' tiles. Kept between
// sessions; the tile grown over the chat is not.
import { useSyncExternalStore } from 'react';
import type { Spot } from './pip';

export interface Dock {
  hidden: boolean;
  stacked: boolean;
  floats: Record<string, Spot>; // by agent ref
  big: string | null; // an agent ref, this session only
}

const storageKey = 'agentbox.ui.v2.pip';
const empty: Dock = { hidden: false, stacked: false, floats: {}, big: null };
const listeners = new Set<() => void>();
let docks: Record<string, Dock> | undefined;

function load(): Record<string, Dock> {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Record<string, Partial<Dock>>;
    const out: Record<string, Dock> = {};
    for (const [project, d] of Object.entries(saved)) {
      const floats: Record<string, Spot> = {};
      for (const [ref, at] of Object.entries(d.floats ?? {})) if (typeof at?.x === 'number' && typeof at?.y === 'number') floats[ref] = { x: at.x, y: at.y };
      out[project] = { hidden: d.hidden === true, stacked: d.stacked === true, floats, big: null };
    }
    return out;
  } catch {
    return {};
  }
}

const all = () => (docks ??= load());
export const dockOf = (project: string): Dock => all()[project] ?? empty;

export function setDock(project: string, change: (d: Dock) => Partial<Dock>): void {
  const d = dockOf(project);
  docks = { ...all(), [project]: { ...d, ...change(d) } };
  try {
    const kept = Object.fromEntries(Object.entries(docks).map(([p, { hidden, stacked, floats }]) => [p, { hidden, stacked, floats }]));
    localStorage.setItem(storageKey, JSON.stringify(kept));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

// unfloat is d's floats without ref's.
export const unfloat = (d: Dock, ref: string) => Object.fromEntries(Object.entries(d.floats).filter(([r]) => r !== ref));

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useDock(project: string): Dock {
  return useSyncExternalStore(subscribe, () => dockOf(project));
}

// today is the Today gallery, open on a project's captures (components/pip),
// filtered to an agent's and on one of them when a mention opened it.
export type Today = { project: string; agent?: string; item?: string } | null;
let today: Today = null;
export function openToday(next: Today): void {
  today = next;
  for (const l of listeners) l();
}
export const useToday = (): Today => useSyncExternalStore(subscribe, () => today);
