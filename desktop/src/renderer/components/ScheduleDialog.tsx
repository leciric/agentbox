import { useMutation } from '@tanstack/react-query';
import { LoaderCircle } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { dayName, fromLocalInput, localInput, parseWeekly, templates, userTimezone, weeklyCron, type Template } from '../lib/schedules';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Select, SelectOption } from './ui/select';

type Mode = 'once' | 'weekly' | 'every' | 'cron';
type Unit = 'm' | 'h' | 'd';

type Form = {
  title: string;
  task: string;
  mode: Mode;
  at: string;
  days: number[];
  time: string;
  everyN: string;
  everyUnit: Unit;
  cron: string;
  timezone: string;
  target: 'lead' | 'agent';
  session: 'fresh' | 'reuse';
  model: string;
  effort: string;
  precheck: string;
  grace: string;
};

const pad = (n: number) => String(n).padStart(2, '0');

function inAnHour(): string {
  const d = new Date(Date.now() + 60 * 60 * 1000);
  d.setMinutes(0, 0, 0);
  return localInput(d);
}

function everyParts(minutes: number): { everyN: string; everyUnit: Unit } {
  if (minutes > 0 && minutes % 1440 === 0) return { everyN: String(minutes / 1440), everyUnit: 'd' };
  if (minutes > 0 && minutes % 60 === 0) return { everyN: String(minutes / 60), everyUnit: 'h' };
  return { everyN: String(minutes || 30), everyUnit: 'm' };
}

// timing is when a form runs, as cron or the other fields would say it, so
// an edit sends it only when it changed: sending an interval again restarts
// its count from now.
function cronTiming(cron: string): Pick<Form, 'mode' | 'days' | 'time' | 'cron'> {
  const w = parseWeekly(cron);
  if (w) return { mode: 'weekly', days: w.days, time: `${pad(w.hour)}:${pad(w.minute)}`, cron };
  return { mode: 'cron', days: [1, 2, 3, 4, 5], time: '09:00', cron };
}

function initial(schedule: T.Schedule | undefined, template: Template | undefined, t: ReturnType<typeof useT>): Form {
  const base: Form = {
    title: '', task: '', mode: 'once', at: inAnHour(), days: [1, 2, 3, 4, 5], time: '09:00', everyN: '1', everyUnit: 'h', cron: '0 9 * * 1-5',
    timezone: userTimezone(), target: 'lead', session: 'fresh', model: '', effort: '', precheck: '', grace: '',
  };
  if (template) {
    const timing = template.cron ? cronTiming(template.cron) : { mode: 'every' as Mode, ...everyParts(template.every ?? 60) };
    return { ...base, ...timing, title: t(template.title), task: t(template.task), target: template.target, precheck: template.precheck ?? '' };
  }
  if (!schedule) return base;
  const form: Form = {
    ...base,
    title: schedule.title,
    task: schedule.task,
    timezone: schedule.timezone || base.timezone,
    target: schedule.target === 'agent' ? 'agent' : 'lead',
    session: schedule.session === 'reuse' ? 'reuse' : 'fresh',
    model: schedule.model ?? '',
    effort: schedule.effort ?? '',
    precheck: schedule.precheck ?? '',
    grace: schedule.graceMinutes ? String(schedule.graceMinutes) : '',
  };
  if (schedule.kind === 'once') return { ...form, mode: 'once', at: schedule.at ? localInput(new Date(schedule.at)) : base.at };
  if (schedule.kind === 'every') return { ...form, mode: 'every', ...everyParts(schedule.everyMinutes ?? 0) };
  return { ...form, ...cronTiming(schedule.cron ?? '') };
}

function timingOf(f: Form): Pick<T.ScheduleRequest, 'at' | 'every' | 'cron'> {
  switch (f.mode) {
    case 'once':
      return { at: f.at ? fromLocalInput(f.at) : '' };
    case 'every':
      return { every: `${f.everyN}${f.everyUnit}` };
    case 'weekly': {
      const [hour, minute] = f.time.split(':').map(Number);
      return { cron: weeklyCron({ days: f.days, hour: hour || 0, minute: minute || 0 }) };
    }
    case 'cron':
      return { cron: f.cron.trim() };
  }
}

// ScheduleDialog makes a scheduled task, or edits one: a name and the task,
// when (once at a time, on days of the week at a time, every so often, or a
// cron expression, in a timezone), what a run does (tell the chat, or create
// an agent — a new one each time or the last run's — on a model of its own),
// and the two guards, a precheck and how late a missed run may still run. A
// new one can start from a template.
export function ScheduleDialog({
  project,
  schedule,
  template,
  onOpenChange,
  onSaved,
}: {
  project: string;
  schedule?: T.Schedule;
  template?: Template;
  onOpenChange: (open: boolean) => void;
  onSaved: (s: T.Schedule) => void;
}) {
  const t = useT();
  const [start] = useState(() => initial(schedule, template, t));
  const [form, setForm] = useState<Form>(start);
  const set = <K extends keyof Form>(key: K, value: Form[K]) => setForm((f) => ({ ...f, [key]: value }));
  const save = useMutation({
    mutationFn: () => {
      const timing = timingOf(form);
      const changed = !schedule || JSON.stringify(timing) !== JSON.stringify(timingOf(start)) || form.timezone !== start.timezone;
      const req: T.ScheduleRequest = {
        title: form.title.trim(),
        task: form.task,
        target: form.target,
        session: form.session,
        model: form.target === 'agent' ? form.model.trim() : '',
        effort: form.target === 'agent' ? form.effort.trim() : '',
        precheck: form.precheck.trim(),
        graceMinutes: form.grace.trim() === '' ? 0 : Math.max(0, Math.round(Number(form.grace))),
        ...(changed ? { ...timing, timezone: form.timezone.trim() } : {}),
      };
      return schedule ? api.updateSchedule(project, schedule.id, req) : api.addSchedule(project, req);
    },
    onSuccess: onSaved,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const modes: Mode[] = ['once', 'weekly', 'every', 'cron'];
  const modeLabel = { once: t('schedules.mode.once'), weekly: t('schedules.mode.weekly'), every: t('schedules.mode.every'), cron: t('schedules.mode.cron') };

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl" data-schedule-dialog>
        <DialogHeader>
          <DialogTitle>{schedule ? t('schedules.dialog.edit') : t('schedules.dialog.new')}</DialogTitle>
          <DialogDescription>{t('schedules.dialog.description')}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          {!schedule && (
            <div className="grid gap-1.5">
              <span className="text-xs font-medium text-muted">{t('schedules.dialog.templates')}</span>
              <div className="flex flex-wrap gap-1.5">
                {templates.map((tpl) => (
                  <button
                    key={tpl.id}
                    type="button"
                    className="rounded-full border border-line px-2.5 py-1 text-[12px] text-secondary transition hover:border-line-strong hover:bg-surface-raised"
                    onClick={() => setForm({ ...initial(undefined, tpl, t) })}
                    data-template={tpl.id}
                  >
                    {t(tpl.title)}
                  </button>
                ))}
              </div>
            </div>
          )}
          <Field label={t('schedules.field.title')} htmlFor="schedule-title">
            <Input id="schedule-title" value={form.title} placeholder={t('schedules.field.titlePlaceholder')} onChange={(e) => set('title', e.target.value)} />
          </Field>
          <Field label={t('schedules.field.task')} htmlFor="schedule-task" hint={t('schedules.field.taskHint')}>
            <Textarea id="schedule-task" rows={4} value={form.task} onChange={(e) => set('task', e.target.value)} required data-schedule-task />
          </Field>

          <div className="grid gap-2">
            <span className="text-xs font-medium text-muted">{t('schedules.field.when')}</span>
            <div className="flex flex-wrap gap-1 rounded-lg bg-sunken p-1" role="radiogroup">
              {modes.map((m) => (
                <button
                  key={m}
                  type="button"
                  role="radio"
                  aria-checked={form.mode === m}
                  className={cn('rounded-md px-2.5 py-1 text-[12.5px] transition', form.mode === m ? 'bg-surface-raised text-primary shadow-sm' : 'text-subtle hover:text-primary')}
                  onClick={() => set('mode', m)}
                  data-schedule-mode={m}
                >
                  {modeLabel[m]}
                </button>
              ))}
            </div>
            {form.mode === 'once' && (
              <Field label={t('schedules.field.at')} htmlFor="schedule-at">
                <Input id="schedule-at" type="datetime-local" value={form.at} onChange={(e) => set('at', e.target.value)} required data-schedule-at />
              </Field>
            )}
            {form.mode === 'weekly' && (
              <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
                <Field label={t('schedules.field.days')}>
                  <div className="flex flex-wrap gap-1">
                    {[1, 2, 3, 4, 5, 6, 0].map((d) => {
                      const on = form.days.includes(d);
                      return (
                        <button
                          key={d}
                          type="button"
                          aria-pressed={on}
                          className={cn('h-8 min-w-10 rounded-md border px-2 text-[12px] transition', on ? 'border-brand-400/50 bg-brand-500/15 text-brand-200' : 'border-line text-subtle hover:text-primary')}
                          onClick={() => set('days', on ? form.days.filter((x) => x !== d) : [...form.days, d])}
                          data-schedule-day={d}
                        >
                          {dayName(d)}
                        </button>
                      );
                    })}
                  </div>
                </Field>
                <Field label={t('schedules.field.time')} htmlFor="schedule-time">
                  <Input id="schedule-time" type="time" value={form.time} onChange={(e) => set('time', e.target.value)} required data-schedule-time />
                </Field>
              </div>
            )}
            {form.mode === 'every' && (
              <Field label={t('schedules.field.every')} htmlFor="schedule-every">
                <div className="flex gap-2">
                  <Input id="schedule-every" type="number" min={1} className="w-24" value={form.everyN} onChange={(e) => set('everyN', e.target.value)} required />
                  <Select className="w-36" value={form.everyUnit} onChange={(v) => set('everyUnit', v as Unit)} aria-label={t('schedules.field.every')}>
                    <SelectOption value="m">{t('schedules.unit.minutes')}</SelectOption>
                    <SelectOption value="h">{t('schedules.unit.hours')}</SelectOption>
                    <SelectOption value="d">{t('schedules.unit.days')}</SelectOption>
                  </Select>
                </div>
              </Field>
            )}
            {form.mode === 'cron' && (
              <Field label={t('schedules.field.cron')} htmlFor="schedule-cron" hint={t('schedules.field.cronHint')}>
                <Input id="schedule-cron" className="font-mono" value={form.cron} onChange={(e) => set('cron', e.target.value)} required data-schedule-cron />
              </Field>
            )}
            {form.mode !== 'once' && (
              <Field label={t('schedules.field.timezone')} htmlFor="schedule-tz">
                <Input id="schedule-tz" value={form.timezone} onChange={(e) => set('timezone', e.target.value)} />
              </Field>
            )}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t('schedules.field.target')}>
              <Select value={form.target} onChange={(v) => set('target', v as Form['target'])} aria-label={t('schedules.field.target')} data-schedule-target>
                <SelectOption value="lead">{t('schedules.target.lead')}</SelectOption>
                <SelectOption value="agent">{t('schedules.target.agent')}</SelectOption>
              </Select>
            </Field>
            {form.target === 'agent' && (
              <Field label={t('schedules.field.session')}>
                <Select value={form.session} onChange={(v) => set('session', v as Form['session'])} aria-label={t('schedules.field.session')}>
                  <SelectOption value="fresh">{t('schedules.session.fresh')}</SelectOption>
                  <SelectOption value="reuse">{t('schedules.session.reuse')}</SelectOption>
                </Select>
              </Field>
            )}
            {form.target === 'agent' && (
              <Field label={t('schedules.field.model')} htmlFor="schedule-model">
                <Input id="schedule-model" value={form.model} placeholder={t('schedules.field.defaultPlaceholder')} onChange={(e) => set('model', e.target.value)} />
              </Field>
            )}
            {form.target === 'agent' && (
              <Field label={t('schedules.field.effort')} htmlFor="schedule-effort">
                <Input id="schedule-effort" value={form.effort} placeholder={t('schedules.field.defaultPlaceholder')} onChange={(e) => set('effort', e.target.value)} />
              </Field>
            )}
          </div>
          <Field label={t('schedules.field.precheck')} htmlFor="schedule-precheck" hint={t('schedules.field.precheckHint')}>
            <Input id="schedule-precheck" className="font-mono" value={form.precheck} onChange={(e) => set('precheck', e.target.value)} />
          </Field>
          <Field label={t('schedules.field.grace')} htmlFor="schedule-grace" hint={t('schedules.field.graceHint')}>
            <Input id="schedule-grace" type="number" min={0} className="w-28" value={form.grace} onChange={(e) => set('grace', e.target.value)} />
          </Field>
          {save.error && <Notice>{errorMessage(save.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending || !form.task.trim()} data-schedule-save>
              {save.isPending && <LoaderCircle className="animate-spin" />}
              {schedule ? t('schedules.save') : t('schedules.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
