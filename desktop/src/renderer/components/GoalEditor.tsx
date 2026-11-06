import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Pencil, Target, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { draftFromGoal, emptyGoalDraft, maxGoalBudget, defaultGoalBudget, specFromDraft, type GoalDraft } from '../lib/goalDraft';
import { goalStyle } from './GoalChip';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Label, Textarea } from './ui/input';
import { Switch } from './ui/switch';

// A goal as the app sets one (internal/daemon/goals.go): what done looks like,
// the checks AgentBox runs after each of the agent's turns, and how many times
// it is sent back before the goal is blocked. The New agent form gives one to
// a new agent; GoalBar shows an agent's on its page, and GoalDialog sets,
// changes or clears it on an agent that already runs.

// GoalFields are a goal's inputs, in a form of the caller's.
export function GoalFields({ draft, onChange, id, autoFocus }: { draft: GoalDraft; onChange: (d: GoalDraft) => void; id: string; autoFocus?: boolean }) {
  const t = useT();
  const set = <K extends keyof GoalDraft>(key: K, value: GoalDraft[K]) => onChange({ ...draft, [key]: value });
  const has = draft.objective.trim() !== '';
  return (
    <div className="grid gap-4" data-goal-fields>
      <Field label={t('goal.form.objective')} htmlFor={`${id}-objective`} hint={t('goal.form.objectiveHint')}>
        <Textarea
          id={`${id}-objective`}
          className="min-h-16"
          autoFocus={autoFocus}
          maxLength={1000}
          placeholder={t('goal.form.objectivePlaceholder')}
          value={draft.objective}
          onChange={(event) => set('objective', event.target.value)}
        />
      </Field>
      <div className={cn('grid gap-3', !has && 'opacity-60')}>
        <Label>{t('goal.form.checks')}</Label>
        <label className="flex items-center justify-between gap-3 text-ui text-secondary">
          {t('goal.form.prOpen')}
          <Switch checked={draft.prOpen} disabled={!has} onCheckedChange={(v) => set('prOpen', v)} aria-label={t('goal.form.prOpen')} data-goal-pr-open />
        </label>
        <label className="flex items-center justify-between gap-3 text-ui text-secondary">
          {t('goal.form.ciGreen')}
          <Switch checked={draft.ciGreen} disabled={!has} onCheckedChange={(v) => set('ciGreen', v)} aria-label={t('goal.form.ciGreen')} data-goal-ci-green />
        </label>
        <Field label={t('goal.form.commands')} htmlFor={`${id}-commands`} hint={t('goal.form.commandsHint')}>
          <Textarea
            id={`${id}-commands`}
            className="min-h-12 font-mono text-ui"
            disabled={!has}
            spellCheck={false}
            placeholder="npm test"
            value={draft.commands}
            onChange={(event) => set('commands', event.target.value)}
          />
        </Field>
        <Field label={t('goal.form.budget')} htmlFor={`${id}-budget`} hint={t('goal.form.budgetHint', { max: maxGoalBudget })}>
          <Input
            id={`${id}-budget`}
            className="w-24 tabular-nums"
            type="number"
            min={1}
            max={maxGoalBudget}
            disabled={!has}
            placeholder={String(defaultGoalBudget)}
            value={draft.budget}
            onChange={(event) => set('budget', event.target.value)}
          />
        </Field>
      </div>
    </div>
  );
}

// GoalDialog sets an agent's goal, or changes it: the agent is told, and goes
// back to work towards it if it had finished.
export function GoalDialog({ agent, open, onClose }: { agent: T.Agent; open: boolean; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<GoalDraft>(emptyGoalDraft);
  useEffect(() => {
    if (open) setDraft(draftFromGoal(agent.goal));
    // Only as it opens: a goal the daemon updates meanwhile doesn't wipe what
    // is being typed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const name = agent.title || agent.name;
  const save = useMutation({
    mutationFn: (spec: T.GoalSpec) => api.setAgentGoal(agent.ref, spec),
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: ['agents'] });
      toast(t(res.working ? 'goal.toast.working' : 'goal.toast.set', { name }));
      onClose();
    },
  });
  const editing = !!agent.goal && agent.goal.status !== 'cleared';
  const spec = specFromDraft(draft);
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-lg" data-goal-dialog>
        <DialogHeader>
          <DialogTitle>{t(editing ? 'goal.dialog.editTitle' : 'goal.dialog.setTitle')}</DialogTitle>
          <DialogDescription>{t(editing ? 'goal.dialog.editDescription' : 'goal.dialog.setDescription', { name })}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (spec) save.mutate(spec);
          }}
        >
          <GoalFields id="agent-goal" draft={draft} onChange={setDraft} autoFocus />
          {save.error && <Notice>{errorMessage(save.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!spec || save.isPending} data-goal-save>
              <Target />
              {t('goal.dialog.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// GoalBar is an agent's goal at the top of its page: how it stands, how many
// of its checks passed, how many more times it may be sent back, and what is
// missing when it is blocked, with Edit and Clear.
export function GoalBar({ agent, onEdit }: { agent: T.Agent; onEdit: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const goal = agent.goal;
  const clear = useMutation({
    mutationFn: () => api.clearAgentGoal(agent.ref),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agents'] });
      toast(t('goal.toast.cleared'));
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  if (!goal || goal.status === 'cleared') return null;
  const s = goalStyle(goal.status);
  const Icon = s.icon;
  const checks = goal.checks ?? [];
  const passed = checks.filter((c) => c.met === true).length;
  const left = goal.source === 'agentbox' && goal.budget ? Math.max(goal.budget - (goal.continuations ?? 0), 0) : undefined;
  const facts = [
    checks.length > 0 && t('goal.bar.checks', { passed, total: checks.length }),
    left !== undefined && goal.status === 'active' && t('goal.bar.budgetLeft', { count: left, budget: goal.budget! }),
  ].filter(Boolean);
  return (
    <div className="shrink-0 px-4 pb-2.5 md:px-6" data-goal-bar={goal.status}>
      <div className={cn('min-w-0 rounded-xl border px-3 py-2', s.className)}>
        <div className="flex min-w-0 items-center gap-2">
          <Icon className="size-4 shrink-0" />
          <span className="shrink-0 text-ui font-medium">{t(s.label)}</span>
          <span className="min-w-0 flex-1 truncate text-ui text-primary" title={goal.objective} data-goal-objective>
            {goal.objective}
          </span>
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 pl-6">
          <span className="min-w-0 flex-1 break-words text-caption tabular-nums opacity-80" data-goal-facts>
            {goal.status === 'blocked' && goal.lastUnmet ? t('goal.lastUnmet', { why: goal.lastUnmet }) : facts.join(' · ')}
          </span>
          <div className="-my-1 flex shrink-0 items-center gap-1">
            <Button size="sm" variant="ghost" className="h-7 px-2" onClick={onEdit} data-goal-edit>
              <Pencil />
              {t('goal.bar.edit')}
            </Button>
            <Button size="sm" variant="ghost" className="h-7 px-2" disabled={clear.isPending} onClick={() => clear.mutate()} data-goal-clear>
              <X />
              {t('goal.bar.clear')}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
