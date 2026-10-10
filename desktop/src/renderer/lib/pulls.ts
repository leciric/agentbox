// What the Pull requests tab works out from the list it is given: which pull
// requests a filter keeps, how labels look and what an edit of them shows
// before GitHub answers. Kept apart from the component so it can be tested.
import type * as T from '../../shared/api';

export type PullsState = 'open' | 'closed' | 'all';

// byState keeps the pull requests a state chip shows: open, closed and merged
// ("closed"), or all of them.
export function byState(prs: readonly T.PullRequest[], state: PullsState): T.PullRequest[] {
  if (state === 'all') return [...prs];
  return prs.filter((pr) => (state === 'open' ? pr.state === 'open' : pr.state !== 'open'));
}

// byAuthor keeps the ones login opened, as GitHub's logins compare: without
// case. No login keeps them all, since the account's isn't known.
export function byAuthor(prs: readonly T.PullRequest[], login: string | undefined): T.PullRequest[] {
  if (!login) return [...prs];
  const me = login.toLowerCase();
  return prs.filter((pr) => pr.author?.toLowerCase() === me);
}

// A label edit not answered yet: what it puts on and takes off one pull
// request. The tab shows a pull request's labels with every pending edit
// applied over what the daemon last said, so an edit shows at once, survives
// a refetch that lands before GitHub has answered, and rolls back by itself
// when it fails and is dropped.
export interface LabelEdit {
  id: number;
  number: number;
  add: T.Label[];
  remove: string[];
}

export function withEdits(labels: readonly T.Label[] | undefined, number: number, edits: readonly LabelEdit[]): T.Label[] {
  let out = [...(labels ?? [])];
  for (const edit of edits) {
    if (edit.number !== number) continue;
    out = out.filter((l) => !edit.remove.includes(l.name));
    for (const l of edit.add) if (!out.some((o) => o.name === l.name)) out.push(l);
  }
  return out;
}

// withLabels is the list with one pull request's labels replaced by what
// GitHub answered.
export function withLabels(data: T.ProjectPullRequests, number: number, labels: T.Label[]): T.ProjectPullRequests {
  return { ...data, pullRequests: data.pullRequests.map((pr) => (pr.number === number ? { ...pr, labels } : pr)) };
}

// matchLabels is the picker's search: by name or description, without case,
// names that start with it first.
export function matchLabels(labels: readonly T.Label[], query: string): T.Label[] {
  const q = query.trim().toLowerCase();
  if (!q) return [...labels];
  const hits = labels.filter((l) => l.name.toLowerCase().includes(q) || l.description?.toLowerCase().includes(q));
  const starts = (l: T.Label) => (l.name.toLowerCase().startsWith(q) ? 0 : 1);
  return hits.sort((a, b) => starts(a) - starts(b));
}

// labelStyle is a label in GitHub's colour, readable on either theme: a
// tinted fill and border of the colour itself, and text that mixes it with
// the text colour around it, so yellow is legible on white and navy on black.
export function labelStyle(color: string): { backgroundColor: string; borderColor: string; color: string } {
  const hex = labelHex(color);
  return {
    backgroundColor: `${hex}2e`,
    borderColor: `${hex}73`,
    color: `color-mix(in oklab, ${hex} 60%, currentColor)`,
  };
}

// labelHex is a label's colour as CSS, grey when GitHub sent something else.
export function labelHex(color: string): string {
  return /^[0-9a-f]{6}$/i.test(color) ? `#${color}` : '#8b949e';
}

// The Mine filter is remembered per project, on this machine.
const mineKey = (project: string) => `agentbox.pulls.mine.${project}`;

export function readMine(project: string, storage: Pick<Storage, 'getItem'> = localStorage): boolean {
  return storage.getItem(mineKey(project)) === '1';
}

export function writeMine(project: string, on: boolean, storage: Pick<Storage, 'setItem'> = localStorage): void {
  storage.setItem(mineKey(project), on ? '1' : '0');
}
