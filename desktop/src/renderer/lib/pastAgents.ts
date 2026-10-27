// Past agents (components/PastAgents.tsx): agents whose machine was removed,
// kept with their chats until the user deletes them, and listed on the Work
// page (components/WorkView.tsx) with the live agents' work.
import type * as T from '../../shared/api.ts';

// pastAgentsKey is the start of every past-agents query's key, which a
// destroy, a delete or a continue invalidates.
export const pastAgentsKey = ['archivedAgents'] as const;

// The state a past agent wears as a T.Agent: no machine, nothing to start.
export const pastState = 'archived';

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
