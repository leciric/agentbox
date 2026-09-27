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
  // keywords are what else a search should find it by: the words of its
  // description, and the ones someone might look for it under ("theme" for
  // Appearance, "limits" for resources).
  keywords?: string;
  // advanced settings are the ones most people never change: a section keeps
  // them folded at its end, and a search shows them like any other.
  advanced?: boolean;
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
// in its section's title, its group's, its own name or its keywords — so
// "project nesting" finds a project's nesting switch — and it's changed from
// its default when the query asks for that.
export function matches(q: SettingsQuery, section: SettingSection, group: SettingGroup, entry: SettingEntry): boolean {
  if (q.changed && !entry.modified) return false;
  const haystack = normalize([section.title, group.title, entry.label, entry.keywords ?? ''].join(' '));
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

// splitAdvanced takes a section's advanced settings out of their groups, for
// the page to draw folded at the end. A group left with nothing else goes.
export function splitAdvanced(groups: SettingGroup[]): { groups: SettingGroup[]; advanced: SettingEntry[] } {
  const advanced: SettingEntry[] = [];
  const kept: SettingGroup[] = [];
  for (const group of groups) {
    const entries = group.entries.filter((entry) => {
      if (entry.advanced) advanced.push(entry);
      return !entry.advanced;
    });
    if (entries.length > 0) kept.push({ ...group, entries });
  }
  return { groups: kept, advanced };
}
