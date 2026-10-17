// The desktops lane of each lead's chat, as you left it (components/pip):
// folded or not, kept between sessions, and the desktop grown in it, this
// session only.
import { useSyncExternalStore } from 'react';

export interface Lane {
  // folded is as you set it, or undefined to fold only when the chat is too
  // narrow to give up the room.
  folded?: boolean;
  big: string | null; // an agent ref
}

const storageKey = 'agentbox.ui.v2.pip';
const empty: Lane = { big: null };
const listeners = new Set<() => void>();
let lanes: Record<string, Lane> | undefined;

function load(): Record<string, Lane> {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Record<string, Partial<Lane>>;
    const out: Record<string, Lane> = {};
    for (const [project, l] of Object.entries(saved)) out[project] = { folded: typeof l.folded === 'boolean' ? l.folded : undefined, big: null };
    return out;
  } catch {
    return {};
  }
}

const all = () => (lanes ??= load());
export const laneOf = (project: string): Lane => all()[project] ?? empty;

export function setLane(project: string, change: (l: Lane) => Partial<Lane>): void {
  const l = laneOf(project);
  lanes = { ...all(), [project]: { ...l, ...change(l) } };
  try {
    const kept = Object.fromEntries(Object.entries(lanes).map(([p, { folded }]) => [p, { folded }]));
    localStorage.setItem(storageKey, JSON.stringify(kept));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useLane(project: string): Lane {
  return useSyncExternalStore(subscribe, () => laneOf(project));
}
