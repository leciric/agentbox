import { Check, CircleAlert, CircleCheck, CircleSlash, Target, X } from 'lucide-react';
import type * as T from '../../shared/api';
import { useT, type MessageKey } from '../lib/i18n';
import { cn } from '../lib/utils';
import { aiLabel } from './state';
import { Tip } from './ui/tooltip';

// Agent goals (internal/api/goal.go). An agent's goal is either its AI tool's
// own /goal, which AgentBox only shows, or one AgentBox gave it (at create, or
// set later from its page, the lead or the CLI), whose checks the daemon runs
// after each turn. The chip sits on the agent's row; GoalBar (GoalEditor.tsx)
// is the goal on its page, and GoalRow the goal in its chat.

const statusStyle: Record<string, { icon: typeof Target; className: string; label: MessageKey }> = {
  active: { icon: Target, className: 'border-sky-500/30 bg-sky-500/[0.08] text-sky-200', label: 'goal.status.active' },
  met: { icon: CircleCheck, className: 'border-emerald-500/30 bg-emerald-500/[0.08] text-emerald-200', label: 'goal.status.met' },
  blocked: { icon: CircleAlert, className: 'border-amber-500/30 bg-amber-500/[0.10] text-amber-200', label: 'goal.status.blocked' },
  cleared: { icon: CircleSlash, className: 'border-line bg-surface-faint text-subtle', label: 'goal.status.cleared' },
};

const styleOf = (status: string) => statusStyle[status] ?? statusStyle.active;

// goalStyle is a goal's icon, colours and label, for GoalBar too.
export const goalStyle = styleOf;

// GoalChip is the goal on an agent's row (compact: an icon and the count) or
// header (with the objective), its details in a tooltip.
export function GoalChip({ goal, ai, compact, className }: { goal: T.AgentGoal; ai?: string; compact?: boolean; className?: string }) {
  const t = useT();
  const s = styleOf(goal.status);
  const Icon = s.icon;
  const count = goal.source === 'agentbox' ? goal.continuations ?? 0 : goal.checked;
  return (
    <Tip label={<GoalDetails goal={goal} ai={ai} />}>
      <span
        className={cn('inline-flex min-w-0 shrink items-center gap-1 rounded-full border px-1.5 py-px text-caption font-medium', s.className, className)}
        data-goal-chip={goal.status}
        aria-label={`${t(s.label)}: ${goal.objective}`}
      >
        <Icon className="size-3 shrink-0" />
        {compact ? (
          <span className="tabular-nums">{goal.source === 'agentbox' && goal.budget ? `${count}/${goal.budget}` : count > 0 ? count : t('goal.chip')}</span>
        ) : (
          <span className="min-w-0 truncate">{goal.objective}</span>
        )}
      </span>
    </Tip>
  );
}

function GoalDetails({ goal, ai }: { goal: T.AgentGoal; ai?: string }) {
  const t = useT();
  const s = styleOf(goal.status);
  return (
    <div className="max-w-80 space-y-1 text-left" data-goal-details>
      <p className="font-medium">{t(s.label)}</p>
      <p className="break-words">{goal.objective}</p>
      <GoalFacts goal={goal} ai={ai} />
    </div>
  );
}

// GoalFacts are what a goal's chip and row both say under its objective: its
// checks, how far it got, and what is missing.
function GoalFacts({ goal, ai }: { goal: T.AgentGoal; ai?: string }) {
  const t = useT();
  const facts = [
    goal.source === 'agentbox' ? t('goal.source.agentbox') : t('goal.source.tool', { tool: ai ? aiLabel(ai) : 'AI' }),
    goal.checked > 0 && t('goal.checked', { count: goal.checked }),
    goal.source === 'agentbox' && goal.budget ? t('goal.continuations', { count: goal.continuations ?? 0, budget: goal.budget }) : '',
  ].filter(Boolean);
  return (
    <>
      {(goal.checks ?? []).length > 0 && (
        <ul className="space-y-0.5" data-goal-checks>
          {goal.checks!.map((c, i) => (
            <li key={i} className="flex min-w-0 items-start gap-1.5" data-goal-check={c.kind} data-met={c.met === undefined ? 'unknown' : String(c.met)}>
              {c.met === undefined ? <Target className="mt-0.5 size-3 shrink-0 opacity-60" /> : c.met ? <Check className="mt-0.5 size-3 shrink-0 text-emerald-300" /> : <X className="mt-0.5 size-3 shrink-0 text-rose-300" />}
              <span className="min-w-0 break-words">
                {c.kind === 'command' ? t('goal.check.command', { command: c.command ?? '' }) : t(c.kind === 'ci_green' ? 'goal.check.ci_green' : 'goal.check.pr_open')}
                {c.detail && <span className="opacity-70"> — {c.detail}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p className="opacity-70">{facts.join(' · ')}</p>
      {/* What is missing, when the checks above don't already say: a tool's
          own check's reason, or why it is blocked. */}
      {goal.lastUnmet && goal.status !== 'met' && (goal.status === 'blocked' || !(goal.checks ?? []).length) && (
        <p className="break-words opacity-80">{t('goal.lastUnmet', { why: goal.lastUnmet })}</p>
      )}
    </>
  );
}

const rowLabel: Record<string, MessageKey> = { active: 'goal.row.active', met: 'goal.row.met', blocked: 'goal.row.blocked', cleared: 'goal.row.cleared' };

// GoalRow is a goal in the agent's chat: set, checked and found not met yet,
// met, blocked or cleared. A check that only moves the count updates the row
// before it rather than adding one (internal/chat/goal.go).
export function GoalRow({ item, ai }: { item: T.ChatItem; ai?: string }) {
  const t = useT();
  const goal = item.goal!;
  const s = styleOf(goal.status);
  const Icon = s.icon;
  const fresh = goal.status === 'active' && goal.checked === 0 && !(goal.continuations ?? 0);
  return (
    <div className="mb-4 min-w-0 text-ui" data-chat-item="goal" data-goal-status={goal.status} data-chat-id={item.id}>
      <div className={cn('min-w-0 rounded-xl border px-3.5 py-2.5', s.className)}>
        <div className="flex min-w-0 items-center gap-2">
          <Icon className="size-4 shrink-0" />
          <span className="shrink-0 font-medium">{t(fresh ? 'goal.row.set' : rowLabel[goal.status] ?? 'goal.row.active')}</span>
          <span className="min-w-0 truncate text-primary" data-goal-objective>
            {goal.objective}
          </span>
        </div>
        <div className="mt-1.5 space-y-1 pl-6 text-caption text-secondary">
          <GoalFacts goal={goal} ai={ai} />
        </div>
      </div>
    </div>
  );
}
