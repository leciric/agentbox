import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ExternalLink, GitBranch, GitMerge, GitPullRequest, MessageSquare, User } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { errorMessage, githubAccountLabel, githubErrorSentence, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { FilterChip } from './MediaTab';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Code, EmptyState, Notice } from './ui/card';
import { Select, SelectOption } from './ui/select';

type Filter = 'open' | 'closed' | 'all';

const methodLabels: Record<string, string> = { merge: 'Merge commit', squash: 'Squash and merge', rebase: 'Rebase and merge' };
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
  const queryClient = useQueryClient();
  // The daemon answers from its cache and re-reads GitHub behind the answer,
  // announcing what moved on the event stream, so this polls as a backstop
  // rather than as the way the list stays current (D54).
  const pulls = useQuery({ queryKey: ['pulls', project], queryFn: () => api.projectPullRequests(project), refetchInterval: 60_000 });
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const [filter, setFilter] = useState<Filter>('open');
  const [merging, setMerging] = useState<T.PullRequest | null>(null);
  const [method, setMethod] = useState('merge');

  const merge = useMutation({
    mutationFn: ({ number, method }: { number: number; method: string }) => api.mergePullRequest(project, number, method),
    onSuccess: async (pr) => {
      toast(`Merged #${pr.number}`, { description: pr.title });
      await queryClient.invalidateQueries({ queryKey: ['pulls', project] });
      await queryClient.invalidateQueries({ queryKey: ['fleet', project] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const data = pulls.data;
  if (!data) {
    return <div className="p-6 text-sm text-subtle">{pulls.error ? 'Pull requests are unavailable.' : 'Loading…'}</div>;
  }

  // Nothing to read, and the two reasons are different problems with
  // different fixes: a checkout that pushes nowhere, and one that pushes
  // somewhere that isn't GitHub. One message for both sent a user looking in
  // the wrong place for a long time (D57).
  if (!data.github && !data.githubError) {
    return (
      <div className="panel rounded-2xl">
        {data.noOrigin ? (
          <EmptyState icon={GitPullRequest} title="This project has no origin remote">
            Its checkout doesn’t push anywhere yet. Give it a GitHub <Code>origin</Code>, and AgentBox will read this repository’s pull requests.
          </EmptyState>
        ) : data.nonGitHubRemote ? (
          <EmptyState icon={GitPullRequest} title="This project’s origin isn’t on GitHub">
            Its <Code>origin</Code> is <Code>{data.nonGitHubRemote}</Code>, which doesn’t resolve to a repository on github.com. Everything else about the project works;
            pull requests are GitHub’s.
          </EmptyState>
        ) : (
          <EmptyState icon={GitPullRequest} title="Not connected to GitHub">
            AgentBox found no GitHub repository for this project, so there are no pull requests to read.
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
  const open = prs.filter((pr) => pr.state === 'open');
  const others = prs.filter((pr) => pr.state !== 'open');
  const visible = filter === 'open' ? open : filter === 'closed' ? others : prs;
  const methods = data.mergeMethods?.length ? data.mergeMethods : allMethods;

  return (
    <div className="flex flex-col gap-3">
      {data.github && (
        <p className="flex flex-wrap items-center gap-x-1.5 px-1 text-[12px] text-subtle" data-pulls-account={data.githubAccount}>
          <span className="font-mono text-muted">{data.github}</span>
          {data.githubAccount && (
            <>
              <span>as</span>
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
        <Notice tone="info">This GitHub token can read {data.github}, but can't merge into it. Merging needs a token with write access.</Notice>
      )}

      {prs.length === 0 ? (
        // Nothing to say about a repository whose pull requests couldn't be
        // read: the error above already says why, and "no pull requests"
        // would read as an answer about the repository.
        !data.githubError && (
          <div className="panel rounded-2xl">
            {data.fetchedAt ? (
              <EmptyState icon={GitPullRequest} title="No pull requests">
                {data.github} has none open, closed or merged yet.
              </EmptyState>
            ) : (
              // Nothing has been read yet: an empty list here isn't an empty
              // repository, it's a repository nobody has asked about.
              <EmptyState icon={GitPullRequest} title="Reading pull requests">
                Asking GitHub what {data.github} has. They appear as soon as it answers.
              </EmptyState>
            )}
          </div>
        )
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-1" data-pulls-filters>
            <FilterChip active={filter === 'open'} count={open.length} onClick={() => setFilter('open')}>
              Open
            </FilterChip>
            <FilterChip active={filter === 'closed'} count={others.length} onClick={() => setFilter('closed')}>
              Closed &amp; merged
            </FilterChip>
            <FilterChip active={filter === 'all'} count={prs.length} onClick={() => setFilter('all')}>
              All
            </FilterChip>
            <span className="ml-auto pr-1 text-[11.5px] text-faint" data-pulls-age>
              {data.refreshing ? 'Reading GitHub…' : data.fetchedAt ? `Read ${timeAgo(data.fetchedAt)}` : ''}
            </span>
          </div>

          {visible.length === 0 ? (
            <p className="px-1 text-[13px] text-subtle">Nothing matches that filter.</p>
          ) : (
            <div className="flex flex-col gap-2">
              {visible.map((pr) => {
                const canOfferMerge = pr.state === 'open' && !pr.draft && (!data.canMergeKnown || !!data.canMerge);
                return (
                  <PullRequestRow
                    key={pr.number}
                    pr={pr}
                    canMerge={canOfferMerge}
                    onOpenAgent={pr.agent ? () => onSelect({ kind: 'agent', ref: `${project}/${pr.agent}` }) : undefined}
                    onMerge={() => {
                      setMethod(methods[0]);
                      setMerging(pr);
                    }}
                  />
                );
              })}
            </div>
          )}
        </>
      )}

      <ConfirmDialog
        open={merging !== null}
        onOpenChange={(open) => !open && setMerging(null)}
        title={`Merge #${merging?.number}?`}
        description={
          merging && (
            <>
              &ldquo;{merging.title}&rdquo; merges <span className="font-mono text-tertiary">{merging.headBranch}</span> into{' '}
              <span className="font-mono text-tertiary">{merging.baseBranch}</span>.
              {merging.checks === 'failing' && ' Its checks are failing.'}
              {merging.checks === 'pending' && ' Its checks are still running.'}
            </>
          )
        }
        confirmLabel="Merge"
        onConfirm={async () => {
          if (!merging) return;
          await merge.mutateAsync({ number: merging.number, method });
        }}
      >
        <label className="grid gap-1.5 text-[13px] text-tertiary">
          Merge method
          <Select value={method} onChange={setMethod}>
            {methods.map((m) => (
              <SelectOption key={m} value={m}>
                {methodLabels[m] ?? m}
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
  const another = (
    <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenAccount}>
      Pick another account for this project
    </button>
  );
  const setup = (
    <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenSetup}>
      add one in Settings
    </button>
  );
  switch (err.kind) {
    case T.GitHubNoAccess:
      return (
        <>
          {another}, or {setup}.
        </>
      );
    case T.GitHubNoAccount:
      return err.account ? (
        <>
          {another}, or {setup}.
        </>
      ) : (
        <>
          <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenSetup}>
            Add one in Settings
          </button>
          .
        </>
      );
    case T.GitHubBadToken:
      return (
        <>
          <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenSetup}>
            Save its token again in Settings
          </button>
          , or {another}.
        </>
      );
    default:
      return null;
  }
}

const stateVariants: Record<string, BadgeVariant> = { open: 'info', merged: 'success', closed: 'default' };
const checksVariants: Record<string, BadgeVariant> = { passing: 'success', failing: 'danger', pending: 'warning' };

function PullRequestRow({
  pr,
  canMerge,
  onOpenAgent,
  onMerge,
}: {
  pr: T.PullRequest;
  canMerge: boolean;
  onOpenAgent?: () => void;
  onMerge: () => void;
}) {
  return (
    <div className="panel rounded-2xl px-4 py-3.5" data-pull-request={pr.number}>
      <div className="flex flex-wrap items-start gap-3">
        <GitPullRequest className="mt-0.5 size-4 shrink-0 text-subtle" />
        <div className="min-w-0 flex-1">
          <a href={pr.url} target="_blank" rel="noreferrer" className="group flex min-w-0 items-center gap-1.5 text-[14px] font-medium text-primary hover:text-brand-300">
            <span className="truncate">{pr.title}</span>
            <ExternalLink className="size-3.5 shrink-0 text-faint group-hover:text-brand-300" />
          </a>
          <p className="truncate font-mono text-[11.5px] text-subtle">
            #{pr.number} · {pr.headBranch} → {pr.baseBranch}
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
          {pr.draft ? <Badge>draft</Badge> : <Badge variant={stateVariants[pr.state] ?? 'default'}>{pr.state}</Badge>}
          {pr.checks && <Badge variant={checksVariants[pr.checks] ?? 'default'}>checks {pr.checks}</Badge>}
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
            {pr.comments} comment{pr.comments === 1 ? '' : 's'}
          </span>
        )}
        {onOpenAgent && (
          <button type="button" onClick={onOpenAgent} className="font-medium text-brand-300 hover:underline">
            {pr.agent}&apos;s agent
          </button>
        )}
        <span className="ml-auto text-faint">{pr.updatedAt && timeAgo(pr.updatedAt)}</span>
        {canMerge && (
          <Button size="sm" variant="ghost" onClick={onMerge}>
            <GitMerge className="size-3.5" />
            Merge
          </Button>
        )}
      </div>
    </div>
  );
}
