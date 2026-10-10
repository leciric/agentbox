import { useQuery } from '@tanstack/react-query';
import {
  ChevronRight,
  CircleCheck,
  CircleDashed,
  CircleMinus,
  CircleX,
  ExternalLink,
  FileDiff,
  GitMerge,
  GitPullRequest,
  LoaderCircle,
  User,
} from 'lucide-react';
import { useCallback, useEffect, useState, type Dispatch, type ReactNode, type SetStateAction } from 'react';
import type { ThemedToken } from 'shiki/core';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { highlight } from '../lib/highlight';
import { useT, type MessageKey } from '../lib/i18n';
import { fileKind, initiallyOpen, isGitHubImage, languageOfPath, nextToLoad, parsePatch, type DiffLine, type FileKind } from '../lib/pulls';
import { useMode } from '../lib/theme';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogTitle } from './ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

const stateLabels: Record<string, MessageKey> = { open: 'memory.pulls.state.open', merged: 'memory.pulls.state.merged', closed: 'memory.pulls.state.closed' };
const stateVariants: Record<string, BadgeVariant> = { open: 'info', merged: 'success', closed: 'default' };
const checksLabels: Record<string, MessageKey> = {
  passing: 'memory.pulls.checks.passing',
  failing: 'memory.pulls.checks.failing',
  pending: 'memory.pulls.checks.pending',
};
const checksVariants: Record<string, BadgeVariant> = { passing: 'success', failing: 'danger', pending: 'warning' };
const statusLabels: Record<string, MessageKey> = {
  added: 'pulls.detail.status.added',
  removed: 'pulls.detail.status.removed',
  renamed: 'pulls.detail.status.renamed',
  copied: 'pulls.detail.status.copied',
};
const kindLabels: Record<Exclude<FileKind, 'code'>, MessageKey> = {
  generated: 'pulls.detail.kind.generated',
  lockfile: 'pulls.detail.kind.lockfile',
  golden: 'pulls.detail.kind.golden',
  binary: 'pulls.detail.kind.binary',
  large: 'pulls.detail.kind.large',
  renamed: 'pulls.detail.kind.renamed',
};

// PullRequestModal is one pull request, read so the user never has to open
// GitHub for it: what it is, who opened it and where it merges, its checks
// and labels, its description as GitHub renders it — pictures included, read
// through the daemon with the project's account — and, on a tab of their own,
// its files. Those are only read once that tab is opened, and their diffs one
// file at a time. Its actions are the row's: merge, and GitHub itself for
// anything else.
export function PullRequestModal({
  project,
  pr,
  onClose,
  canMerge,
  mergeRunning,
  mergeError,
  onMerge,
  onOpenAgent,
}: {
  project: string;
  pr: T.PullRequest | null;
  onClose: () => void;
  canMerge: boolean;
  mergeRunning: boolean;
  mergeError?: string;
  onMerge: () => void;
  onOpenAgent?: () => void;
}) {
  return (
    <Dialog open={pr !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[88vh] max-w-4xl flex-col gap-0 overflow-hidden p-0" data-pull-detail={pr?.number}>
        {pr && (
          <Detail
            project={project}
            pr={pr}
            canMerge={canMerge}
            mergeRunning={mergeRunning}
            mergeError={mergeError}
            onMerge={onMerge}
            onOpenAgent={onOpenAgent}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function Detail({
  project,
  pr: listed,
  canMerge,
  mergeRunning,
  mergeError,
  onMerge,
  onOpenAgent,
}: {
  project: string;
  pr: T.PullRequest;
  canMerge: boolean;
  mergeRunning: boolean;
  mergeError?: string;
  onMerge: () => void;
  onOpenAgent?: () => void;
}) {
  const t = useT();
  const detail = useQuery({ queryKey: ['pull', project, listed.number], queryFn: () => api.pullRequestDetail(project, listed.number) });
  const [tab, setTab] = useState<'description' | 'files'>('description');
  // Which files are open and which diffs have loaded outlive the Files tab,
  // which unmounts whenever the Description tab is shown.
  const [open, setOpen] = useState<ReadonlySet<string> | null>(null);
  const [loaded, setLoaded] = useState<ReadonlySet<string>>(() => new Set());
  // What the list had shows at once; what's read fresh replaces it.
  const pr: T.PullRequest = detail.data ?? listed;
  const imageSrc = useCallback((src: string) => (isGitHubImage(src) ? api.pullImageUrl(project, src) : undefined), [project]);
  const into = pr.state === 'merged' ? 'pulls.detail.intoMerged' : pr.state === 'closed' ? 'pulls.detail.intoClosed' : 'pulls.detail.into';

  return (
    <>
      <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)} className="flex min-h-0 flex-1 flex-col">
        <header className="grid gap-2 border-b border-line-faint px-6 pb-3 pt-5 pr-14">
          <DialogTitle className="flex min-w-0 items-start gap-2 text-[17px] leading-snug">
            <GitPullRequest className="mt-1 size-4 shrink-0 text-subtle" />
            <span className="min-w-0 break-words">
              {pr.title} <span className="font-normal text-subtle">#{pr.number}</span>
            </span>
          </DialogTitle>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 text-[12.5px] text-muted">
            {pr.draft ? (
              <Badge>{t('memory.pulls.draft')}</Badge>
            ) : (
              <Badge variant={stateVariants[pr.state] ?? 'default'}>{stateLabels[pr.state] ? t(stateLabels[pr.state]) : pr.state}</Badge>
            )}
            <span className="flex min-w-0 flex-wrap items-center gap-1.5">
              {t.rich(into, {
                author: (
                  <span className="inline-flex items-center gap-1.5 font-medium text-secondary">
                    {pr.authorAvatar ? <img src={pr.authorAvatar} alt="" className="size-4 rounded-full" /> : <User className="size-3.5" />}
                    {pr.author || '?'}
                  </span>
                ),
                head: <code className="rounded bg-surface-raised px-1.5 py-0.5 font-mono text-[11.5px] text-tertiary">{pr.headBranch}</code>,
                base: <code className="rounded bg-surface-raised px-1.5 py-0.5 font-mono text-[11.5px] text-tertiary">{pr.baseBranch}</code>,
              })}
            </span>
            {pr.updatedAt && <span className="text-faint">· {timeAgo(pr.updatedAt)}</span>}
            {onOpenAgent && (
              <button type="button" onClick={onOpenAgent} className="font-medium text-brand-300 hover:underline">
                {t('memory.pulls.agentOf', { name: pr.agent ?? listed.agent ?? '' })}
              </button>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {pr.checks && (
              <Badge variant={checksVariants[pr.checks] ?? 'default'}>
                {t('memory.pulls.checks', { state: checksLabels[pr.checks] ? t(checksLabels[pr.checks]) : pr.checks })}
              </Badge>
            )}
            {listed.conflict && <Badge variant="danger">{t('memory.pulls.conflicts')}</Badge>}
            {listed.review === 'changes_requested' && <Badge variant="warning">{t('memory.pulls.changesRequested')}</Badge>}
            {detail.data?.labels.map((label) => (
              <LabelChip key={label.name} label={label} />
            ))}
          </div>
          <TabsList className="mt-1 justify-self-start">
            <TabsTrigger value="description">{t('pulls.detail.tab.description')}</TabsTrigger>
            <TabsTrigger value="files" data-pull-files-tab>
              <FileDiff />
              {t('pulls.detail.tab.files')}
              {detail.data && <span className="rounded-full bg-surface-raised px-1.5 text-[11px] text-subtle">{detail.data.changedFiles}</span>}
            </TabsTrigger>
          </TabsList>
        </header>

        <TabsContent value="description" className="overflow-y-auto px-6 py-5" data-pull-body>
          {detail.error && <Notice className="mb-4">{t('pulls.detail.unavailable', { error: errorMessage(detail.error) })}</Notice>}
          {detail.data ? (
            detail.data.body.trim() ? (
              <Markdown text={detail.data.body} imageSrc={imageSrc} className="text-[13.5px]" />
            ) : (
              <p className="text-[13px] italic text-subtle">{t('pulls.detail.noDescription')}</p>
            )
          ) : (
            !detail.error && <p className="text-[13px] text-subtle">{t('common.loading')}</p>
          )}

          {detail.data && detail.data.checkRuns.length > 0 && <Checks runs={detail.data.checkRuns} />}
        </TabsContent>

        {/* Radix mounts a tab's content only while it's shown, so the files are
          first read when this tab is first opened. */}
        <TabsContent value="files" className="overflow-y-auto px-6 py-5">
          <Files project={project} pr={pr} open={open} setOpen={setOpen} loaded={loaded} setLoaded={setLoaded} />
        </TabsContent>
      </Tabs>

      <footer className="flex flex-wrap items-center gap-2 border-t border-line-faint px-6 py-3">
        {mergeError && <Notice className="w-full">{t('memory.pulls.mergeFailed', { error: mergeError })}</Notice>}
        <Button variant="ghost" size="sm" className="ml-auto" onClick={() => void window.agentbox.openExternal(pr.url)}>
          <ExternalLink className="size-3.5" />
          {t('pulls.detail.openOnGitHub')}
        </Button>
        {canMerge && (
          <Button size="sm" disabled={mergeRunning} onClick={onMerge} data-merge-running={mergeRunning || undefined}>
            {mergeRunning ? <LoaderCircle className="size-3.5 animate-spin" /> : <GitMerge className="size-3.5" />}
            {t('memory.pulls.merge')}
          </Button>
        )}
      </footer>
    </>
  );
}

// LabelChip is a label in its GitHub colour, as a dot, so it reads in either
// theme.
function LabelChip({ label }: { label: T.PullLabel }) {
  return (
    <span
      title={label.description || undefined}
      className="inline-flex items-center gap-1.5 rounded-full border border-line px-2 py-0.5 text-[11.5px] font-medium text-secondary"
      data-pull-label={label.name}
    >
      <span className="size-2 rounded-full" style={{ backgroundColor: /^[0-9a-f]{6}$/i.test(label.color ?? '') ? `#${label.color}` : undefined }} />
      {label.name}
    </span>
  );
}

function checkState(run: T.PullCheck): { icon: ReactNode; label: MessageKey } {
  if (run.status !== 'completed') {
    return run.status === 'queued'
      ? { icon: <CircleDashed className="size-3.5 text-amber-400" />, label: 'pulls.detail.check.queued' }
      : { icon: <LoaderCircle className="size-3.5 animate-spin text-amber-400" />, label: 'pulls.detail.check.running' };
  }
  switch (run.conclusion) {
    case 'success':
      return { icon: <CircleCheck className="size-3.5 text-emerald-400" />, label: 'pulls.detail.check.success' };
    case 'failure':
      return { icon: <CircleX className="size-3.5 text-rose-400" />, label: 'pulls.detail.check.failure' };
    case 'timed_out':
      return { icon: <CircleX className="size-3.5 text-rose-400" />, label: 'pulls.detail.check.timedOut' };
    case 'cancelled':
      return { icon: <CircleX className="size-3.5 text-subtle" />, label: 'pulls.detail.check.cancelled' };
    case 'action_required':
      return { icon: <CircleX className="size-3.5 text-amber-400" />, label: 'pulls.detail.check.actionRequired' };
    case 'skipped':
      return { icon: <CircleMinus className="size-3.5 text-subtle" />, label: 'pulls.detail.check.skipped' };
    default:
      return { icon: <CircleMinus className="size-3.5 text-subtle" />, label: 'pulls.detail.check.neutral' };
  }
}

function Checks({ runs }: { runs: T.PullCheck[] }) {
  const t = useT();
  return (
    <section className="mt-6" data-pull-checks>
      <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-subtle">{t('pulls.detail.checks')}</h3>
      <ul className="overflow-hidden rounded-xl border border-line-faint">
        {runs.map((run, i) => {
          const state = checkState(run);
          return (
            <li key={`${run.name}-${i}`} className="flex min-w-0 items-center gap-2 border-b border-line-faint px-3 py-1.5 text-[12.5px] last:border-b-0">
              {state.icon}
              <span className="min-w-0 flex-1 truncate text-secondary">{run.name}</span>
              <span className="shrink-0 text-faint">{t(state.label)}</span>
              {run.url && (
                <button
                  type="button"
                  aria-label={t('pulls.detail.openOnGitHub')}
                  className="shrink-0 rounded p-0.5 text-faint hover:text-brand-300"
                  onClick={() => void window.agentbox.openExternal(run.url!)}
                >
                  <ExternalLink className="size-3.5" />
                </button>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function Files({
  project,
  pr,
  open,
  setOpen,
  loaded,
  setLoaded,
}: {
  project: string;
  pr: T.PullRequest;
  open: ReadonlySet<string> | null;
  setOpen: Dispatch<SetStateAction<ReadonlySet<string> | null>>;
  loaded: ReadonlySet<string>;
  setLoaded: Dispatch<SetStateAction<ReadonlySet<string>>>;
}) {
  const t = useT();
  const query = useQuery({ queryKey: ['pullFiles', project, pr.number], queryFn: () => api.pullRequestFiles(project, pr.number) });
  const files = query.data;
  // The first few small files start open, once they're known.
  useEffect(() => {
    if (files && open === null) setOpen(initiallyOpen(files.files));
  }, [files, open, setOpen]);
  const next = files && open ? nextToLoad(files.files, open, loaded) : undefined;
  const onLoaded = useCallback((path: string) => setLoaded((l) => (l.has(path) ? l : new Set(l).add(path))), [setLoaded]);

  const additions = files?.files.reduce((n, f) => n + f.additions, 0) ?? 0;
  const deletions = files?.files.reduce((n, f) => n + f.deletions, 0) ?? 0;
  return (
    <section data-pull-files>
      <h3 className="mb-2 flex items-center gap-2 text-[12px] font-semibold uppercase tracking-wide text-subtle">
        {files ? t('pulls.detail.files', { count: files.files.length }) : !query.error && t('common.loading')}
        {files && (
          <span className="font-mono normal-case tracking-normal">
            <span className="text-emerald-400">+{additions}</span> <span className="text-rose-400">−{deletions}</span>
          </span>
        )}
      </h3>
      {query.error && <Notice>{t('pulls.detail.filesFailed', { error: errorMessage(query.error) })}</Notice>}
      {files?.truncated && <p className="mb-2 text-[12px] text-subtle">{t('pulls.detail.filesTruncated', { count: files.files.length })}</p>}
      <div className="grid gap-2">
        {files?.files.map((f) => (
          <FileRow
            key={f.path}
            project={project}
            pr={pr}
            file={f}
            open={open?.has(f.path) ?? false}
            mayLoad={loaded.has(f.path) || f.path === next}
            onLoaded={onLoaded}
            onToggle={() =>
              setOpen((o) => {
                const next = new Set(o);
                if (next.has(f.path)) next.delete(f.path);
                else next.add(f.path);
                return next;
              })
            }
          />
        ))}
      </div>
    </section>
  );
}

function FileRow({
  project,
  pr,
  file,
  open,
  mayLoad,
  onLoaded,
  onToggle,
}: {
  project: string;
  pr: T.PullRequest;
  file: T.PullFile;
  open: boolean;
  mayLoad: boolean;
  onLoaded: (path: string) => void;
  onToggle: () => void;
}) {
  const t = useT();
  const kind = fileKind(file);
  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-line-faint" data-pull-file={file.path} data-open={open || undefined}>
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full min-w-0 items-center gap-2 bg-surface-raised/40 px-3 py-2 text-left text-[12.5px] hover:bg-surface-raised"
      >
        <ChevronRight className={cn('size-3.5 shrink-0 text-subtle transition-transform', open && 'rotate-90')} />
        <span className="min-w-0 truncate font-mono text-secondary" title={file.path}>
          {file.path}
        </span>
        {file.previousPath && (
          <span className="hidden min-w-0 truncate font-mono text-faint sm:inline">{t('pulls.detail.renamedFrom', { path: file.previousPath })}</span>
        )}
        {statusLabels[file.status] && <span className="shrink-0 text-faint">{t(statusLabels[file.status])}</span>}
        {kind !== 'code' && <span className="shrink-0 rounded bg-surface-raised px-1.5 text-[11px] text-subtle">{t(kindLabels[kind])}</span>}
        <span className="ml-auto shrink-0 font-mono text-[11.5px]">
          <span className="text-emerald-400">+{file.additions}</span> <span className="text-rose-400">−{file.deletions}</span>
        </span>
      </button>
      {open &&
        (file.hasDiff ? (
          <Diff project={project} pr={pr} path={file.path} enabled={mayLoad} onLoaded={onLoaded} />
        ) : (
          <p className="px-3 py-2 text-[12px] text-subtle">{t('pulls.detail.noDiff')}</p>
        ))}
    </div>
  );
}

const lineTints: Record<DiffLine['kind'], string> = {
  add: 'bg-emerald-500/10',
  del: 'bg-rose-500/10',
  hunk: 'bg-sky-500/10 text-subtle',
  context: '',
  note: 'text-faint italic',
};
const signs: Record<DiffLine['kind'], string> = { add: '+', del: '−', hunk: '', context: ' ', note: '' };

// Diff is one file's diff, read when its turn comes (nextToLoad) and
// highlighted as its language.
function Diff({
  project,
  pr,
  path,
  enabled,
  onLoaded,
}: {
  project: string;
  pr: T.PullRequest;
  path: string;
  enabled: boolean;
  onLoaded: (path: string) => void;
}) {
  const t = useT();
  const diff = useQuery({
    queryKey: ['pullDiff', project, pr.number, pr.headSha, path],
    queryFn: () => api.pullFileDiff(project, pr.number, path),
    staleTime: Infinity,
    enabled,
  });
  const done = diff.isSuccess || diff.isError;
  useEffect(() => {
    if (done) onLoaded(path);
  }, [done, onLoaded, path]);
  const lines = diff.data ? parsePatch(diff.data.patch) : null;
  const tokens = useHighlighted(lines, languageOfPath(path));
  if (diff.error) return <p className="px-3 py-2 text-[12px] text-rose-400">{t('pulls.detail.diffFailed', { error: errorMessage(diff.error) })}</p>;
  if (!lines) return <p className="px-3 py-2 text-[12px] text-subtle">{t('common.loading')}</p>;
  let code = 0;
  return (
    <div className="overflow-x-auto border-t border-line-faint bg-well" data-pull-diff={path}>
      <table className="w-full border-collapse font-mono text-[11.5px] leading-[1.55]">
        <tbody>
          {lines.map((line, i) => {
            const isCode = line.kind === 'add' || line.kind === 'del' || line.kind === 'context';
            const lineTokens = isCode ? tokens?.[code++] : undefined;
            return (
              <tr key={i} className={lineTints[line.kind]}>
                <td className="w-10 select-none px-2 text-right align-top text-faint">{isCode ? line.old : ''}</td>
                <td className="w-10 select-none px-2 text-right align-top text-faint">{isCode ? line.new : ''}</td>
                <td className="w-4 select-none align-top text-subtle">{signs[line.kind]}</td>
                <td className="whitespace-pre pr-4 text-tertiary">
                  {lineTokens
                    ? lineTokens.map((token, j) => (
                        <span key={j} style={{ color: token.color, fontStyle: token.fontStyle === 1 ? 'italic' : undefined }}>
                          {token.content}
                        </span>
                      ))
                    : line.text}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// useHighlighted highlights a diff's code lines, all together so a line is
// read in the context of the ones around it, again when the theme turns over.
function useHighlighted(lines: DiffLine[] | null, lang: string | undefined): ThemedToken[][] | null {
  const mode = useMode();
  const code = lines
    ?.filter((l) => l.kind === 'add' || l.kind === 'del' || l.kind === 'context')
    .map((l) => l.text)
    .join('\n');
  const [out, setOut] = useState<{ code: string; mode: string; tokens: ThemedToken[][] } | null>(null);
  useEffect(() => {
    if (!lang || code === undefined) return;
    let current = true;
    highlight(code, lang)
      .then((tokens) => current && setOut({ code, mode, tokens }))
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [code, lang, mode]);
  return out && out.code === code && out.mode === mode ? out.tokens : null;
}
