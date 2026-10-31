import type * as T from '../../shared/api';

// The tool, model and effort the New agent dialog starts on, with "" for a
// model or an effort left to the defaults.
export interface StartingPick {
  ai: string;
  model: string;
  effort: string;
}

// startingPick is where the New agent dialog starts for project p: on the
// project's tool, with its model and effort left to the project's defaults
// (the daemon applies them), else on the tool, model and effort last picked
// there, else on Claude Code.
export function startingPick(p: T.Project, last: T.AgentPick | undefined): StartingPick {
  if (p.agentTool) return { ai: p.agentTool, model: '', effort: '' };
  if (last?.ai) return { ai: last.ai, model: last.model, effort: last.effort };
  return { ai: 'claude', model: '', effort: '' };
}

// projectAgentDefaults is what project p names for its agents on ai: its
// model, effort and context window (in tokens), or nothing for an agent on
// another tool, since each is a setting of the project's tool, Claude Code
// when it names none. "auto" leaves the model to the project's chat.
export function projectAgentDefaults(p: T.Project, ai: string): { model: string; effort: string; window: string } {
  if ((p.agentTool || 'claude') !== ai) return { model: '', effort: '', window: '' };
  return { model: p.agentModel === 'auto' ? '' : p.agentModel, effort: p.agentEffort, window: p.agentContextWindow };
}
