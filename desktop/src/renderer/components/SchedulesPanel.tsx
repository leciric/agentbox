import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlarmClock, Bot, CircleAlert, ExternalLink, History, LoaderCircle, MessageSquare, Pencil, Play, Plus, RotateCw, ShieldCheck, Trash2, Zap } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, useT } from '../lib/i18n';
import { requestReveal } from '../lib/reveal';
import { templates, whenLabel, type Template } from '../lib/schedules';
import { cn, errorMessage, timeAgo, timeUntil } from '../lib/utils';
import type { View } from '../App';
import { ConfirmDialog } from './ConfirmDialog';
import { ScheduleDialog } from './ScheduleDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

// SchedulesPanel is a project's scheduled tasks, in the right panel beside
// its lead's chat (internal/daemon/schedules.go): each with when it runs
// next, a switch to pause it, Run now, Edit and Delete, and its runs; under
// them every run of the project, newest first, each linking to what it did —
// its chip in the chat, or the agent it went to — with Rerun.
export function SchedulesPanel({ project, onSelect }: { project: string; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const schedules = useQuery({ queryKey: ['schedules', project], queryFn: () => api.schedules(project) });
  const runs = useQuery({ queryKey: ['scheduleRuns', project], queryFn: () => api.scheduleRuns(project) });
  const [editing, setEditing] = useState<{ schedule?: T.Schedule; template?: Template } | null>(null);
  const [deleting, setDeleting] = useState<T.Schedule | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['schedules', project] });
    void queryClient.invalidateQueries({ queryKey: ['scheduleRuns', project] });
  };
  const pause = useMutation({
    mutationFn: (s: T.Schedule) => api.updateSchedule(project, s.id, { paused: !s.paused }),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });
  const runNow = useMutation({
    mutationFn: (s: { id: string; title: string }) => api.runSchedule(project, s.id),
    onSuccess: (run, s) => {
      refresh();
      if (run.outcome === 'failed' || run.outcome === 'skipped') toast.error(`${s.title}: ${run.error ?? run.outcome}`);
      else toast.success(t('schedules.ran', { title: s.title }));
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const remove = useMutation({
    mutationFn: (s: T.Schedule) => api.removeSchedule(project, s.id),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });

  const list = schedules.data ?? [];
  const history = runs.data ?? [];
  const openRun = (run: T.ScheduleRun) => {
    if (run.agent) onSelect({ kind: 'agent', ref: `${project}/${run.agent}` });
    else if (run.chatItem) requestReveal({ chat: { project, item: run.chatItem } });
  };

  return (
    <div className="grid min-w-0 gap-4" data-schedules>
      <div className="flex min-w-0 items-start gap-3">
        <p className="min-w-0 flex-1 text-[12.5px] leading-relaxed text-subtle">{t('schedules.description')}</p>
        <Button size="sm" variant="primary" onClick={() => setEditing({})} data-schedule-new>
          <Plus /> {t('schedules.new')}
        </Button>
      </div>

      {schedules.isSuccess && list.length === 0 && (
        <div className="grid gap-3 rounded-xl border border-dashed border-line px-4 py-5" data-schedules-empty>
          <div className="flex items-center gap-2 text-[13.5px] font-medium text-primary">
            <AlarmClock className="size-4 text-brand-300" /> {t('schedules.empty.title')}
          </div>
          <p className="text-[12.5px] text-subtle">{t('schedules.empty.body')}</p>
          <div className="flex flex-wrap gap-2">
            {templates.map((tpl) => (
              <Button key={tpl.id} size="sm" variant="outline" onClick={() => setEditing({ template: tpl })} data-schedule-template={tpl.id}>
                {t(tpl.title)}
              </Button>
            ))}
          </div>
        </div>
      )}

      <ul className="grid min-w-0 gap-2">
        {list.map((s) => {
          const mine = history.filter((r) => r.schedule === s.id);
          return (
            <li key={s.id} className={cn('min-w-0 rounded-xl border border-line bg-surface px-3.5 py-3', s.paused && 'opacity-70')} data-schedule={s.id}>
              <div className="flex min-w-0 items-start gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                    <span className="min-w-0 truncate text-[13.5px] font-medium text-primary">{s.title}</span>
                    {s.target === 'agent' ? (
                      <Badge variant="brand">
                        <Bot /> {s.session === 'reuse' ? t('schedules.target.reuse') : t('schedules.target.agent')}
                      </Badge>
                    ) : (
                      <Badge>
                        <MessageSquare /> {t('schedules.target.lead')}
                      </Badge>
                    )}
                    {s.precheck && (
                      <Tip label={s.precheck}>
                        <Badge>
                          <ShieldCheck /> {t('schedules.precheckBadge')}
                        </Badge>
                      </Tip>
                    )}
                    {s.createdBy === 'lead' && <span className="text-[11px] text-faint">{t('schedules.byLead')}</span>}
                  </div>
                  <p className="mt-0.5 line-clamp-2 break-words text-[12.5px] text-tertiary">{s.task}</p>
                  <p className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 text-[12px] text-subtle">
                    <span>{whenLabel(s, t)}</span>
                    <span>·</span>
                    {s.paused ? (
                      <span className="text-amber-200" data-schedule-paused>{t('schedules.paused')}</span>
                    ) : s.kind === 'event' ? (
                      <span className="inline-flex items-center gap-1" data-schedule-watching>
                        <Zap className="size-3" /> {t('schedules.watching')}
                      </span>
                    ) : s.nextRun ? (
                      <Tip label={formatDateTime(s.nextRun)}>
                        <span data-schedule-next>{t('schedules.next', { when: timeUntil(s.nextRun) })}</span>
                      </Tip>
                    ) : (
                      <span>{t('schedules.noNext')}</span>
                    )}
                    {s.lastRun && (
                      <>
                        <span>·</span>
                        <RunOutcome run={s.lastRun} />
                      </>
                    )}
                  </p>
                </div>
                <Switch checked={!s.paused} onCheckedChange={() => pause.mutate(s)} aria-label={t('schedules.toggle', { title: s.title })} data-schedule-toggle />
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-1">
                <Button size="sm" variant="ghost" disabled={runNow.isPending} onClick={() => runNow.mutate(s)} data-schedule-run>
                  {runNow.isPending && runNow.variables?.id === s.id ? <LoaderCircle className="animate-spin" /> : <Play />} {t('schedules.runNow')}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setEditing({ schedule: s })} data-schedule-edit>
                  <Pencil /> {t('schedules.edit')}
                </Button>
                {mine.length > 0 && (
                  <Button size="sm" variant="ghost" onClick={() => setOpen(open === s.id ? null : s.id)} data-schedule-history>
                    <History /> {t('schedules.history.count', { count: s.runs })}
                  </Button>
                )}
                <Button size="sm" variant="danger" className="ml-auto" onClick={() => setDeleting(s)} data-schedule-delete>
                  <Trash2 /> {t('schedules.delete')}
                </Button>
              </div>
              {open === s.id && <RunList runs={mine} onOpen={openRun} onRerun={() => runNow.mutate(s)} />}
            </li>
          );
        })}
      </ul>

      {history.length > 0 && (
        <section className="grid min-w-0 gap-2" data-schedule-runs>
          <h3 className="text-[12px] font-medium uppercase tracking-wide text-faint">{t('schedules.history.recent')}</h3>
          <RunList
            runs={history.slice(0, 20)}
            titled
            onOpen={openRun}
            onRerun={(run) => list.some((s) => s.id === run.schedule) && runNow.mutate({ id: run.schedule, title: run.title ?? '' })}
          />
        </section>
      )}

      {editing && (
        <ScheduleDialog
          project={project}
          schedule={editing.schedule}
          template={editing.template}
          onOpenChange={(next) => !next && setEditing(null)}
          onSaved={(s) => {
            refresh();
            setEditing(null);
            toast.success(t('schedules.saved', { title: s.title }));
          }}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(next) => !next && setDeleting(null)}
        title={t('schedules.deleteTitle', { title: deleting?.title ?? '' })}
        description={t('schedules.deleteBody')}
        confirmLabel={t('schedules.delete')}
        destructive
        onConfirm={async () => {
          if (deleting) await remove.mutateAsync(deleting);
          setDeleting(null);
        }}
      />
    </div>
  );
}

// RunOutcome says how one run went, in a few words.
function RunOutcome({ run }: { run: T.ScheduleRun }) {
  const t = useT();
  const label =
    run.outcome === 'sent'
      ? t('schedules.run.sent')
      : run.outcome === 'agent'
        ? run.agent
          ? t('schedules.run.agent', { agent: run.agent })
          : t('schedules.run.agentPending')
        : run.outcome === 'failed'
          ? t('schedules.run.failed')
          : run.outcome === 'skipped'
            ? t('schedules.run.skipped')
            : t('schedules.run.running');
  const bad = run.outcome === 'failed' || run.outcome === 'skipped';
  return (
    <Tip label={run.error ? `${formatDateTime(run.ranAt)} · ${run.error}` : formatDateTime(run.ranAt)}>
      <span className={cn('inline-flex items-center gap-1', bad && 'text-rose-300')} data-run-outcome={run.outcome}>
        {bad && <CircleAlert className="size-3" />}
        {label} · {timeAgo(run.ranAt)}
      </span>
    </Tip>
  );
}

function RunList({ runs, titled, onOpen, onRerun }: { runs: T.ScheduleRun[]; titled?: boolean; onOpen: (run: T.ScheduleRun) => void; onRerun: (run: T.ScheduleRun) => void }) {
  const t = useT();
  if (runs.length === 0) return <p className="text-[12px] text-subtle">{t('schedules.history.empty')}</p>;
  return (
    <ul className="mt-2 grid min-w-0 gap-1">
      {runs.map((run) => (
        <li key={run.id} className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 rounded-lg bg-sunken px-2.5 py-1.5 text-[12px] text-tertiary" data-schedule-run-row={run.id}>
          {titled && <span className="min-w-0 truncate font-medium text-secondary">{run.title}</span>}
          <RunOutcome run={run} />
          {run.trigger === 'manual' && <Badge>{t('schedules.run.manual')}</Badge>}
          {run.event && (
            <span className="flex min-w-0 basis-full items-center gap-1 text-subtle" data-run-event>
              <Zap className="size-3 shrink-0" />
              <span className="min-w-0 truncate">{run.event}</span>
              {run.eventUrl && (
                <button
                  type="button"
                  className="flex shrink-0 items-center gap-0.5 rounded px-1 text-[11.5px] hover:bg-surface-strong hover:text-primary"
                  onClick={() => void window.agentbox.openExternal(run.eventUrl!)}
                >
                  {t('schedules.run.event')} <ExternalLink className="size-3" />
                </button>
              )}
            </span>
          )}
          {run.late && (
            <Tip label={run.dueAt ? t('schedules.run.lateTitle', { due: formatDateTime(run.dueAt) }) : ''}>
              <Badge variant="warning" data-run-late>
                {t('schedules.run.late')}
                {(run.missed ?? 0) > 0 && ` · ${t('schedules.run.missed', { count: run.missed ?? 0 })}`}
              </Badge>
            </Tip>
          )}
          <span className="ml-auto flex shrink-0 items-center gap-1">
            {(run.agent || run.chatItem) && (
              <Button size="sm" variant="ghost" className="h-6 px-2 text-[12px]" onClick={() => onOpen(run)} data-run-open>
                {run.agent ? <Bot /> : <MessageSquare />} {run.agent ?? t('schedules.run.open')}
              </Button>
            )}
            <Button size="sm" variant="ghost" className="h-6 px-2 text-[12px]" onClick={() => onRerun(run)} data-run-rerun>
              <RotateCw /> {t('schedules.rerun')}
            </Button>
          </span>
        </li>
      ))}
    </ul>
  );
}
