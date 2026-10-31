import type * as T from '../../shared/api';

// latestFinishes maps each agent to the id of the last notice in a lead's chat
// that says it finished. An agent that finishes again (after a check or Request
// changes) gets another notice, but every evidence card draws the agent's
// current state, so only the latest notice keeps its card.
export function latestFinishes(items: T.ChatItem[]): Map<string, string> {
  const latest = new Map<string, string>();
  for (const item of items) for (const ref of item.finished ?? []) latest.set(ref, item.id);
  return latest;
}

// mergedFinishes is the agents (refs) among `refs` whose pull request is merged.
// Their card has said all it has to once the work has landed, so the chat hides
// it (the notice stays in the history and in the lead's context). `prs` is each
// agent's pull request as the app knows it: the freshest read, the project's
// list, over the one on the agent's review item. An agent with none, or whose
// is still open or closed, or whose state isn't loaded yet, keeps its card.
export function mergedFinishes(refs: string[], prs: Map<string, Pick<T.PullRequest, 'state'> | null | undefined>): Set<string> {
  return new Set(refs.filter((ref) => prs.get(ref)?.state === 'merged'));
}

// visibleFinishes splits a notice's agents into those whose card is drawn and
// the count of merged ones left out; `showMerged` draws them all.
export function visibleFinishes(refs: string[], merged: Set<string>, showMerged: boolean): { shown: string[]; hidden: number } {
  if (showMerged) return { shown: refs, hidden: 0 };
  const shown = refs.filter((ref) => !merged.has(ref));
  return { shown, hidden: refs.length - shown.length };
}
