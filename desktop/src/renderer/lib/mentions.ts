// The composer's "@" popup: what it inserts, and what it matches against.
// Every row is this shape, whatever it's built from — files and projects
// today — so the popup itself never needs to know the difference.
export interface MentionItem {
  label: string; // shown in the popup
  insert: string; // what goes after "@" in the message
  kind: 'file' | 'project';
}

export const mentionLimit = 8;
export const projectMentionLimit = 3;

// matchProjects lists the projects whose name or display name contains the
// mention query, case-insensitively, name matches that start earlier first.
// Picking one inserts the project's name (its slug): the daemon swaps it for
// the name and repository path when the message is sent.
export function matchProjects(projects: { name: string; displayName: string }[], query: string, limit = projectMentionLimit): MentionItem[] {
  const q = query.trim().toLowerCase();
  const scored: { item: MentionItem; index: number }[] = [];
  for (const p of projects) {
    const index = Math.min(...[p.name, p.displayName].map((n) => n.toLowerCase().indexOf(q)).filter((i) => i >= 0), Infinity);
    if (index === Infinity) continue;
    scored.push({ item: { label: p.displayName && p.displayName !== p.name ? `${p.displayName} (${p.name})` : p.name, insert: p.name, kind: 'project' }, index });
  }
  scored.sort((a, b) => a.index - b.index);
  return scored.slice(0, limit).map(({ item }) => item);
}

// mentionAt finds the @mention that ends at the cursor, so typing "@" opens
// the popup anywhere in the message, not only at its start. It only triggers
// at the start of a word — "@" preceded by whitespace or nothing — so an
// email address typed mid-sentence doesn't open it.
export function mentionAt(text: string, cursor: number): { start: number; query: string } | undefined {
  const before = text.slice(0, cursor);
  const match = /(?:^|\s)@(\S*)$/.exec(before);
  if (!match) return undefined;
  return { start: cursor - match[1].length - 1, query: match[1] };
}

// matchFiles ranks a worktree's files against a mention query: an empty query
// lists files as given, a substring match (anywhere in the path,
// case-insensitive) ranks by how early and short the match is, and a query
// that matches nothing as a substring falls back to a fuzzy subsequence match
// — "cmpsr" still finds Composer.tsx — ranked by how tight that match is.
// Either way, the best matches come first.
export function matchFiles(files: string[], query: string, limit = mentionLimit): MentionItem[] {
  const q = query.trim().toLowerCase();
  const scored: { path: string; rank: number; index: number; length: number }[] = [];
  for (const path of files) {
    const lower = path.toLowerCase();
    if (q === '') {
      scored.push({ path, rank: 0, index: 0, length: path.length });
      continue;
    }
    const index = lower.indexOf(q);
    if (index >= 0) {
      scored.push({ path, rank: 0, index, length: path.length });
      continue;
    }
    const span = fuzzySpan(lower, q);
    if (span !== undefined) scored.push({ path, rank: 1, index: span, length: path.length });
  }
  scored.sort((a, b) => a.rank - b.rank || a.index - b.index || a.length - b.length);
  return scored.slice(0, limit).map(({ path }) => ({ label: path, insert: path, kind: 'file' }));
}

// fuzzySpan reports how tightly query's characters occur, in order, inside
// text — the width of the shortest run that contains them all as a
// subsequence — or undefined when text doesn't contain query that way.
function fuzzySpan(text: string, query: string): number | undefined {
  let i = 0;
  let start = -1;
  let end = -1;
  for (let j = 0; j < text.length && i < query.length; j++) {
    if (text[j] === query[i]) {
      if (start === -1) start = j;
      end = j;
      i++;
    }
  }
  return i === query.length ? end - start : undefined;
}
