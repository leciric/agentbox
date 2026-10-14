// Past agents (components/PastAgents.tsx): agents whose machine was removed,
// kept with their chats until the user deletes them.
import type * as T from '../../shared/api.ts';
import type { SearchSince } from './searchOpen.ts';

// pastAgentsKey is the start of every past-agents query's key, which a
// destroy, a delete or a continue invalidates.
export const pastAgentsKey = ['archivedAgents'] as const;

// The state a past agent wears as a T.Agent: no machine, nothing to start.
export const pastState = 'archived';

// What the pull request filter can be: any, one of GitHub's states, or none.
export type PastPR = '' | 'open' | 'merged' | 'closed' | 'none';
export type PastSince = SearchSince;

// asAgent is a past agent as the chat's components take an agent: what it was,
// with no machine, worktree or address, in a state nothing starts or wakes.
export function asAgent(a: T.ArchivedAgent): T.Agent {
  return {
    ref: a.ref,
    project: a.project,
    name: a.name,
    title: a.title,
    instance: '',
    ai: a.ai,
    autonomous: false,
    branch: a.branch,
    baseRef: a.baseRef,
    baseCommit: a.baseCommit,
    worktree: '',
    source: '',
    claudeAccount: '',
    githubAccount: '',
    interface: a.interface,
    state: pastState,
    ip: '',
    createdAt: a.createdAt,
  };
}

// filtered is whether any filter narrows the list, for its empty state to
// say "nothing matches" rather than "nothing yet".
export function filtered(q: string, pr: PastPR, since: PastSince): boolean {
  return q.trim() !== '' || pr !== '' || since !== '';
}

// toggled is a selection with ref added, or taken out if it was in.
export function toggled(selected: ReadonlySet<string>, ref: string): Set<string> {
  const next = new Set(selected);
  if (!next.delete(ref)) next.add(ref);
  return next;
}
