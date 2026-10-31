// The sidebar's order, as data (D79).
//
// The daemon owns it — a section is a row, and a project carries the section
// it is in and where it sits — so everything here is a pure function over
// what the daemon last said. A move produces a whole new layout, which is
// what the API takes: one transaction, no move for the server to infer.
import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';

// A list of the sidebar: one section's projects, or the projects in none,
// which is the list with no section and always comes last.
export type SidebarList = { section: T.Section | null; projects: T.Project[] };

// Where a dragged thing would land. A project drops beside another project,
// onto a section (joining it at the top) or into a list's empty space; a
// section drops beside another section.
export type DropTarget =
  | { kind: 'project'; name: string; edge: 'before' | 'after' }
  | { kind: 'section'; id: string }
  | { kind: 'list'; section: string | null }
  | { kind: 'sectionOrder'; id: string; edge: 'before' | 'after' };

// targetKey identifies a drop target, so the row being drawn can ask whether
// it is the one: comparing the objects field by field is the same answer in
// more places, and one of them would forget the edge.
export const targetKey = (t: DropTarget): string => {
  switch (t.kind) {
    case 'project':
      return `project:${t.name}:${t.edge}`;
    case 'section':
      return `section:${t.id}`;
    case 'sectionOrder':
      return `sectionOrder:${t.id}:${t.edge}`;
    case 'list':
      return `list:${t.section ?? ''}`;
  }
};

// What is being dragged.
export type Dragging = { kind: 'project'; name: string } | { kind: 'section'; id: string };

// buildLists groups the projects the daemon sent — already in its order — by
// the section they are in. Every section is here, including an empty one, and
// the projects in no section come last, in a list of their own.
export function buildLists(projects: T.Project[], sections: T.Section[]): SidebarList[] {
  const lists: SidebarList[] = sections.map((section) => ({ section, projects: [] }));
  const loose: SidebarList = { section: null, projects: [] };
  const byId = new Map(lists.map((l) => [l.section!.id, l]));
  for (const project of projects) (byId.get(project.section) ?? loose).projects.push(project);
  return [...lists, loose];
}

// arrange is buildLists with a layout over it: the order a move asked for,
// kept on screen until the daemon has answered for it, whatever the lists
// say meanwhile (a refetch can answer with the order before the move). The
// projects and sections themselves are still the lists', so a status, a
// rename or a section folded shows at once. What the layout doesn't know, a
// project or section added since, goes where buildLists would put it.
export function arrange(projects: T.Project[], sections: T.Section[], layout: T.ProjectLayout | null): SidebarList[] {
  if (!layout) return buildLists(projects, sections);
  const byName = new Map(projects.map((p) => [p.name, p]));
  const byId = new Map(sections.map((s) => [s.id, s]));
  const placed = new Set<string>();
  const take = (names: string[]) =>
    names.flatMap((name) => {
      const project = byName.get(name);
      if (!project || placed.has(name)) return [];
      placed.add(name);
      return [project];
    });
  const lists: SidebarList[] = layout.sections.flatMap((s) => {
    const section = byId.get(s.id);
    return section ? [{ section, projects: take(s.projects) }] : [];
  });
  const known = new Set(layout.sections.map((s) => s.id));
  for (const section of sections) if (!known.has(section.id)) lists.push({ section, projects: [] });
  const loose: SidebarList = { section: null, projects: take(layout.loose) };
  const bySection = new Map(lists.map((l) => [l.section!.id, l]));
  for (const project of projects) if (!placed.has(project.name)) (bySection.get(project.section) ?? loose).projects.push(project);
  return [...lists, loose];
}

export function toLayout(lists: SidebarList[]): T.ProjectLayout {
  return {
    sections: lists.filter((l) => l.section).map((l) => ({ id: l.section!.id, projects: l.projects.map((p) => p.name) })),
    loose: (lists.find((l) => !l.section)?.projects ?? []).map((p) => p.name),
  };
}

const clone = (lists: SidebarList[]): SidebarList[] => lists.map((l) => ({ section: l.section, projects: [...l.projects] }));

function locate(lists: SidebarList[], name: string): [number, number] | null {
  for (let i = 0; i < lists.length; i++) {
    const j = lists[i].projects.findIndex((p) => p.name === name);
    if (j >= 0) return [i, j];
  }
  return null;
}

// moveProject is one step through the lists as a single sequence: down past
// the end of a list is the top of the next one, and up past the top of a list
// is the end of the one before. That one rule is both "reorder" and "move
// into or out of a section", which is why the keyboard needs nothing else.
// It answers null when there is nowhere to go.
export function moveProject(lists: SidebarList[], name: string, delta: -1 | 1): SidebarList[] | null {
  const at = locate(lists, name);
  if (!at) return null;
  const [i, j] = at;
  const next = clone(lists);
  const [project] = next[i].projects.splice(j, 1);
  const to = j + delta;
  if (to >= 0 && to <= next[i].projects.length) {
    next[i].projects.splice(to, 0, project);
    return next;
  }
  const list = i + delta;
  if (list < 0 || list >= next.length) return null; // the top or the bottom of the sidebar
  next[list].projects.splice(delta === 1 ? 0 : next[list].projects.length, 0, project);
  return next;
}

// moveSection is the same for a section, among the sections. The list of
// projects in no section isn't one and never moves: it is always last.
export function moveSection(lists: SidebarList[], id: string, delta: -1 | 1): SidebarList[] | null {
  const sections = lists.filter((l) => l.section);
  const i = sections.findIndex((l) => l.section!.id === id);
  const to = i + delta;
  if (i < 0 || to < 0 || to >= sections.length) return null;
  const next = clone(sections);
  next.splice(to, 0, ...next.splice(i, 1));
  return [...next, ...lists.filter((l) => !l.section)];
}

// drop applies what a drag ended on, and answers null when it would change
// nothing — dropping a project back where it already was.
export function drop(lists: SidebarList[], dragging: Dragging, target: DropTarget): SidebarList[] | null {
  if (dragging.kind === 'section') {
    if (target.kind !== 'sectionOrder' && target.kind !== 'section') return null;
    const id = target.kind === 'section' ? target.id : target.id;
    if (id === dragging.id) return null;
    const sections = lists.filter((l) => l.section);
    const from = sections.findIndex((l) => l.section!.id === dragging.id);
    let to = sections.findIndex((l) => l.section!.id === id);
    if (from < 0 || to < 0) return null;
    if (target.kind === 'sectionOrder' && target.edge === 'after') to += 1;
    if (to > from) to -= 1;
    const next = clone(sections);
    next.splice(to, 0, ...next.splice(from, 1));
    return [...next, ...lists.filter((l) => !l.section)];
  }

  const at = locate(lists, dragging.name);
  if (!at) return null;
  const [i, j] = at;
  let list: number;
  let index: number;
  if (target.kind === 'project') {
    const to = locate(lists, target.name);
    if (!to) return null;
    [list, index] = to;
    if (target.edge === 'after') index += 1;
  } else if (target.kind === 'section') {
    list = lists.findIndex((l) => l.section?.id === target.id);
    index = 0; // onto the header: the top of the section, right under its name
  } else if (target.kind === 'list') {
    list = lists.findIndex((l) => (l.section?.id ?? null) === target.section);
    index = lists[list]?.projects.length ?? 0;
  } else {
    return null; // a project dropped on a target only a section has
  }
  if (list < 0) return null;
  if (list === i && (index === j || index === j + 1)) return null; // already there
  const next = clone(lists);
  const [project] = next[i].projects.splice(j, 1);
  next[list].projects.splice(list === i && index > j ? index - 1 : index, 0, project);
  return next;
}

// place says where a project ended up, for the live region a screen reader
// reads after a move nobody can see happen.
export function place(lists: SidebarList[], name: string): string {
  const at = locate(lists, name);
  if (!at) return '';
  const [i, j] = at;
  const section = lists[i].section;
  return section
    ? t('shell.sidebar.placeInSection', { name, n: j + 1, total: lists[i].projects.length, section: section.name })
    : t('shell.sidebar.placeNoSection', { name, n: j + 1, total: lists[i].projects.length });
}

// flatten is the lists as the daemon will store and return them: the projects
// in one array, in the sidebar's order and carrying where they now sit, and
// the sections renumbered. Writing this into the cache before the round trip
// is what makes a drag land where it was dropped and stay there.
export function flatten(lists: SidebarList[]): { projects: T.Project[]; sections: T.Section[] } {
  return {
    projects: lists.flatMap((l) => l.projects.map((p, i) => ({ ...p, section: l.section?.id ?? '', position: i + 1 }))),
    sections: lists.filter((l) => l.section).map((l, i) => ({ ...l.section!, position: i + 1 })),
  };
}

// listEnd is where a project dragged over a list but none of its rows (the
// space between two sections) lands: after its last row, or onto the
// section's header when there are no rows to go after.
export function listEnd(list: SidebarList): DropTarget {
  const last = list.section?.collapsed ? undefined : list.projects.at(-1);
  if (last) return { kind: 'project', name: last.name, edge: 'after' };
  return list.section ? { kind: 'section', id: list.section.id } : { kind: 'list', section: null };
}

// UI v2's sidebar (components/v2/SidebarV2.tsx) draws the same sections, but
// not every project in one is in it there: a pinned project is with the pins
// and a hidden one with the hidden, and the projects in no section are in
// v2's own order, by name or by activity, not this one. So v2 works on the
// lists it shows and moves on the whole of them, through drop, which finds a
// project wherever it is.

// sectionsAside is the sections as v2 shows them, without the projects shown
// elsewhere (aside), and the names of the projects they do show.
export function sectionsAside(lists: SidebarList[], aside: ReadonlySet<string>): { sections: SidebarList[]; shown: Set<string> } {
  const sections = lists.filter((l) => l.section).map((l) => ({ section: l.section, projects: l.projects.filter((p) => !aside.has(p.name)) }));
  return { sections, shown: new Set(sections.flatMap((l) => l.projects.map((p) => p.name))) };
}

// stepTarget is moveProject as v2 shows the lists: where one step up or down
// puts a project, as a drop target for drop over the whole lists. In a
// section a step passes the next project shown (not one shown elsewhere),
// and past its end or its top goes into the next section or the one before;
// down past the last section is out of any. The projects in no section have
// no order of their own in v2, so from there the only step is up, to the end
// of the last section.
export function stepTarget(shown: SidebarList[], name: string, delta: -1 | 1): DropTarget | null {
  const at = locate(shown, name);
  if (!at) return null;
  const [i, j] = at;
  const sections = shown.filter((l) => l.section);
  if (shown[i].section) {
    const next = shown[i].projects[j + delta];
    if (next) return { kind: 'project', name: next.name, edge: delta === -1 ? 'before' : 'after' };
  } else if (delta === 1) return null;
  const k = shown[i].section ? sections.indexOf(shown[i]) + delta : sections.length - 1;
  if (k >= sections.length) return { kind: 'list', section: null };
  const into = sections[k];
  if (!into) return null; // the top of the sidebar
  const id = into.section!.id;
  if (delta === 1) {
    const first = into.projects[0];
    return first ? { kind: 'project', name: first.name, edge: 'before' } : { kind: 'section', id };
  }
  const last = into.projects.at(-1);
  return last ? { kind: 'project', name: last.name, edge: 'after' } : { kind: 'list', section: id };
}
