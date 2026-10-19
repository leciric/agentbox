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
