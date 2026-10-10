import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ExternalLink, GitBranch, GitMerge, GitPullRequest, LoaderCircle, MessageSquare, RotateCw, Tag, User } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { useReveal } from '../lib/reveal';
import { byAuthor, byState, labelHex, labelStyle, matchLabels, readMine, withEdits, withLabels, writeMine, type LabelEdit, type PullsState } from '../lib/pulls';
import { cn, errorMessage, githubAccountLabel, githubErrorSentence, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { FilterChip } from './MediaTab';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Code, EmptyState, Notice } from './ui/card';
import { PullRequestModal } from './PullRequestModal';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';
import { Select, SelectOption } from './ui/select';
import { Tip } from './ui/tooltip';

const methodLabels: Record<string, MessageKey> = {
  merge: 'memory.pulls.method.merge',
  squash: 'memory.pulls.method.squash',
  rebase: 'memory.pulls.method.rebase',
};
const stateLabels: Record<string, MessageKey> = { open: 'memory.pulls.state.open', merged: 'memory.pulls.state.merged', closed: 'memory.pulls.state.closed' };
const checksLabels: Record<string, MessageKey> = {
  passing: 'memory.pulls.checks.passing',
  failing: 'memory.pulls.checks.failing',
  pending: 'memory.pulls.checks.pending',
};
const allMethods = ['merge', 'squash', 'rebase'];

// PullRequestsPanel lists a project repository's pull requests — every one
// GitHub has, not only the ones with an agent behind them — and lets you
// merge one, behind a confirmation that always names the branch it merges
// into. They are read with the project's GitHub account, so the panel always
// says which one that is: the same repository is a 404 for the wrong account,
// and "no pull requests" would be a lie about it.
export function PullRequestsPanel({
  project,
  onSelect,
  onOpenAccount,
}: {
  project: string;
  onSelect: (view: View) => void;
  onOpenAccount: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  // The daemon answers from its cache and re-reads GitHub behind the answer,
  // announcing on the event stream when that read ends, so this polls as a
  // backstop rather than as the way the list stays current (D54): every
  // minute, or every few seconds while a read is out, so "refreshing" never
  // outlives it by more than that even if the event is missed. Coming back to
  // the window asks again, which re-reads GitHub when the cache is old.
  const pulls = useQuery({
    queryKey: ['pulls', project],
    queryFn: () => api.projectPullRequests(project),
    refetchInterval: (query) => (query.state.data?.refreshing ? 4_000 : 60_000),
    refetchOnWindowFocus: true,
  });
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const [filter, setFilter] = useState<PullsState>('open');
  const [mine, setMine] = useMine(project);
  // The refresh button: GitHub is re-read now, whatever the cache's age, and
  // the answer that says so replaces the list at once.
  const refresh = useMutation({
    mutationFn: () => api.projectPullRequests(project, true),
    onSuccess: (data) => queryClient.setQueryData(['pulls', project], data),
  });
  // A search result for one that's closed or merged: list them all, so it is
  // there to be brought forward.
  const reveal = useReveal();
  const revealedState = reveal?.pull?.project === project ? pulls.data?.pullRequests.find((pr) => pr.number === reveal.pull?.number)?.state : undefined;
  useEffect(() => {
    if (revealedState && revealedState !== 'open') setFilter('all');
  }, [revealedState, reveal]);
  const [merging, setMerging] = useState<T.PullRequest | null>(null);
  // The pull request open in the modal, by number, so it follows the list as
  // it's re-read: merged under it, say.
  const [viewing, setViewing] = useState<number | null>(null);
  const [method, setMethod] = useState('merge');
  // What each merge is doing, by pull request number. The confirmation closes
  // the moment it's confirmed, and the row says the rest: a spinner on its
  // button while the merge runs, or why it failed. Merges are independent,
  // so every other row stays usable meanwhile.
  const [running, setRunning] = useState<ReadonlySet<number>>(() => new Set());
  const [failed, setFailed] = useState<Readonly<Record<number, string>>>({});
  // Label edits GitHub hasn't answered yet, shown over the list as though
  // they had landed (lib/pulls.ts withEdits). One that fails is dropped,
  // which puts the row back as it was, and says why.
  const [edits, setEdits] = useState<readonly LabelEdit[]>([]);
  const nextEdit = useRef(0);
  const labels = useMutation({
    mutationFn: (edit: LabelEdit) => api.editPullRequestLabels(project, edit.number, { add: edit.add.map((l) => l.name), remove: edit.remove }),
    onMutate: (edit) => setEdits((e) => [...e, edit]),
    onSuccess: (now, edit) => queryClient.setQueryData<T.ProjectPullRequests>(['pulls', project], (d) => d && withLabels(d, edit.number, now)),
    onError: (err, edit) => toast.error(t('memory.pulls.labels.failed', { number: edit.number, error: errorMessage(err) })),
    onSettled: (_now, _err, edit) => setEdits((e) => e.filter((x) => x.id !== edit.id)),
  });
  const toggleLabel = (pr: T.PullRequest, label: T.Label, on: boolean) =>
    labels.mutate({ id: nextEdit.current++, number: pr.number, add: on ? [label] : [], remove: on ? [] : [label.name] });

  const merge = useMutation({
    mutationFn: ({ number, method }: { number: number; method: string }) => api.mergePullRequest(project, number, method),
    onMutate: ({ number }) => {
      setRunning((r) => new Set(r).add(number));
      setFailed((f) => without(f, number));
    },
    onSuccess: async (pr) => {
      toast(t('memory.pulls.merged', { number: pr.number }), { description: pr.title });
      // The daemon answers with the list it had, this pull request marked
      // merged, and re-reads GitHub behind it; the list on screen stays
      // until that answer replaces it. The spinner lasts until then too, so
      // the row never flicks back to an open one with a Merge button.
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['pulls', project] }),
        queryClient.invalidateQueries({ queryKey: ['fleet', project] }),
      ]);
    },
    onError: (err, { number }) => setFailed((f) => ({ ...f, [number]: errorMessage(err) })),
    onSettled: (_pr, _err, { number }) =>
      setRunning((r) => {
        const next = new Set(r);
        next.delete(number);
        return next;
      }),
  });

  const data = pulls.data;
  if (!data) {
    return <div className="p-6 text-sm text-subtle">{pulls.error ? t('memory.pulls.unavailable') : t('common.loading')}</div>;
  }

  // Nothing to read, and the two reasons are different problems with
  // different fixes: a checkout that pushes nowhere, and one that pushes
  // somewhere that isn't GitHub. One message for both sent a user looking in
  // the wrong place for a long time (D57).
  if (!data.github && !data.githubError) {
    return (
      <div className="panel rounded-2xl">
        {data.noOrigin ? (
          <EmptyState icon={GitPullRequest} title={t('memory.pulls.noOrigin.title')}>
            {t.rich('memory.pulls.noOrigin.body', { code: (c) => <Code>{c}</Code> })}
          </EmptyState>
        ) : data.nonGitHubRemote ? (
          <EmptyState icon={GitPullRequest} title={t('memory.pulls.nonGitHub.title')}>
            {t.rich('memory.pulls.nonGitHub.body', { code: (c) => <Code>{c}</Code>, remote: <Code>{data.nonGitHubRemote}</Code> })}
          </EmptyState>
        ) : (
          <EmptyState icon={GitPullRequest} title={t('memory.pulls.notConnected.title')}>
            {t('memory.pulls.notConnected.body')}
          </EmptyState>
        )}
      </div>
    );
  }

  // Who the account is on GitHub, from the same place the Settings page lists
  // them. It is missing only for an account stored before AgentBox remembered
  // logins, and then the account's own name stands alone.
  const login = auth.data?.githubAccounts.find((a) => a.name === data.githubAccount)?.login;

  const prs = data.pullRequests;
  // Mine is who the project's GitHub account is; an account stored before
  // AgentBox remembered logins has none known, and no Mine chip.
  const me = data.githubLogin || login;
  const mineOn = mine && !!me;
  const scoped = mineOn ? byAuthor(prs, me) : prs;
  const open = byState(scoped, 'open');
  const others = byState(scoped, 'closed');
  const visible = byState(scoped, filter);
  const methods = data.mergeMethods?.length ? data.mergeMethods : allMethods;
  const canOfferMerge = (pr: T.PullRequest) => pr.state === 'open' && !pr.draft && (!data.canMergeKnown || !!data.canMerge);
  const viewed = prs.find((pr) => pr.number === viewing) ?? null;
  const refreshing = data.refreshing || refresh.isPending;

  return (
    <div className="flex flex-col gap-3">
      {data.github && (
        <p className="flex flex-wrap items-center gap-x-1.5 px-1 text-[12px] text-subtle" data-pulls-account={data.githubAccount}>
          <span className="font-mono text-muted">{data.github}</span>
          {data.githubAccount && (
            <>
              <span>{t('memory.pulls.as')}</span>
              <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenAccount}>
                {githubAccountLabel(data.githubAccount, login)}
              </button>
            </>
          )}
        </p>
      )}
      {data.githubError && (
        <Notice>
          {githubErrorSentence(data.githubError)} <GitHubErrorFix err={data.githubError} onOpenAccount={onOpenAccount} onOpenSetup={() => onSelect({ kind: 'settings' })} />
        </Notice>
      )}
      {data.canMergeKnown && !data.canMerge && (
        <Notice tone="info">{t('memory.pulls.cannotMerge', { repo: data.github ?? '' })}</Notice>
      )}

      {prs.length === 0 ? (
        // Nothing to say about a repository whose pull requests couldn't be
        // read: the error above already says why, and "no pull requests"
        // would read as an answer about the repository.
        !data.githubError && (
          <div className="panel rounded-2xl">
            {data.fetchedAt ? (
              <EmptyState icon={GitPullRequest} title={t('memory.pulls.none.title')}>
                {t('memory.pulls.none.body', { repo: data.github ?? '' })}
              </EmptyState>
            ) : (
              // Nothing has been read yet: an empty list here isn't an empty
              // repository, it's a repository nobody has asked about.
              <EmptyState icon={GitPullRequest} title={t('memory.pulls.reading.title')}>
                {t('memory.pulls.reading.body', { repo: data.github ?? '' })}
              </EmptyState>
            )}
          </div>
        )
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-1" data-pulls-filters>
            <FilterChip active={filter === 'open'} count={open.length} onClick={() => setFilter('open')}>
              {t('memory.pulls.filter.open')}
            </FilterChip>
            <FilterChip active={filter === 'closed'} count={others.length} onClick={() => setFilter('closed')}>
              {t('memory.pulls.filter.closed')}
            </FilterChip>
            <FilterChip active={filter === 'all'} count={scoped.length} onClick={() => setFilter('all')}>
              {t('memory.pulls.filter.all')}
            </FilterChip>
            {me && (
              <>
                <span className="mx-1 h-4 w-px bg-line-strong" aria-hidden />
                <Tip label={t('memory.pulls.filter.mineTip', { login: me })}>
                  <span data-pulls-mine={mineOn || undefined}>
                    <FilterChip icon={User} active={mineOn} count={byState(byAuthor(prs, me), filter).length} onClick={() => setMine(!mineOn)}>
                      {t('memory.pulls.filter.mine')}
                    </FilterChip>
                  </span>
                </Tip>
              </>
            )}
            <span className="ml-auto flex items-center gap-1.5 pr-1 text-[11.5px] text-faint" data-pulls-age data-refreshing={refreshing || undefined}>
              {refreshing ? (
                <>
                  <LoaderCircle className="size-3 animate-spin" />
                  {t('memory.pulls.refreshing')}
                </>
              ) : (
                data.fetchedAt && t('memory.pulls.readAgo', { when: timeAgo(data.fetchedAt) })
              )}
              <Tip label={t('memory.pulls.refresh')}>
                <button
                  type="button"
                  aria-label={t('memory.pulls.refresh')}
                  disabled={refreshing}
                  onClick={() => refresh.mutate()}
                  className="grid size-6 place-items-center rounded-md text-subtle transition hover:bg-surface hover:text-primary disabled:pointer-events-none disabled:opacity-40"
                  data-pulls-refresh
                >
                  <RotateCw className="size-3.5" />
                </button>
              </Tip>
            </span>
          </div>

          {visible.length === 0 ? (
            <p className="px-1 text-[13px] text-subtle">{t('memory.pulls.noMatch')}</p>
          ) : (
            <div className="flex flex-col gap-2">
              {visible.map((pr) => {
                return (
                  <PullRequestRow
                    key={pr.number}
                    pr={pr}
                    project={project}
                    labels={withEdits(pr.labels, pr.number, edits)}
                    onToggleLabel={(label, on) => toggleLabel(pr, label, on)}
                    canMerge={canOfferMerge(pr)}
                    mergeRunning={running.has(pr.number)}
                    mergeError={failed[pr.number]}
                    onOpenAgent={pr.agent ? () => onSelect({ kind: 'agent', ref: `${project}/${pr.agent}` }) : undefined}
                    onMerge={() => {
                      setMethod(methods[0]);
                      setMerging(pr);
                    }}
                    onOpen={() => setViewing(pr.number)}
                  />
                );
              })}
            </div>
          )}
        </>
      )}

      <PullRequestModal
        project={project}
        pr={viewed}
        onClose={() => setViewing(null)}
        canMerge={!!viewed && canOfferMerge(viewed)}
        mergeRunning={!!viewed && running.has(viewed.number)}
        mergeError={viewed ? failed[viewed.number] : undefined}
        onMerge={() => {
          setMethod(methods[0]);
          setMerging(viewed);
        }}
        onOpenAgent={viewed?.agent ? () => onSelect({ kind: 'agent', ref: `${project}/${viewed.agent}` }) : undefined}
      />

      <ConfirmDialog
        open={merging !== null}
        onOpenChange={(open) => !open && setMerging(null)}
        title={t('memory.pulls.confirm.title', { number: merging?.number ?? 0 })}
        description={
          merging && (
            <>
              {t.rich('memory.pulls.confirm.body', {
                title: merging.title,
                head: <span className="font-mono text-tertiary">{merging.headBranch}</span>,
                base: <span className="font-mono text-tertiary">{merging.baseBranch}</span>,
              })}
              {merging.checks === 'failing' && ` ${t('memory.pulls.confirm.failing')}`}
              {merging.checks === 'pending' && ` ${t('memory.pulls.confirm.pending')}`}
            </>
          )
        }
        confirmLabel={t('memory.pulls.merge')}
        onConfirm={async () => {
          if (merging) merge.mutate({ number: merging.number, method });
        }}
      >
        <label className="grid gap-1.5 text-[13px] text-tertiary">
          {t('memory.pulls.method')}
          <Select value={method} onChange={setMethod}>
            {methods.map((m) => (
              <SelectOption key={m} value={m}>
                {methodLabels[m] ? t(methodLabels[m]) : m}
              </SelectOption>
            ))}
          </Select>
        </label>
      </ConfirmDialog>
    </div>
  );
}

// GitHubErrorFix is what to do about it, in the same sentence: the two fixes
// are somewhere in this app, so they are links rather than advice.
function GitHubErrorFix({ err, onOpenAccount, onOpenSetup }: { err: T.GitHubError; onOpenAccount: () => void; onOpenSetup: () => void }) {
  const t = useT();
  const link = (onClick: () => void) => (c: ReactNode) => (
    <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onClick}>
      {c}
    </button>
  );
  const another = link(onOpenAccount);
  const setup = link(onOpenSetup);
  switch (err.kind) {
    case T.GitHubNoAccess:
      return <>{t.rich('memory.pulls.fix.another', { another, setup })}</>;
    case T.GitHubNoAccount:
      return err.account ? <>{t.rich('memory.pulls.fix.another', { another, setup })}</> : <>{t.rich('memory.pulls.fix.add', { setup })}</>;
    case T.GitHubBadToken:
      return <>{t.rich('memory.pulls.fix.badToken', { another, setup })}</>;
    default:
      return null;
  }
}

function without(errors: Readonly<Record<number, string>>, number: number): Record<number, string> {
  const rest = { ...errors };
  delete rest[number];
  return rest;
}

const stateVariants: Record<string, BadgeVariant> = { open: 'info', merged: 'success', closed: 'default' };
const checksVariants: Record<string, BadgeVariant> = { passing: 'success', failing: 'danger', pending: 'warning' };

// useMine is the Mine filter, remembered per project.
function useMine(project: string): [boolean, (on: boolean) => void] {
  const [state, setState] = useState(() => ({ project, on: readMine(project) }));
  const on = state.project === project ? state.on : readMine(project);
  return [
    on,
    (next) => {
      writeMine(project, next);
      setState({ project, on: next });
    },
  ];
}

function PullRequestRow({
  pr,
  project,
  labels,
  onToggleLabel,
  canMerge,
  mergeRunning,
  mergeError,
  onOpenAgent,
  onMerge,
  onOpen,
}: {
  pr: T.PullRequest;
  project: string;
  labels: T.Label[];
  onToggleLabel: (label: T.Label, on: boolean) => void;
  canMerge: boolean;
  mergeRunning: boolean;
  mergeError?: string;
  onOpenAgent?: () => void;
  onMerge: () => void;
  onOpen: () => void;
}) {
  const t = useT();
  return (
    // A click anywhere on the row but its buttons opens the pull request in the
    // app, its title included; the arrow beside the title still goes to GitHub.
    <div
      className="panel cursor-pointer rounded-2xl px-4 py-3.5 transition-colors hover:border-line-strong"
      data-pull-request={pr.number}
      onClick={(event) => {
        // The label picker's popover is portalled out of the row, but its clicks still bubble here.
        const target = event.target as Element;
        if (event.currentTarget.contains(target) && !target.closest('button, [data-external]')) {
          event.preventDefault();
          onOpen();
        }
      }}
    >
      <div className="flex flex-wrap items-start gap-3">
        <GitPullRequest className="mt-0.5 size-4 shrink-0 text-subtle" />
        <div className="min-w-0 flex-1">
          <a href={pr.url} target="_blank" rel="noreferrer" className="group flex min-w-0 items-center gap-1.5 text-[14px] font-medium text-primary hover:text-brand-300">
            <span className="truncate">{pr.title}</span>
            <ExternalLink data-external className="size-3.5 shrink-0 text-faint group-hover:text-brand-300" />
          </a>
          <p className="truncate font-mono text-[11.5px] text-subtle">
            #{pr.number} · {pr.headBranch} → {pr.baseBranch}
          </p>
          <PullRequestLabels project={project} labels={labels} onToggle={onToggleLabel} />
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
          {pr.draft ? (
            <Badge>{t('memory.pulls.draft')}</Badge>
          ) : (
            <Badge variant={stateVariants[pr.state] ?? 'default'}>{stateLabels[pr.state] ? t(stateLabels[pr.state]) : pr.state}</Badge>
          )}
          {pr.checks && (
            <Badge variant={checksVariants[pr.checks] ?? 'default'}>
              {t('memory.pulls.checks', { state: checksLabels[pr.checks] ? t(checksLabels[pr.checks]) : pr.checks })}
            </Badge>
          )}
          {pr.conflict && <Badge variant={checksVariants.failing}>{t('memory.pulls.conflicts')}</Badge>}
          {pr.review === 'changes_requested' && <Badge variant={checksVariants.pending}>{t('memory.pulls.changesRequested')}</Badge>}
        </div>
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-muted">
        {pr.author && (
          <span className="flex min-w-0 items-center gap-1.5">
            {pr.authorAvatar ? (
              <img src={pr.authorAvatar} alt="" className="size-4 shrink-0 rounded-full" />
            ) : (
              <User className="size-3.5 shrink-0" />
            )}
            <span className="min-w-0 truncate">{pr.author}</span>
          </span>
        )}
        {((pr.additions ?? 0) > 0 || (pr.deletions ?? 0) > 0) && (
          <span className="flex items-center gap-1.5">
            <GitBranch className="size-3.5" />
            <span className="text-emerald-400">+{pr.additions ?? 0}</span>
            <span className="text-rose-400">−{pr.deletions ?? 0}</span>
          </span>
        )}
        {(pr.comments ?? 0) > 0 && (
          <span className="flex items-center gap-1.5">
            <MessageSquare className="size-3.5" />
            {t('memory.pulls.comments', { count: pr.comments ?? 0 })}
          </span>
        )}
        {onOpenAgent && (
          <button type="button" onClick={onOpenAgent} className="font-medium text-brand-300 hover:underline">
            {t('memory.pulls.agentOf', { name: pr.agent ?? '' })}
          </button>
        )}
        <span className="ml-auto text-faint">{pr.updatedAt && timeAgo(pr.updatedAt)}</span>
        {canMerge && (
          <Button size="sm" variant="ghost" disabled={mergeRunning} onClick={onMerge} data-merge-running={mergeRunning || undefined}>
            {mergeRunning ? <LoaderCircle className="size-3.5 animate-spin" /> : <GitMerge className="size-3.5" />}
            {t('memory.pulls.merge')}
          </Button>
        )}
      </div>
      {mergeError && (
        <Notice className="mt-2.5">{t('memory.pulls.mergeFailed', { error: mergeError })}</Notice>
      )}
    </div>
  );
}

// PullRequestLabels is a pull request's labels in GitHub's colours, and the
// picker that puts the repository's labels on it and takes them off. Clicks
// in it stay in it, so they never reach whatever a click on the row does.
function PullRequestLabels({ project, labels, onToggle }: { project: string; labels: T.Label[]; onToggle: (label: T.Label, on: boolean) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-1" onClick={(e) => e.stopPropagation()} data-pull-labels>
      {labels.map((l) => (
        <LabelPill key={l.name} label={l} />
      ))}
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={labels.length ? t('memory.pulls.labels.edit') : t('memory.pulls.labels.add')}
            className={cn(
              'flex h-5 items-center gap-1 rounded-full border border-dashed border-line-strong px-1.5 text-[11px] text-subtle transition hover:border-line-vivid hover:text-primary',
              open && 'border-line-vivid text-primary',
            )}
            data-labels-edit
          >
            <Tag className="size-3" />
            {labels.length === 0 && t('memory.pulls.labels.add')}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-72 p-0">
          {open && <LabelPicker project={project} applied={labels} onToggle={onToggle} />}
        </PopoverContent>
      </Popover>
    </div>
  );
}

function LabelPill({ label }: { label: T.Label }) {
  const pill = (
    <span
      className="inline-flex h-5 max-w-48 items-center truncate rounded-full border px-2 text-[11px] font-medium leading-none"
      style={labelStyle(label.color)}
      data-label={label.name}
    >
      {label.name}
    </span>
  );
  return label.description ? <Tip label={label.description}>{pill}</Tip> : pill;
}

// LabelPicker lists the repository's labels, read with the project's GitHub
// account when it first opens and kept a few minutes after, with a search.
// Each click puts one on or takes it off at once; the picker stays open for
// the next.
function LabelPicker({ project, applied, onToggle }: { project: string; applied: T.Label[]; onToggle: (label: T.Label, on: boolean) => void }) {
  const t = useT();
  const all = useQuery({ queryKey: ['labels', project], queryFn: () => api.projectLabels(project), staleTime: 5 * 60_000 });
  const [query, setQuery] = useState('');
  const shown = matchLabels(all.data?.labels ?? [], query);
  const has = (l: T.Label) => applied.some((a) => a.name === l.name);
  return (
    <div className="grid" data-label-picker>
      <div className="border-b border-line p-2">
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && shown[0]) onToggle(shown[0], !has(shown[0]));
          }}
          placeholder={t('memory.pulls.labels.search')}
          className="h-8 w-full rounded-md border border-line-strong bg-sunken px-2.5 text-[12.5px] text-primary placeholder:text-faint focus-visible:border-brand-400/60 focus-visible:outline-none"
        />
      </div>
      <div className="max-h-72 overflow-y-auto p-1">
        {all.isPending ? (
          <p className="flex items-center gap-2 px-2 py-2 text-[12px] text-subtle">
            <LoaderCircle className="size-3.5 animate-spin" />
            {t('memory.pulls.labels.loading')}
          </p>
        ) : all.error ? (
          <p className="px-2 py-2 text-[12px] text-rose-300">{t('memory.pulls.labels.unavailable', { error: errorMessage(all.error) })}</p>
        ) : shown.length === 0 ? (
          <p className="px-2 py-2 text-[12px] text-subtle">{all.data?.labels.length ? t('memory.pulls.labels.noMatch') : t('memory.pulls.labels.none')}</p>
        ) : (
          shown.map((l) => {
            const on = has(l);
            return (
              <button
                key={l.name}
                type="button"
                aria-pressed={on}
                onClick={() => onToggle(l, !on)}
                className="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition hover:bg-surface"
                data-label-option={l.name}
              >
                <Check className={cn('mt-0.5 size-3.5 shrink-0 text-brand-300', !on && 'invisible')} />
                <span className="mt-[3px] size-2.5 shrink-0 rounded-full" style={{ backgroundColor: labelHex(l.color) }} />
                <span className="grid min-w-0">
                  <span className="truncate text-[12.5px] text-primary">{l.name}</span>
                  {l.description && <span className="truncate text-[11px] text-subtle">{l.description}</span>}
                </span>
              </button>
            );
          })
        )}
      </div>
    </div>
  );
}
