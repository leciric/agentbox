import type * as T from '../../shared/api';

// A goal as the New agent form and GoalDialog edit it (components/GoalEditor.tsx),
// and the GoalSpec it is sent as.

export interface GoalDraft {
  objective: string;
  prOpen: boolean;
  ciGreen: boolean;
  commands: string; // one per line
  budget: string; // "" is the daemon's default
}

export const emptyGoalDraft: GoalDraft = { objective: '', prOpen: false, ciGreen: false, commands: '', budget: '' };

// The most times a goal sends its agent back (api.MaxGoalBudget), and how many
// when none is given (api.DefaultGoalBudget).
export const maxGoalBudget = 50;
export const defaultGoalBudget = 10;

export function draftFromGoal(goal?: T.AgentGoal): GoalDraft {
  if (!goal || goal.status === 'cleared') return emptyGoalDraft;
  const checks = goal.checks ?? [];
  return {
    objective: goal.objective,
    prOpen: checks.some((c) => c.kind === 'pr_open'),
    ciGreen: checks.some((c) => c.kind === 'ci_green'),
    commands: checks
      .filter((c) => c.kind === 'command')
      .map((c) => c.command ?? '')
      .join('\n'),
    budget: goal.source === 'agentbox' && goal.budget && goal.budget !== defaultGoalBudget ? String(goal.budget) : '',
  };
}

// specFromDraft is the goal to send: undefined when there is no objective.
export function specFromDraft(d: GoalDraft): T.GoalSpec | undefined {
  const objective = d.objective.trim();
  if (!objective) return undefined;
  const checks: T.GoalCheck[] = [];
  if (d.prOpen) checks.push({ kind: 'pr_open' });
  if (d.ciGreen) checks.push({ kind: 'ci_green' });
  for (const line of d.commands.split('\n')) {
    const command = line.trim();
    if (command) checks.push({ kind: 'command', command });
  }
  const budget = Number.parseInt(d.budget, 10);
  return { objective, checks: checks.length ? checks : undefined, budget: budget > 0 ? Math.min(budget, maxGoalBudget) : undefined };
}

