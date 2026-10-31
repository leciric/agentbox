// The sections of the projects, as data (D79).
//
// The daemon owns them — a section is a row, and a project carries the
// section it is in and where it sits — so this is a pure function over what
// the daemon last said.
import type * as T from '../../shared/api';

// A list of projects: one section's, or the projects in none, which is the
// list with no section and always comes last.
export type SidebarList = { section: T.Section | null; projects: T.Project[] };

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
