// Which of an agent's lifecycle buttons apply to its current state, and
// whether it has a chat at all — the one rule AgentView's own buttons and the
// right-click menu (AgentContextMenu) both draw from, so the two never drift
// apart on what an agent's state allows.
import type * as T from '../../shared/api';

export type LifecycleAction = 'pause' | 'resume' | 'start' | 'stop';

export function lifecycleActions(state: string): LifecycleAction[] {
  const actions: LifecycleAction[] = [];
  if (state === 'running') actions.push('pause');
  if (state === 'paused') actions.push('resume');
  if (state === 'stopped') actions.push('start');
  if (state === 'running' || state === 'paused') actions.push('stop');
  return actions;
}

// An agent you use through the chat opens on it; one you use from the terminal has no Chat tab.
export function usesChat(agent: Pick<T.Agent, 'ai' | 'interface'>): boolean {
  return agent.ai !== 'none' && agent.interface === 'chat';
}
