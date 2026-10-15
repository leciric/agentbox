// The order of UI v2's sidebar (components/v2/SidebarV2.tsx): the projects the
// user pinned first, in the order they dragged them; below them the rest,
// sorted by the switch in the sort menu; and at the bottom, folded, the
// settled ones, which nobody has written to for settleAfterMs. Here is the
// reading; where pins, the sort and when a project was last opened are kept
// is v2Projects.ts.
import type * as T from '../../shared/api';

// settleAfterMs is how long a project goes without the user writing to its
// lead or its agents, or opening it, before it settles: three days.
export const settleAfterMs = 3 * 24 * 60 * 60 * 1000;

// SortBy is the sort menu's switch, for the projects that aren't pinned.
export type SortBy = 'recent' | 'name' | 'working';
export const sortOptions: readonly SortBy[] = ['recent', 'name', 'working'];
export const defaultSort: SortBy = 'recent';
export const isSortBy = (x: unknown): x is SortBy => sortOptions.includes(x as SortBy);

const ms = (at: string | undefined) => {
  const n = at ? Date.parse(at) : NaN;
  return Number.isNaN(n) ? 0 : n;
};

export type SettleInput = {
  pinned: boolean;
  // live is whether the project has something going on now: an agent at work,
  // asking or to review, or its lead busy. It never settles while it does.
  live: boolean;
  createdAt: string;
  // The user's last message to the lead or any of its agents
  // (T.ProjectActivity.lastMessage), and the newest item of any of its chats.
  lastMessage?: string;
  lastActivity?: string;
  // When the user last opened it here.
  opened?: string;
};

// settled is whether a project goes to the Settled shelf. What keeps it up is
// the user: a message from them or opening it. Activity that isn't theirs
// (an agent finishing, the lead answering) doesn't keep it from settling, but
// once it has settled, any brings it back, for another settleAfterMs.
export function settled(p: SettleInput, now: number): boolean {
  if (p.pinned || p.live) return false;
  let touched = Math.max(ms(p.lastMessage), ms(p.opened), ms(p.createdAt));
  const activity = ms(p.lastActivity);
  if (activity >= touched + settleAfterMs) touched = activity;
  return now - touched >= settleAfterMs;
}

export type OrderInput = {
  projects: readonly T.Project[];
  // The pinned projects' names, in the user's order.
  pins: readonly string[];
  sort: SortBy;
  activity: ReadonlyMap<string, T.ProjectActivity>;
  // How many agents of each project are working.
  working: ReadonlyMap<string, number>;
  // Which projects have something going on now (SettleInput.live).
  live: ReadonlySet<string>;
  opened: Readonly<Record<string, string>>;
  label: (p: T.Project) => string;
  now: number;
};

export type Ordered = { pinned: T.Project[]; rest: T.Project[]; settled: T.Project[] };

// recentOf is when a project last saw activity, from its lead or its agents:
// when it was added, before any.
export const recentOf = (p: T.Project, activity: ReadonlyMap<string, T.ProjectActivity>) => Math.max(ms(activity.get(p.name)?.lastActivity), ms(p.createdAt));

// orderProjects splits the projects into the pinned ones, in the pins' order;
// the rest, sorted; and the settled ones, most recent first. A pin overrides
// the sort and never settles; a pin of a project that's gone is skipped.
export function orderProjects(input: OrderInput): Ordered {
  const { projects, pins, sort, activity, working, live, opened, label, now } = input;
  const byName = new Map(projects.map((p) => [p.name, p]));
  const pinned = pins.flatMap((name) => byName.get(name) ?? []);
  const pinnedSet = new Set(pinned.map((p) => p.name));
  const rest: T.Project[] = [];
  const shelf: T.Project[] = [];
  for (const p of projects) {
    if (pinnedSet.has(p.name)) continue;
    const a = activity.get(p.name);
    const isSettled = settled({ pinned: false, live: live.has(p.name), createdAt: p.createdAt, lastMessage: a?.lastMessage, lastActivity: a?.lastActivity, opened: opened[p.name] }, now);
    (isSettled ? shelf : rest).push(p);
  }
  const byName2 = (a: T.Project, b: T.Project) => label(a).localeCompare(label(b), undefined, { sensitivity: 'base' }) || a.name.localeCompare(b.name);
  const byRecent = (a: T.Project, b: T.Project) => recentOf(b, activity) - recentOf(a, activity) || byName2(a, b);
  const compare =
    sort === 'name' ? byName2 : sort === 'working' ? (a: T.Project, b: T.Project) => (working.get(b.name) ?? 0) - (working.get(a.name) ?? 0) || byRecent(a, b) : byRecent;
  return { pinned, rest: rest.sort(compare), settled: shelf.sort(byRecent) };
}

// movePin is the pins after dragging name onto target: before it or after it.
export function movePin(pins: readonly string[], name: string, target: string, edge: 'before' | 'after'): string[] {
  if (name === target) return [...pins];
  const out = pins.filter((p) => p !== name);
  const at = out.indexOf(target);
  if (at < 0) return [...pins];
  out.splice(edge === 'before' ? at : at + 1, 0, name);
  return out;
}

// togglePin pins a project at the bottom of the pins, or unpins it.
export const togglePin = (pins: readonly string[], name: string): string[] => (pins.includes(name) ? pins.filter((p) => p !== name) : [...pins, name]);
