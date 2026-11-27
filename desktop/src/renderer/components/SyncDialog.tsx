import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { JobProgress } from './JobProgress';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { SettingNote } from './ui/settings';

// SyncDialog syncs this AgentBox with the ones it paired with
// (internal/daemon/sync.go), all of them or those named. It first works out
// what a sync would do, a job that changes nothing; then the user unticks the
// projects to leave out and says, for everything both sides have with other
// values, whose version to keep; then it syncs, and says what was done.

// mine is the choice of this AgentBox's version: no remote's id.
const mine = '';

export function SyncDialog({ remotes, onClose }: { remotes?: string[]; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [planJob, setPlanJob] = useState<string | null>(null);
  const [plan, setPlan] = useState<T.SyncPlan | null>(null);
  const [off, setOff] = useState<Set<string>>(new Set());
  const [keep, setKeep] = useState<Record<string, string>>({});
  const [syncJob, setSyncJob] = useState<string | null>(null);
  const [report, setReport] = useState<T.SyncReport | null>(null);

  const planning = useMutation({
    mutationFn: () => api.planSync({ remotes }),
    onSuccess: (j) => setPlanJob(j.id),
  });
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    planning.mutate();
  }, [planning]);

  const ok = (plan?.remotes ?? []).filter((r) => !r.error);
  const projects = (plan?.projects ?? []).filter((p) => !p.skip);
  const conflicts = (plan?.conflicts ?? []).filter((c) => !off.has(c.project ?? ''));
  const syncing = useMutation({
    mutationFn: () => api.sync({ plan: plan!.id, projects: projects.filter((p) => !off.has(p.name)).map((p) => p.name), keep }),
    onSuccess: (j) => setSyncJob(j.id),
  });
  const toggle = (name: string) =>
    setOff((was) => {
      const next = new Set(was);
      if (!next.delete(name)) next.add(name);
      return next;
    });
  const keepAll = (remote: string) =>
    setKeep(Object.fromEntries(conflicts.map((c) => [c.id, remote !== mine && !c.versions.some((v) => v.remote === remote) ? (keep[c.id] ?? mine) : remote])));
  const names = (ids: string[]) => ids.map((id) => plan?.remotes.find((r) => r.id === id)?.name ?? id).join(', ');
  const kind = (k: string) => (k in kinds ? t(kinds[k]) : k);
  const projectName = (name?: string) => plan?.projects?.find((p) => p.name === name)?.displayName || name || '';

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl" data-sync-dialog>
        <DialogHeader>
          <DialogTitle>{t('peers.sync.title')}</DialogTitle>
          <DialogDescription>{t('peers.sync.description')}</DialogDescription>
        </DialogHeader>

        {planning.error && <Notice>{errorMessage(planning.error)}</Notice>}
        {planJob && !plan && (
          <JobProgress
            key={planJob}
            jobId={planJob}
            header={false}
            logClassName="h-28"
            onDone={(j) => {
              if (j.status === 'succeeded' && j.result) setPlan(j.result as unknown as T.SyncPlan);
            }}
          />
        )}

        {plan && !syncJob && (
          <div className="grid min-w-0 gap-5" data-sync-plan>
            {plan.remotes
              .filter((r) => r.error)
              .map((r) => (
                <Notice key={r.id} tone="warning">
                  {r.error}
                </Notice>
              ))}
            {ok.length > 0 && (
              <section className="grid min-w-0 gap-2">
                <h3 className="text-ui font-medium text-primary">{t('peers.sync.projects')}</h3>
                <ul className="grid min-w-0 gap-1.5" data-sync-projects>
                  {(plan.projects ?? []).map((p) => (
                    <li key={p.name} className="flex min-w-0 items-start gap-2">
                      <input
                        id={`sync-project-${p.name}`}
                        type="checkbox"
                        className="mt-0.5"
                        data-sync-project={p.name}
                        checked={!p.skip && !off.has(p.name)}
                        disabled={!!p.skip}
                        onChange={() => toggle(p.name)}
                      />
                      <label htmlFor={`sync-project-${p.name}`} className="grid min-w-0 flex-1 gap-0.5">
                        <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-ui text-secondary">
                          <span className="min-w-0 break-words [overflow-wrap:anywhere]">{p.displayName || p.name}</span>
                          {!p.here && <Badge>{t('peers.sync.newHere')}</Badge>}
                          {p.here && (p.on ?? []).length < ok.length && <Badge>{t('peers.sync.newThere')}</Badge>}
                        </span>
                        <span className="break-words text-caption text-subtle [overflow-wrap:anywhere]">
                          {p.skip
                            ? t('peers.sync.projectSkipped', { why: p.skip })
                            : !p.here
                              ? t('peers.sync.projectFrom', { names: names(p.on ?? []) })
                              : p.incoming > 0
                                ? t('peers.sync.projectIncoming', { count: p.incoming })
                                : (p.on ?? []).length > 0
                                  ? t('peers.sync.projectUpToDate')
                                  : t('peers.sync.projectOnlyHere')}
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {conflicts.length > 0 && (
              <section className="grid min-w-0 gap-2" data-sync-conflicts>
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <h3 className="min-w-0 flex-1 text-ui font-medium text-primary">{t('peers.sync.conflicts', { count: conflicts.length })}</h3>
                  <Button size="sm" variant="ghost" data-sync-keep-all="mine" onClick={() => keepAll(mine)}>
                    {t('peers.sync.keepAllMine')}
                  </Button>
                  {ok.map((r) => (
                    <Button key={r.id} size="sm" variant="ghost" data-sync-keep-all={r.id} onClick={() => keepAll(r.id)}>
                      {t('peers.sync.keepAllTheirs', { name: r.name })}
                    </Button>
                  ))}
                </div>
                <SettingNote>{t('peers.sync.conflictsHelp')}</SettingNote>
                <ul className="grid min-w-0 gap-2">
                  {conflicts.map((c) => {
                    const chosen = keep[c.id] ?? mine;
                    const options = [
                      { remote: mine, name: t('peers.sync.thisAgentBox'), values: c.versions[0]?.fields.map((f) => ({ name: f.name, value: f.mine })) ?? [] },
                      ...c.versions.map((v) => ({ remote: v.remote, name: v.name, values: v.fields.map((f) => ({ name: f.name, value: f.theirs })) })),
                    ];
                    return (
                      <li key={c.id} className="grid min-w-0 gap-2 rounded-xl border border-line px-3 py-2.5" data-sync-conflict={c.id}>
                        <p className="min-w-0 break-words text-ui text-primary [overflow-wrap:anywhere]">
                          <span className="font-medium">{whole.has(c.kind) ? projectName(c.project) : c.label}</span>
                          <span className="text-subtle">
                            {' '}
                            · {kind(c.kind)}
                            {c.project && !whole.has(c.kind) ? ` · ${projectName(c.project)}` : ''}
                          </span>
                        </p>
                        <div className="grid min-w-0 gap-2 sm:grid-cols-2" role="radiogroup" aria-label={c.label}>
                          {options.map((o) => (
                            <button
                              key={o.remote}
                              type="button"
                              role="radio"
                              aria-checked={chosen === o.remote}
                              data-sync-keep={o.remote || 'mine'}
                              onClick={() => setKeep((was) => ({ ...was, [c.id]: o.remote }))}
                              className={cn(
                                'grid min-w-0 content-start gap-1 rounded-lg border px-2.5 py-2 text-left transition-colors',
                                chosen === o.remote ? 'border-brand-400/50 bg-brand-500/15' : 'border-line bg-surface-raised hover:border-line-strong',
                              )}
                            >
                              <span className="flex min-w-0 items-center gap-2 text-caption font-medium text-secondary">
                                <span aria-hidden className={cn('size-2 shrink-0 rounded-full border', chosen === o.remote ? 'border-brand-400 bg-brand-400' : 'border-line-strong')} />
                                <span className="min-w-0 truncate">{t('peers.sync.keepOf', { name: o.name })}</span>
                              </span>
                              {o.values.map((f) => (
                                <span key={f.name} className="grid min-w-0 gap-0.5">
                                  {f.name !== c.kind && <span className="text-caption text-subtle">{f.name.replaceAll('_', ' ')}</span>}
                                  <span className="max-h-32 min-w-0 overflow-y-auto whitespace-pre-wrap break-words text-caption text-secondary [overflow-wrap:anywhere]">
                                    {f.value || t('peers.sync.empty')}
                                  </span>
                                </span>
                              ))}
                            </button>
                          ))}
                        </div>
                      </li>
                    );
                  })}
                </ul>
              </section>
            )}
            {ok.length > 0 && conflicts.length === 0 && <SettingNote>{t('peers.sync.noConflicts')}</SettingNote>}
            {syncing.error && <Notice>{errorMessage(syncing.error)}</Notice>}
          </div>
        )}

        {syncJob && (
          <JobProgress
            key={syncJob}
            jobId={syncJob}
            header={false}
            logClassName="h-28"
            onDone={(j) => {
              if (j.status === 'succeeded' && j.result) setReport(j.result as unknown as T.SyncReport);
              void queryClient.invalidateQueries();
            }}
          />
        )}
        {report && (
          <ul className="grid min-w-0 gap-2" data-sync-report>
            {report.remotes.map((r) => (
              <li key={r.id} className="grid min-w-0 gap-1 rounded-xl border border-line px-3 py-2.5">
                <p className="truncate text-ui font-medium text-primary">{r.name}</p>
                {r.error ? (
                  <SettingNote tone="error">{r.error}</SettingNote>
                ) : (
                  <p className="break-words text-caption text-secondary [overflow-wrap:anywhere]">
                    {t('peers.sync.reportHere', { added: r.received, replaced: r.replaced })}
                    <br />
                    {t('peers.sync.reportThere', { added: r.sent, replaced: r.sentOver })}
                  </p>
                )}
                {(r.projectsAdded ?? []).length > 0 && <SettingNote>{t('peers.sync.reportProjectsThere', { names: (r.projectsAdded ?? []).join(', ') })}</SettingNote>}
                {r.left > 0 && <SettingNote tone="warning">{t('peers.sync.reportLeft', { count: r.left })}</SettingNote>}
                {r.notes?.map((n, i) => (
                  <SettingNote key={i} tone="warning">
                    {n}
                  </SettingNote>
                ))}
              </li>
            ))}
            {report.projectsAdded.length > 0 && <SettingNote>{t('peers.sync.reportProjectsHere', { names: report.projectsAdded.join(', ') })}</SettingNote>}
          </ul>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={onClose} data-sync-close>
            {report ? t('common.close') : t('common.cancel')}
          </Button>
          {plan && !syncJob && ok.length > 0 && (
            <Button variant="primary" disabled={syncing.isPending} onClick={() => syncing.mutate()} data-sync-start>
              <RefreshCw />
              {t('peers.sync.start')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// whole are the kinds that are one thing of a project, which a conflict is
// then named by: its settings, its notes.
const whole = new Set(['settings', 'sections', 'icon', 'notes']);

// kinds is each kind of a project's data as Settings → Export names it.
const kinds: Record<string, Parameters<ReturnType<typeof useT>>[0]> = {
  settings: 'transfer.kind.settings',
  sections: 'transfer.kind.sections',
  icon: 'transfer.kind.icon',
  notes: 'transfer.kind.notes',
  skills: 'transfer.kind.skills',
  memory: 'transfer.kind.memory',
  chats: 'transfer.kind.chats',
  connectors: 'transfer.kind.connectors',
};
