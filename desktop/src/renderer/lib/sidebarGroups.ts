// UI v2's sidebar's groups and hidden projects (T.SidebarLayout, kept by the
// daemon so every window and the phone see the same): the edits, each a whole
// new layout for PUT /v1/sidebar, and the reading of it over projectOrder.ts's
// order. It only arranges the sidebar; nothing else, cross-project messaging
// included, looks at it.
import type * as T from '../../shared/api';
import type { Ordered } from './projectOrder.ts';

export const emptyLayout: T.SidebarLayout = { groups: [], hidden: [] };

const without = (list: readonly string[], names: readonly string[]) => list.filter((n) => !names.includes(n));

// hide hides the projects, which keep their group.
export const hide = (l: T.SidebarLayout, names: readonly string[]): T.SidebarLayout => ({ ...l, hidden: [...without(l.hidden, names), ...names] });

export const unhide = (l: T.SidebarLayout, name: string): T.SidebarLayout => ({ ...l, hidden: without(l.hidden, [name]) });

// moveToGroup puts a project in a group, at its end, or in none (null).
export const moveToGroup = (l: T.SidebarLayout, name: string, group: string | null): T.SidebarLayout => ({
  ...l,
  groups: l.groups.map((g) => ({ ...g, projects: g.id === group ? [...without(g.projects, [name]), name] : without(g.projects, [name]) })),
});

// addGroup adds a group at the end, with the projects given moved into it.
export function addGroup(l: T.SidebarLayout, id: string, name: string, projects: readonly string[] = []): T.SidebarLayout {
  const next = { ...l, groups: [...l.groups, { id, name: name.trim(), projects: [] }] };
  return projects.reduce((acc, p) => moveToGroup(acc, p, id), next);
}

export const renameGroup = (l: T.SidebarLayout, id: string, name: string): T.SidebarLayout => ({ ...l, groups: l.groups.map((g) => (g.id === id ? { ...g, name: name.trim() } : g)) });

// removeGroup deletes a group; its projects are left in none.
export const removeGroup = (l: T.SidebarLayout, id: string): T.SidebarLayout => ({ ...l, groups: l.groups.filter((g) => g.id !== id) });

// moveGroup moves a group up (-1) or down (+1) the order; past an end it stays.
export function moveGroup(l: T.SidebarLayout, id: string, by: -1 | 1): T.SidebarLayout {
  const at = l.groups.findIndex((g) => g.id === id);
  const to = at + by;
  if (at < 0 || to < 0 || to >= l.groups.length) return l;
  const groups = [...l.groups];
  [groups[at], groups[to]] = [groups[to], groups[at]];
  return { ...l, groups };
}

export const groupOf = (l: T.SidebarLayout, name: string): T.SidebarGroup | undefined => l.groups.find((g) => g.projects.includes(name));

// A group's projects in one part of the sidebar, or the ungrouped projects'
// (group null).
export type GroupList = { group: T.SidebarGroup | null; projects: T.Project[] };

export type Arranged = { pinned: T.Project[]; active: GroupList[]; idle: GroupList[]; hidden: T.Project[] };

// arrange lays the ordered projects out: the pins on top across groups; then
// the projects with something going on, each group's in the user's order,
// then the ungrouped; then Idle, split the same way, always after every
// active project and group; and the hidden at the very bottom. A hidden
// project with something going on (active) is shown in its place until it
// has none; a pin is never hidden. only narrows it all to one group's
// projects (the switcher), '' for all. Lists may come back empty.
export function arrange(order: Ordered, layout: T.SidebarLayout, active: ReadonlySet<string>, only = ''): Arranged {
  const groupId = new Map<string, string>();
  for (const g of layout.groups) for (const p of g.projects) groupId.set(p, g.id);
  const filter = only && layout.groups.some((g) => g.id === only) ? only : '';
  const inView = (p: T.Project) => !filter || groupId.get(p.name) === filter;
  const hiddenSet = new Set(layout.hidden);
  const isHidden = (p: T.Project) => hiddenSet.has(p.name) && !active.has(p.name);
  const groups: (T.SidebarGroup | null)[] = [...layout.groups.filter((g) => !filter || g.id === filter), ...(filter ? [] : [null])];
  const split = (projects: T.Project[]) =>
    groups.map((group) => ({ group, projects: projects.filter((p) => inView(p) && !isHidden(p) && (groupId.get(p.name) ?? null) === (group?.id ?? null)) }));
  return {
    pinned: order.pinned.filter(inView),
    active: split(order.rest),
    idle: split(order.idle),
    hidden: [...order.rest, ...order.idle].filter((p) => inView(p) && isHidden(p)),
  };
}
