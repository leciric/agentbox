// The shape of the Settings page, and the search across it. Settings is a list
// of sections, each a list of groups, each a list of settings; the page draws a
// section at a time, and a search draws every setting that matches, wherever
// it lives, still under its section and group. Pure data and matching, so the
// search is tested here without a page to draw it on.
import type { ReactNode } from 'react';

export type SettingEntry = {
  id: string;
  // label is the setting's name, as its row shows it.
  label: string;
  // description is the line its row says it does, which a search reads too.
  description?: string;
  // keywords are what else a search should find it by: the words someone
  // might look for it under ("theme" for Appearance, "limits" for resources).
  keywords?: string;
  // essential settings are the few a new user changes: Settings opens on them,
  // and keeps the rest under Advanced. A search finds either alike.
  essential?: boolean;
  // modified is whether it's set to something other than its default, when
  // that can be told; the row is marked, and "@changed" finds it.
  modified?: boolean;
  render: () => ReactNode;
};

export type SettingGroup = {
  id: string;
  title: string;
  description?: ReactNode;
  // cards: each entry stands on its own (a setup check, the VM's size)
  // rather than being a row of the group's one panel.
  cards?: boolean;
  entries: SettingEntry[];
};

export type SettingSection = {
  id: string;
  title: string;
  // description is what the section holds, in one line under its title.
  description: ReactNode;
  scope: 'installation' | 'project';
  // nav is the heading the section sits under in the sidebar: the sections
  // that share one follow each other, and the first few have none.
  nav?: string;
  groups: SettingGroup[];
  // attention is how many things in it want a look, badged in the sidebar.
  attention?: number;
  // header and footer are drawn above and below the section's groups when
  // it's open, and never in a search.
  header?: ReactNode;
  footer?: ReactNode;
};

// changedToken finds the settings that are set to something other than their
// default, as VS Code's @modified does.
export const changedTokens = ['@changed', '@modified'];

// normalize lowercases, drops accents, and makes curly and straight
// apostrophes alike, so "agents" finds "agent's" and "cafe" finds "café".
export function normalize(text: string): string {
  return text
    .normalize('NFD')
    .replace(/\p{Diacritic}/gu, '')
    .toLowerCase()
    .replace(/[’']/g, '');
}

export type SettingsQuery = { words: string[]; changed: boolean };

export function parseQuery(query: string): SettingsQuery {
  const words: string[] = [];
  let changed = false;
  for (const word of normalize(query).split(/\s+/)) {
    if (!word) continue;
    if (changedTokens.includes(word)) changed = true;
    else words.push(word);
  }
  return { words, changed };
}

export function isEmptyQuery(q: SettingsQuery): boolean {
  return q.words.length === 0 && !q.changed;
}

// matches says whether one setting answers the query: every word is somewhere
// in its section's title, its group's, its own name, description or keywords — so
// "project nesting" finds a project's nesting switch — and it's changed from
// its default when the query asks for that.
export function matches(q: SettingsQuery, section: SettingSection, group: SettingGroup, entry: SettingEntry): boolean {
  if (q.changed && !entry.modified) return false;
  const haystack = normalize([section.title, group.title, entry.label, entry.description ?? '', entry.keywords ?? ''].join(' '));
  return q.words.every((word) => haystack.includes(word));
}

// search keeps the sections, groups and settings that match, in the page's own
// order: a setting is found where it lives, not ranked away from its
// neighbours.
export function search(sections: SettingSection[], query: string): SettingSection[] {
  const q = parseQuery(query);
  if (isEmptyQuery(q)) return sections;
  const found: SettingSection[] = [];
  for (const section of sections) {
    const groups: SettingGroup[] = [];
    for (const group of section.groups) {
      const entries = group.entries.filter((entry) => matches(q, section, group, entry));
      if (entries.length > 0) groups.push({ ...group, entries });
    }
    if (groups.length > 0) found.push({ ...section, groups });
  }
  return found;
}

// countEntries is how many settings a section holds, for the sidebar's count
// of what a search found in it.
export function countEntries(section: SettingSection): number {
  return section.groups.reduce((n, g) => n + g.entries.length, 0);
}

// splitEssentials parts groups into the essential settings and the rest,
// each still under its own group. A group left with nothing goes from that
// side.
export function splitEssentials(groups: SettingGroup[]): { essentials: SettingGroup[]; advanced: SettingGroup[] } {
  const essentials: SettingGroup[] = [];
  const advanced: SettingGroup[] = [];
  for (const group of groups) {
    const kept = group.entries.filter((entry) => entry.essential);
    const rest = group.entries.filter((entry) => !entry.essential);
    if (kept.length > 0) essentials.push({ ...group, entries: kept });
    if (rest.length > 0) advanced.push({ ...group, entries: rest });
  }
  return { essentials, advanced };
}

// essentialsSection gathers every section's essential settings into the one
// Settings opens on, under the groups they come from, in the sections' order.
// Each group's id is prefixed with its section's, as two sections may share
// a group id.
export function essentialsSection(sections: SettingSection[], title: string, description: ReactNode): SettingSection {
  const groups = sections.flatMap((section) =>
    splitEssentials(section.groups).essentials.map((group) => ({ ...group, id: `${section.id}.${group.id}` })),
  );
  return { id: essentialsId, title, description, scope: 'installation', groups };
}

export const essentialsId = 'essentials';
