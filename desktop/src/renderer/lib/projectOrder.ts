// The order of UI v2's sidebar (components/v2/SidebarV2.tsx): the projects the
// user pinned first, in the order they dragged them; then the rest by name,
// which activity never reorders. With "Group idle projects and sort by
// activity" on (groupIdle.ts), the rest with something going on come next,
// most recent first, and at the bottom, folded, the Idle list of those with
// nothing going on, sorted the same. (A sort menu with Name
// and Most agents working went: with the few active projects it sorted, it
// changed nothing you could see.) Here is the reading; where the pins are kept
// is v2Projects.ts.
import type * as T from '../../shared/api';

const ms = (at: string | undefined) => {
  const n = at ? Date.parse(at) : NaN;
  return Number.isNaN(n) ? 0 : n;
};

export type OrderInput = {
  projects: readonly T.Project[];
  // The pinned projects' names, in the user's order.
  pins: readonly string[];
  activity: ReadonlyMap<string, T.ProjectActivity>;
  // Which projects have something going on now (leadFirst.activeProjects,
  // with grace.ts's grace): the others are idle.
  active: ReadonlySet<string>;
  label: (p: T.Project) => string;
  // Group the idle ones apart and sort by activity; off, one stable list.
  groupIdle: boolean;
};

export type Ordered = { pinned: T.Project[]; rest: T.Project[]; idle: T.Project[] };

// recentOf is when a project last saw activity, from its lead or its agents:
// when it was added, before any.
export const recentOf = (p: T.Project, activity: ReadonlyMap<string, T.ProjectActivity>) => Math.max(ms(activity.get(p.name)?.lastActivity), ms(p.createdAt));

// orderProjects splits the projects into the pinned ones, in the pins' order;
// the rest that are active, most recent first; and the idle ones, sorted the
// same. A pin overrides the order and keeps its place idle or not; a pin of a project
// that's gone is skipped.
export function orderProjects(input: OrderInput): Ordered {
  const { projects, pins, activity, active, label, groupIdle } = input;
  const byName = new Map(projects.map((p) => [p.name, p]));
  const pinned = pins.flatMap((name) => byName.get(name) ?? []);
  const pinnedSet = new Set(pinned.map((p) => p.name));
  const byName2 = (a: T.Project, b: T.Project) => label(a).localeCompare(label(b), undefined, { sensitivity: 'base' }) || a.name.localeCompare(b.name);
  if (!groupIdle) return { pinned, rest: projects.filter((p) => !pinnedSet.has(p.name)).sort(byName2), idle: [] };
  const rest: T.Project[] = [];
  const idle: T.Project[] = [];
  for (const p of projects) {
    if (pinnedSet.has(p.name)) continue;
    (active.has(p.name) ? rest : idle).push(p);
  }
  const byRecent = (a: T.Project, b: T.Project) => recentOf(b, activity) - recentOf(a, activity) || byName2(a, b);
  return { pinned, rest: rest.sort(byRecent), idle: idle.sort(byRecent) };
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
