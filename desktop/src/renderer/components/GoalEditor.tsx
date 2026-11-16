import { useMutation, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, Pencil, Target, X } from 'lucide-react';
import { useEffect, useId, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { styleOf } from './GoalChip';
import { aiLabel } from './state';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Switch } from './ui/switch';
import { budgetOK, draftOf, emptyGoal, GoalMaxBudget, specOf, type GoalDraft } from '../lib/goal';

export { budgetOK, emptyGoal, specOf, type GoalDraft };

// GoalFields are a goal's fields: in the New agent dialog, and the agent
// page's goal dialog.
export function GoalFields({ value, onChange, autoFocus }: { value: GoalDraft; onChange: (next: GoalDraft) => void; autoFocus?: boolean }) {
  const t = useT();
  const id = useId();
  const set = <K extends keyof GoalDraft>(key: K, v: GoalDraft[K]) => onChange({ ...value, [key]: v });
  return (
    <div className="grid gap-4" data-goal-fields>
      <Field label={t('goal.edit.objective')} htmlFor={`${id}-objective`} hint={t('goal.edit.objectiveHint')}>
        <Textarea
          id={`${id}-objective`}
          autoFocus={autoFocus}
          className="min-h-16"
          value={value.objective}
          placeholder={t('goal.edit.objectivePlaceholder')}
          onChange={(e) => set('objective', e.target.value)}
          data-goal-objective-input
        />
      </Field>
      <div className="grid gap-2">
        <span className="text-ui font-medium text-tertiary">{t('goal.edit.checks')}</span>
        <label className="flex items-center justify-between gap-3 text-ui text-secondary">
          <span>{t('goal.check.pr_open')}</span>
          <Switch checked={value.prOpen} onCheckedChange={(v) => set('prOpen', v)} aria-label={t('goal.check.pr_open')} data-goal-check-input="pr_open" />
        </label>
        <label className="flex items-center justify-between gap-3 text-ui text-secondary">
          <span>{t('goal.check.ci_green')}</span>
          <Switch checked={value.ciGreen} onCheckedChange={(v) => set('ciGreen', v)} aria-label={t('goal.check.ci_green')} data-goal-check-input="ci_green" />
        </label>
        <p className="text-xs text-subtle">{t('goal.edit.checksHint')}</p>
      </div>
      <Field label={t('goal.edit.commands')} htmlFor={`${id}-commands`} hint={t('goal.edit.commandsHint')}>
        <Textarea
          id={`${id}-commands`}
          className="min-h-12 font-mono text-ui"
          value={value.commands}
          placeholder="go test ./..."
          onChange={(e) => set('commands', e.target.value)}
          data-goal-commands-input
        />
      </Field>
      <Field label={t('goal.edit.budget')} htmlFor={`${id}-budget`} hint={budgetOK(value) ? t('goal.edit.budgetHint') : t('goal.edit.budgetBad', { max: GoalMaxBudget })}>
        <Input
          id={`${id}-budget`}
          inputMode="numeric"
          className={cn('w-24', !budgetOK(value) && 'border-rose-500/60')}
          value={value.budget}
          placeholder="10"
          onChange={(e) => set('budget', e.target.value)}
          data-goal-budget-input
        />
      </Field>
    </div>
  );
}

// useGoalMutations are an agent's goal set and cleared, the agent kept in the
// list the app reads it from.
function useGoalMutations(agent: T.Agent, onSet?: () => void) {
  const t = useT();
  const queryClient = useQueryClient();
  const keep = (goal: T.AgentGoal) => {
    queryClient.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === agent.ref ? { ...a, goal } : a)));
    void queryClient.invalidateQueries({ queryKey: ['agents'] });
  };
  const set = useMutation({
    mutationFn: (spec: T.GoalSpec) => api.setGoal(agent.ref, spec),
    onSuccess: (res) => {
      keep(res.goal);
      toast(t(res.told === 'aside' ? 'goal.toast.setWorking' : 'goal.toast.setIdle', { name: agent.name }));
      onSet?.();
    },
  });
  const clear = useMutation({
    mutationFn: () => api.clearGoal(agent.ref),
    onSuccess: (res) => {
      keep(res.goal);
      toast(t('goal.toast.cleared', { name: agent.name }));
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  return { set, clear };
}

// GoalDialog sets or edits an agent's goal.
export function GoalDialog({ agent, open, onClose }: { agent: T.Agent; open: boolean; onClose: () => void }) {
  const t = useT();
  const [draft, setDraft] = useState<GoalDraft>(emptyGoal);
  const { set } = useGoalMutations(agent, onClose);
  const live = agent.goal && agent.goal.status !== 'cleared' ? agent.goal : undefined;
  useEffect(() => {
    if (!open) return;
    setDraft(draftOf(live));
    set.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const spec = specOf(draft);
  const editing = live?.source === 'agentbox' && live.status === 'active';
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-lg" data-goal-dialog>
        <DialogHeader>
          <DialogTitle>{t(editing ? 'goal.edit.titleEdit' : 'goal.edit.title', { name: agent.title || agent.name })}</DialogTitle>
          <DialogDescription>{t('goal.edit.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (spec && budgetOK(draft)) set.mutate(spec);
          }}
        >
          <GoalFields value={draft} onChange={setDraft} autoFocus />
          {live?.source === 'tool' && live.status === 'active' && <Notice>{t('goal.edit.overTool', { tool: aiLabel(agent.ai) })}</Notice>}
          {set.error && <Notice>{errorMessage(set.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!spec || !budgetOK(draft) || set.isPending} data-goal-save>
              {set.isPending && <LoaderCircle className="animate-spin" />}
              {t(editing ? 'goal.edit.saveEdit' : 'goal.edit.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// GoalBar is an agent's goal at the top of its page, under its header: its
// state, objective, checks passed and budget left, with Edit and Clear. An
// agent with no goal has none; its ⋯ sets one (onEdit).
export function GoalBar({ agent, onEdit }: { agent: T.Agent; onEdit: () => void }) {
  const t = useT();
  const { clear } = useGoalMutations(agent);
  const goal = agent.goal;
  if (!goal || goal.status === 'cleared') return null;
  const s = styleOf(goal.status);
  const Icon = s.icon;
  const checks = goal.checks ?? [];
  const passed = checks.filter((c) => c.met === true).length;
  const facts = [
    checks.length > 0 && t('goal.bar.checks', { passed, total: checks.length }),
    goal.source === 'agentbox' && goal.budget ? t('goal.bar.budgetLeft', { left: Math.max(goal.budget - (goal.continuations ?? 0), 0), budget: goal.budget }) : '',
    goal.source === 'tool' && t('goal.source.tool', { tool: aiLabel(agent.ai) }),
  ].filter(Boolean);
  const toolActive = goal.source === 'tool' && goal.status === 'active';
  return (
    <div className={cn('mx-2 mb-2 flex min-w-0 items-start gap-3 rounded-xl border px-3 py-2 md:mx-6', s.className)} data-agent-goal={goal.status}>
      <Icon className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <span className="shrink-0 text-ui font-semibold">{t(s.label)}</span>
          <span className="min-w-0 break-words text-ui text-primary" data-agent-goal-objective>
            {goal.objective}
          </span>
        </div>
        <div className="mt-0.5 flex min-w-0 flex-wrap gap-x-3 text-caption text-secondary" data-agent-goal-facts>
          {facts.map((f, i) => (
            <span key={i}>{f}</span>
          ))}
          {checks.map((c, i) => (
            <span key={`c${i}`} className="inline-flex min-w-0 items-center gap-1" data-agent-goal-check={c.kind} data-met={c.met === undefined ? 'unknown' : String(c.met)}>
              <span className={cn(c.met === true ? 'text-emerald-300' : c.met === false ? 'text-rose-300' : 'opacity-60')}>{c.met === true ? '✓' : c.met === false ? '✗' : '·'}</span>
              <span className="min-w-0 truncate">{c.kind === 'command' ? t('goal.check.command', { command: c.command ?? '' }) : t(c.kind === 'ci_green' ? 'goal.check.ci_green' : 'goal.check.pr_open')}</span>
            </span>
          ))}
        </div>
        {goal.lastUnmet && goal.status !== 'met' && <p className="mt-0.5 break-words text-caption opacity-80">{t('goal.lastUnmet', { why: goal.lastUnmet })}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button size="sm" variant="ghost" className="h-7 px-2" onClick={onEdit} aria-label={t('goal.bar.edit')} data-agent-goal-edit>
          <Pencil className="size-3.5" />
          <span className="hidden sm:inline">{t(goal.status === 'active' ? 'goal.bar.edit' : 'goal.bar.new')}</span>
        </Button>
        {!toolActive && (
          <Button size="sm" variant="ghost" className="h-7 px-2" disabled={clear.isPending} onClick={() => clear.mutate()} aria-label={t('goal.bar.clear')} data-agent-goal-clear>
            {clear.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <X className="size-3.5" />}
            <span className="hidden sm:inline">{t('goal.bar.clear')}</span>
          </Button>
        )}
      </div>
    </div>
  );
}

// GoalIcon is the ⋯ item's icon.
export const GoalIcon = Target;
