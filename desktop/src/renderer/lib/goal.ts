import type * as T from '../../shared/api';

// An agent's goal as the app edits it (components/GoalEditor.tsx).

// A goal as its fields edit it (internal/api/goal.go's GoalSpec): the
// objective, the two checks AgentBox knows by name, commands that must exit
// 0 (one a line), and the budget ("" is the default, 10).
export interface GoalDraft {
  objective: string;
  prOpen: boolean;
  ciGreen: boolean;
  commands: string;
  budget: string;
}

export const emptyGoal: GoalDraft = { objective: '', prOpen: false, ciGreen: false, commands: '', budget: '' };

// GoalMaxBudget is api.MaxGoalBudget.
export const GoalMaxBudget = 50;

// draftOf is a goal's fields, from the goal an agent has: an AgentBox goal's
// checks and budget, a tool's only its objective.
export function draftOf(goal?: T.AgentGoal): GoalDraft {
  if (!goal || goal.status === 'cleared') return emptyGoal;
  const checks = goal.source === 'agentbox' ? (goal.checks ?? []) : [];
  return {
    objective: goal.objective,
    prOpen: checks.some((c) => c.kind === 'pr_open'),
    ciGreen: checks.some((c) => c.kind === 'ci_green'),
    commands: checks.filter((c) => c.kind === 'command').map((c) => c.command ?? '').join('\n'),
    budget: goal.source === 'agentbox' && goal.budget ? String(goal.budget) : '',
  };
}

// specOf is the goal the API takes, or undefined with no objective.
export function specOf(d: GoalDraft): T.GoalSpec | undefined {
  const objective = d.objective.trim();
  if (!objective) return undefined;
  const checks: T.GoalCheck[] = [];
  if (d.prOpen) checks.push({ kind: 'pr_open' });
  if (d.ciGreen) checks.push({ kind: 'ci_green' });
  for (const line of d.commands.split('\n')) {
    if (line.trim()) checks.push({ kind: 'command', command: line.trim() });
  }
  const budget = Number(d.budget);
  return { objective, checks: checks.length ? checks : undefined, budget: Number.isInteger(budget) && budget > 0 ? budget : undefined };
}

// budgetOK says whether the budget field holds what the daemon takes.
export const budgetOK = (d: GoalDraft) => d.budget.trim() === '' || (/^\d+$/.test(d.budget.trim()) && Number(d.budget) >= 1 && Number(d.budget) <= GoalMaxBudget);
