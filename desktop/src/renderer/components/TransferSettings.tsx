import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, FolderOpen, Upload } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import * as T from '../../shared/api';
import { api, ApiError } from '../lib/api';
import { useT } from '../lib/i18n';
import { formatBytes } from '../lib/skills';
import { cn } from '../lib/utils';
import { JobProgress } from './JobProgress';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Input } from './ui/input';
import { SettingNote, SettingRow } from './ui/settings';

// Settings → Export / Import: moving this AgentBox's data to another one
// through one file (package transfer). Both halves preview with a plain
// request, then run the daemon's job and show its log and result, as
// `agentbox export` and `agentbox import` do.

// What an export takes unless unticked: everything but secrets, which are
// opt-in, as they are in the CLI.
const optIn = new Set(['secrets']);
const secretKinds = new Set(['secrets']);

type ProjectPick = { on: boolean; kinds: Set<string>; bundle: boolean };

function Check({ checked, onChange, disabled, children, testId }: { checked: boolean; onChange: (on: boolean) => void; disabled?: boolean; children: ReactNode; testId?: string }) {
  return (
    <label className={cn('inline-flex min-w-0 items-center gap-2 text-[12.5px] text-secondary', disabled && 'opacity-50')} data-transfer-check={testId}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span className="min-w-0 break-words">{children}</span>
    </label>
  );
}

function useKindLabel() {
  const t = useT();
  return (kind: string) => t(`transfer.kind.${kind}` as Parameters<typeof t>[0]);
}

function errorText(t: ReturnType<typeof useT>, err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === T.ErrorWrongPassphrase) return t('transfer.error.wrongPassphrase');
    if (err.code === T.ErrorPassphraseNeeded) return t('transfer.error.passphraseNeeded');
  }
  return err instanceof Error ? err.message : String(err);
}

function PathField({ value, onChange, onPick, placeholder, id }: { value: string; onChange: (v: string) => void; onPick: () => Promise<string | null>; placeholder: string; id: string }) {
  const t = useT();
  const desktop = !('web' in window.agentbox);
  return (
    <div className="flex min-w-0 gap-2">
      <Input id={id} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} className="min-w-0 flex-1 font-mono text-[12.5px]" />
      {desktop && (
        <Button
          size="sm"
          className="h-9"
          onClick={() => void onPick().then((p) => p && onChange(p))}
        >
          <FolderOpen />
          {t('transfer.choose')}
        </Button>
      )}
    </div>
  );
}

export function ExportSettings() {
  const t = useT();
  const kindLabel = useKindLabel();
  const preview = useQuery({ queryKey: ['export-preview'], queryFn: api.exportPreview, staleTime: 30_000 });
  const [picks, setPicks] = useState<Record<string, ProjectPick>>({});
  const [logins, setLogins] = useState<Set<string>>(new Set());
  const [passphrase, setPassphrase] = useState('');
  const [again, setAgain] = useState('');
  const [plain, setPlain] = useState(false);
  const [path, setPath] = useState('');
  const [job, setJob] = useState<string | null>(null);
  const [result, setResult] = useState<T.InstanceExportResult | null>(null);

  const projects = preview.data?.projects ?? [];
  const pickOf = (p: T.InstanceExportProjectPreview): ProjectPick =>
    picks[p.name] ?? { on: true, kinds: new Set(p.categories.map((c) => c.kind).filter((k) => !optIn.has(k))), bundle: false };
  const setPick = (p: T.InstanceExportProjectPreview, change: Partial<ProjectPick>) => setPicks((all) => ({ ...all, [p.name]: { ...pickOf(p), ...change } }));
  const toggleKind = (p: T.InstanceExportProjectPreview, kind: string, on: boolean) => {
    const kinds = new Set(pickOf(p).kinds);
    if (on) kinds.add(kind);
    else kinds.delete(kind);
    setPick(p, { kinds });
  };

  const chosen = projects.filter((p) => pickOf(p).on && pickOf(p).kinds.size > 0);
  const secret = logins.size > 0 || chosen.some((p) => [...pickOf(p).kinds].some((k) => secretKinds.has(k)));
  const total = chosen.reduce(
    (sum, p) =>
      sum +
      p.categories.reduce((n, c) => {
        if (!pickOf(p).kinds.has(c.kind)) return n;
        return n + (c.kind === 'repository' && pickOf(p).bundle ? p.bundleSize : c.size);
      }, 0),
    0,
  );
  const encrypting = !plain || secret;
  const passphraseProblem = !encrypting
    ? ''
    : passphrase.length < 8
      ? t('transfer.passphrase.short')
      : again !== passphrase
        ? t('transfer.passphrase.differ')
        : '';
  const ready = (chosen.length > 0 || logins.size > 0) && path.trim() !== '' && !passphraseProblem;

  const start = useMutation({
    mutationFn: () =>
      api.exportInstance({
        path: path.trim(),
        passphrase: encrypting ? passphrase : undefined,
        noEncrypt: !encrypting,
        projects: chosen.map((p) => ({ name: p.name, categories: [...pickOf(p).kinds], bundle: pickOf(p).bundle })),
        logins: [...logins],
      }),
    onSuccess: (j) => {
      setResult(null);
      setJob(j.id);
    },
  });

  if (preview.isPending) return <SettingRow label={t('transfer.export.title')} description={t('common.loading')} />;
  if (preview.error) return <SettingRow label={t('transfer.export.title')}><Notice>{errorText(t, preview.error)}</Notice></SettingRow>;

  return (
    <SettingRow label={t('transfer.export.title')} description={t('transfer.export.description')} data-transfer-export>
      <div className="grid min-w-0 gap-3">
        {projects.length === 0 && <SettingNote>{t('transfer.export.noProjects')}</SettingNote>}
        {projects.map((p) => {
          const pick = pickOf(p);
          return (
            <div key={p.name} className="grid min-w-0 gap-2 rounded-xl border border-line bg-surface p-3" data-transfer-project={p.name}>
              <Check checked={pick.on} onChange={(on) => setPick(p, { on })} testId={`project:${p.name}`}>
                <span className="font-medium text-primary">{p.displayName}</span>
              </Check>
              {pick.on && (
                <>
                  <div className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-x-4 gap-y-1.5 pl-6">
                    {p.categories.map((c) => (
                      <Check key={c.kind} checked={pick.kinds.has(c.kind)} onChange={(on) => toggleKind(p, c.kind, on)} testId={`${p.name}:${c.kind}`}>
                        {kindLabel(c.kind)}{' '}
                        <span className="text-subtle">
                          {c.kind === 'repository'
                            ? formatBytes(pick.bundle ? p.bundleSize : c.size)
                            : t('transfer.count', { count: c.items, size: formatBytes(c.size) })}
                        </span>
                      </Check>
                    ))}
                  </div>
                  {pick.kinds.has('repository') && (
                    <div className="grid min-w-0 gap-1 pl-6 text-[12px] text-subtle" role="radiogroup" aria-label={kindLabel('repository')}>
                      <label className="flex min-w-0 items-start gap-2">
                        <input type="radio" checked={!pick.bundle} onChange={() => setPick(p, { bundle: false })} />
                        <span className="min-w-0 break-all">
                          {t('transfer.repo.reference')} — {p.remote || t('transfer.repo.noRemote')}
                          {p.branch && ` (${p.branch})`}
                        </span>
                      </label>
                      <label className="flex min-w-0 items-start gap-2" data-transfer-bundle={p.name}>
                        <input type="radio" checked={pick.bundle} onChange={() => setPick(p, { bundle: true })} />
                        <span>{t('transfer.repo.bundle', { size: formatBytes(p.bundleSize) })}</span>
                      </label>
                    </div>
                  )}
                </>
              )}
            </div>
          );
        })}

        <div className="grid min-w-0 gap-1.5">
          <div className="text-[12px] font-medium text-tertiary">{t('transfer.logins.title')}</div>
          <div className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-x-4 gap-y-1.5">
            {(preview.data?.logins ?? []).map((l) => (
              <Check
                key={l.kind}
                checked={logins.has(l.kind)}
                disabled={l.accounts.length === 0}
                testId={`login:${l.kind}`}
                onChange={(on) =>
                  setLogins((all) => {
                    const next = new Set(all);
                    if (on) next.add(l.kind);
                    else next.delete(l.kind);
                    return next;
                  })
                }
              >
                {t(`transfer.login.${l.kind}` as Parameters<typeof t>[0])}{' '}
                <span className="text-subtle">{l.accounts.length ? l.accounts.join(', ') : t('transfer.logins.none')}</span>
              </Check>
            ))}
          </div>
          <SettingNote>{t('transfer.logins.note')}</SettingNote>
        </div>

        <div className="grid min-w-0 gap-2">
          <PathField
            id="export-path"
            value={path}
            onChange={setPath}
            placeholder={t('transfer.export.pathPlaceholder')}
            onPick={() => window.agentbox.pickSaveFile(t('transfer.export.title'), `agentbox-${new Date().toISOString().slice(0, 10)}.agentbox`)}
          />
          {encrypting && (
            <div className="grid min-w-0 gap-2 sm:grid-cols-2">
              <Input type="password" autoComplete="new-password" placeholder={t('transfer.passphrase.label')} value={passphrase} onChange={(e) => setPassphrase(e.target.value)} data-transfer-passphrase />
              <Input type="password" autoComplete="new-password" placeholder={t('transfer.passphrase.again')} value={again} onChange={(e) => setAgain(e.target.value)} data-transfer-passphrase-again />
            </div>
          )}
          <Check checked={plain && !secret} disabled={secret} onChange={setPlain} testId="plain">
            {t('transfer.export.plain')}
          </Check>
          {secret && <SettingNote>{t('transfer.export.plainRefused')}</SettingNote>}
        </div>

        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <Button variant="primary" disabled={!ready || start.isPending || (job !== null && result === null && !start.isError)} onClick={() => start.mutate()} data-transfer-export-start>
            <Download />
            {t('transfer.export.start')}
          </Button>
          <span className="text-[12px] text-subtle" data-transfer-total>
            {t('transfer.export.total', { size: formatBytes(total) })}
          </span>
          {passphraseProblem && passphrase !== '' && <SettingNote tone="warning">{passphraseProblem}</SettingNote>}
        </div>
        {start.error && <Notice>{errorText(t, start.error)}</Notice>}
        {job && (
          <JobProgress
            key={job}
            jobId={job}
            header={false}
            logClassName="h-40"
            onDone={(j) => {
              if (j.status === 'succeeded' && j.result) setResult(j.result as unknown as T.InstanceExportResult);
              else setJob(null);
            }}
          />
        )}
        {result && (
          <Notice tone="info">
            <span data-transfer-export-done>
              {t('transfer.export.done', { path: result.path, size: formatBytes(result.size) })}{' '}
              {result.encrypted ? t('transfer.export.encrypted') : t('transfer.export.notEncrypted')}
            </span>
          </Notice>
        )}
      </div>
    </SettingRow>
  );
}

type ImportPick = { on: boolean; kinds: Set<string>; path: string };

export function ImportSettings() {
  const t = useT();
  const kindLabel = useKindLabel();
  const queryClient = useQueryClient();
  const [path, setPath] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [askPassphrase, setAskPassphrase] = useState(false);
  const [picks, setPicks] = useState<Record<string, ImportPick>>({});
  const [logins, setLogins] = useState<Set<string> | null>(null);
  const [job, setJob] = useState<string | null>(null);
  const [report, setReport] = useState<T.InstanceImportReport | null>(null);

  const inspect = useMutation({
    mutationFn: () => api.inspectImport({ path: path.trim(), passphrase: passphrase || undefined }),
    onSuccess: () => {
      setPicks({});
      setLogins(null);
      setReport(null);
      setJob(null);
    },
    onError: (err) => {
      if (err instanceof ApiError && (err.code === T.ErrorPassphraseNeeded || err.code === T.ErrorWrongPassphrase)) setAskPassphrase(true);
    },
  });
  const contents = inspect.data;

  const pickOf = (p: T.InstanceImportProjectContents): ImportPick => picks[p.name] ?? { on: true, kinds: new Set(p.categories.map((c) => c.kind)), path: p.path };
  const setPick = (p: T.InstanceImportProjectContents, change: Partial<ImportPick>) => setPicks((all) => ({ ...all, [p.name]: { ...pickOf(p), ...change } }));
  const loginsOn = logins ?? new Set((contents?.logins ?? []).filter((l) => l.items > 0).map((l) => l.kind));

  const start = useMutation({
    mutationFn: () =>
      api.importInstance({
        path: path.trim(),
        passphrase: passphrase || undefined,
        projects: (contents?.projects ?? [])
          .filter((p) => pickOf(p).on)
          .map((p) => ({ name: p.name, categories: [...pickOf(p).kinds], path: p.exists ? undefined : pickOf(p).path.trim() || undefined })),
        logins: [...loginsOn],
      }),
    onSuccess: (j) => {
      setReport(null);
      setJob(j.id);
    },
  });

  const newer = inspect.error instanceof ApiError && inspect.error.code === T.ErrorNewerExport;

  return (
    <SettingRow label={t('transfer.import.title')} description={t('transfer.import.description')} data-transfer-import>
      <div className="grid min-w-0 gap-3">
        <PathField id="import-path" value={path} onChange={setPath} placeholder={t('transfer.import.pathPlaceholder')} onPick={() => window.agentbox.pickFile(t('transfer.import.title'))} />
        {askPassphrase && (
          <Input
            type="password"
            autoComplete="off"
            placeholder={t('transfer.passphrase.label')}
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && path.trim() && inspect.mutate()}
            data-transfer-import-passphrase
          />
        )}
        <div>
          <Button disabled={path.trim() === '' || inspect.isPending} onClick={() => inspect.mutate()} data-transfer-open>
            <FolderOpen />
            {t('transfer.import.open')}
          </Button>
        </div>
        {inspect.error && !(inspect.error instanceof ApiError && inspect.error.code === T.ErrorPassphraseNeeded) && (
          <Notice>{newer ? inspect.error.message : errorText(t, inspect.error)}</Notice>
        )}

        {contents && (
          <div className="grid min-w-0 gap-3" data-transfer-contents>
            <SettingNote>
              {t('transfer.import.about', {
                date: new Date(contents.createdAt).toLocaleString(),
                app: contents.app,
                encrypted: contents.encrypted ? 'yes' : 'no',
              })}
            </SettingNote>
            {contents.projects.map((p) => {
              const pick = pickOf(p);
              return (
                <div key={p.name} className="grid min-w-0 gap-2 rounded-xl border border-line bg-surface p-3" data-transfer-import-project={p.name}>
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <Check checked={pick.on} onChange={(on) => setPick(p, { on })} testId={`import:${p.name}`}>
                      <span className="font-medium text-primary">{p.displayName}</span>
                    </Check>
                    <Badge variant={p.exists ? 'info' : 'success'}>{p.exists ? t('transfer.import.merges') : t('transfer.import.new')}</Badge>
                  </div>
                  {pick.on && (
                    <>
                      <div className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-x-4 gap-y-1.5 pl-6">
                        {p.categories.map((c) => (
                          <Check
                            key={c.kind}
                            checked={pick.kinds.has(c.kind)}
                            testId={`import:${p.name}:${c.kind}`}
                            onChange={(on) => {
                              const kinds = new Set(pick.kinds);
                              if (on) kinds.add(c.kind);
                              else kinds.delete(c.kind);
                              setPick(p, { kinds });
                            }}
                          >
                            {kindLabel(c.kind)} <span className="text-subtle">{t('transfer.count', { count: c.items, size: formatBytes(c.size) })}</span>
                          </Check>
                        ))}
                      </div>
                      {!p.exists && pick.kinds.has('repository') && (
                        <div className="grid min-w-0 gap-1.5 pl-6">
                          <div className="text-[12px] text-subtle break-all">
                            {t(`transfer.plan.${p.plan || 'none'}` as Parameters<typeof t>[0])}
                            {p.remote && ` — ${p.remote}`}
                            {p.bundled && ` · ${t('transfer.import.bundled')}`}
                          </div>
                          <PathField
                            id={`import-repo-${p.name}`}
                            value={pick.path}
                            onChange={(v) => setPick(p, { path: v })}
                            placeholder={t('transfer.import.repoPlaceholder')}
                            onPick={() => window.agentbox.pickDirectory()}
                          />
                          {p.planError && pick.path === p.path && <SettingNote tone="warning">{p.planError}</SettingNote>}
                        </div>
                      )}
                    </>
                  )}
                </div>
              );
            })}
            {contents.logins.length > 0 && (
              <div className="grid min-w-0 gap-1.5">
                <div className="text-[12px] font-medium text-tertiary">{t('transfer.logins.title')}</div>
                <div className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-x-4 gap-y-1.5">
                  {contents.logins.map((l) => (
                    <Check
                      key={l.kind}
                      checked={loginsOn.has(l.kind)}
                      disabled={l.items === 0}
                      testId={`import-login:${l.kind}`}
                      onChange={(on) => {
                        const next = new Set(loginsOn);
                        if (on) next.add(l.kind);
                        else next.delete(l.kind);
                        setLogins(next);
                      }}
                    >
                      {t(`transfer.login.${l.kind}` as Parameters<typeof t>[0])} <span className="text-subtle">{t('transfer.accounts', { count: l.items })}</span>
                    </Check>
                  ))}
                </div>
              </div>
            )}
            <div>
              <Button variant="primary" disabled={start.isPending || (job !== null && report === null)} onClick={() => start.mutate()} data-transfer-import-start>
                <Upload />
                {t('transfer.import.start')}
              </Button>
            </div>
            {start.error && <Notice>{errorText(t, start.error)}</Notice>}
          </div>
        )}

        {job && (
          <JobProgress
            key={job}
            jobId={job}
            header={false}
            logClassName="h-40"
            onDone={(j) => {
              if (j.status === 'succeeded' && j.result) {
                setReport(j.result as unknown as T.InstanceImportReport);
                void queryClient.invalidateQueries();
              }
            }}
          />
        )}
        {report && <ImportReportView report={report} />}
      </div>
    </SettingRow>
  );
}

function ImportReportView({ report }: { report: T.InstanceImportReport }) {
  const t = useT();
  return (
    <div className="grid min-w-0 gap-2 rounded-xl border border-line bg-surface p-3" data-transfer-report>
      <div className="text-[13px] font-medium text-primary">
        {t('transfer.report.summary', { added: report.added, skipped: report.skipped, conflicted: report.conflicted })}
      </div>
      {report.projectsAdded.length > 0 && <SettingNote>{t('transfer.report.newProjects', { names: report.projectsAdded.join(', ') })}</SettingNote>}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[28rem] text-left text-[12px]">
          <thead className="text-tertiary">
            <tr>
              <th className="py-1 pr-3 font-medium">{t('transfer.report.part')}</th>
              <th className="py-1 pr-3 font-medium">{t('transfer.report.added')}</th>
              <th className="py-1 pr-3 font-medium">{t('transfer.report.skipped')}</th>
              <th className="py-1 font-medium">{t('transfer.report.conflicted')}</th>
            </tr>
          </thead>
          <tbody className="text-secondary">
            {report.entries.map((e) => (
              <tr key={e.id} className="border-t border-line align-top">
                <td className="py-1 pr-3">
                  <div className="font-mono">{e.id}</div>
                  {e.notes?.map((n, i) => (
                    <div key={i} className="text-subtle break-words">
                      {n}
                    </div>
                  ))}
                </td>
                <td className="py-1 pr-3 tabular-nums">{e.added}</td>
                <td className="py-1 pr-3 tabular-nums">{e.skipped}</td>
                <td className={cn('py-1 tabular-nums', e.conflicted > 0 && 'text-amber-300')}>{e.conflicted}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {report.reportPath && <SettingNote>{t('transfer.report.saved', { path: report.reportPath })}</SettingNote>}
    </div>
  );
}
