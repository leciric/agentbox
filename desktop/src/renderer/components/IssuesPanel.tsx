import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bot, CircleDot, ExternalLink, GitPullRequest, LoaderCircle, MessageSquare, RotateCw, Sparkles, User } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { labelStyle } from '../lib/pulls';
import { errorMessage, githubAccountLabel, githubErrorSentence, timeAgo } from '../lib/utils';
import { FilterChip } from './MediaTab';
import { GitHubErrorFix, stateLabels, stateVariants } from './PullRequestsPanel';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { EmptyState, Notice } from './ui/card';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

type IssuesFilter = 'waiting' | 'handed' | 'all';

// handedNow is whether an issue is with an agent that is still there, or
// being made: only those can't be handed again.
export function handedNow(issue: T.Issue): boolean {
  return !!issue.handed && !issue.handed.retired;
}

// filterIssues is the issues a filter chip shows.
export function filterIssues(issues: T.Issue[], filter: IssuesFilter): T.Issue[] {
  if (filter === 'waiting') return issues.filter((i) => !handedNow(i));
  if (filter === 'handed') return issues.filter(handedNow);
  return issues;
}

// IssuesPanel lists a project repository's open GitHub issues, read with the
// project's GitHub account like its pull requests, and hands one to a new
// agent whose task is the issue and whose pull request closes it. An issue
// already handed over shows its agent, and the pull request that agent opened.
export function IssuesPanel({ project, onSelect, onOpenAccount }: { project: string; onSelect: (view: View) => void; onOpenAccount: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  // Every minute, or every few seconds while an agent is being made, so the
  // issue goes from "starting" to the agent's name without a refresh.
  const issues = useQuery({
    queryKey: ['issues', project],
    queryFn: () => api.projectIssues(project),
    refetchInterval: (query) => (query.state.data?.issues.some((i) => i.handed && !i.handed.agent && !i.handed.retired) ? 4_000 : 60_000),
    refetchOnWindowFocus: true,
  });
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const [filter, setFilter] = useState<IssuesFilter>('waiting');
  const refresh = useMutation({
    mutationFn: () => api.projectIssues(project, true),
    onSuccess: (data) => queryClient.setQueryData(['issues', project], data),
  });
  const [handing, setHanding] = useState<number | null>(null);
  const hand = useMutation({
    mutationFn: (issue: T.Issue) => api.createAgent({ project, issue: issue.number, ai: '' }),
    onMutate: (issue) => setHanding(issue.number),
    onSuccess: async (_job, issue) => {
      toast(t('issues.handedToast', { number: issue.number }));
      await Promise.all([queryClient.invalidateQueries({ queryKey: ['issues', project] }), queryClient.invalidateQueries({ queryKey: ['agents'] })]);
    },
    onError: (err) => toast.error(errorMessage(err)),
    onSettled: () => setHanding(null),
  });
  const pickUp = useMutation({
    mutationFn: (pickUpIssues: boolean) => api.updateProject(project, { pickUpIssues }),
    onSuccess: async (updated) => {
      toast(updated.pickUpIssues ? t('defaults.project.pickUpOnToast', { name: updated.name }) : t('defaults.project.pickUpOffToast', { name: updated.name }));
      await Promise.all([queryClient.invalidateQueries({ queryKey: ['projects'] }), queryClient.invalidateQueries({ queryKey: ['issues', project] })]);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const data = issues.data;
  if (!data) {
    return <div className="p-6 text-sm text-subtle">{issues.error ? errorMessage(issues.error) : t('common.loading')}</div>;
  }
  if (!data.github && !data.githubError) {
    return (
      <div className="panel rounded-2xl">
        <EmptyState icon={CircleDot} title={t('issues.noRepo.title')}>
          {t('issues.noRepo.body')}
        </EmptyState>
      </div>
    );
  }

  const login = auth.data?.githubAccounts.find((a) => a.name === data.githubAccount)?.login;
  const waiting = filterIssues(data.issues, 'waiting');
  const handed = filterIssues(data.issues, 'handed');
  const visible = filterIssues(data.issues, filter);
  const refreshing = refresh.isPending;

  return (
    <div className="flex flex-col gap-3" data-issues>
      {data.github && (
        <p className="flex flex-wrap items-center gap-x-1.5 px-1 text-[12px] text-subtle">
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
      {data.github && (
        <label className="panel flex cursor-pointer items-center gap-3 rounded-2xl px-4 py-3" data-issues-pickup={data.pickUpIssues || undefined}>
          <Sparkles className="size-4 shrink-0 text-subtle" />
          <span className="min-w-0 flex-1">
            <span className="block text-[13px] font-medium text-primary">{t('issues.pickupSwitch')}</span>
            <span className="block text-[12px] text-subtle">
              {t.rich(data.pickUpIssues ? 'issues.pickupOn' : 'issues.pickupOff', {
                label: (
                  <span className="inline-flex h-[18px] items-center rounded-full border px-1.5 align-middle text-[11px] font-medium leading-none" style={labelStyle('0e8a16')}>
                    {data.label}
                  </span>
                ),
              })}
            </span>
          </span>
          <Switch checked={data.pickUpIssues} disabled={pickUp.isPending} onCheckedChange={(on) => pickUp.mutate(on)} aria-label={t('defaults.project.pickUpLabel')} />
        </label>
      )}

      {data.issues.length === 0 ? (
        !data.githubError && (
          <div className="panel rounded-2xl">
            <EmptyState icon={CircleDot} title={t('issues.none.title')}>
              {t('issues.none.body', { repo: data.github ?? '' })}
            </EmptyState>
          </div>
        )
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-1" data-issues-filters>
            <FilterChip active={filter === 'waiting'} count={waiting.length} onClick={() => setFilter('waiting')}>
              {t('issues.filter.waiting')}
            </FilterChip>
            <FilterChip active={filter === 'handed'} count={handed.length} onClick={() => setFilter('handed')}>
              {t('issues.filter.handed')}
            </FilterChip>
            <FilterChip active={filter === 'all'} count={data.issues.length} onClick={() => setFilter('all')}>
              {t('issues.filter.all')}
            </FilterChip>
            <span className="ml-auto flex items-center gap-1.5 pr-1 text-[11.5px] text-faint">
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
                >
                  <RotateCw className="size-3.5" />
                </button>
              </Tip>
            </span>
          </div>
          {visible.length === 0 ? (
            <p className="px-1 text-[13px] text-subtle">{t('issues.noMatch')}</p>
          ) : (
            <div className="flex flex-col gap-2">
              {visible.map((issue) => (
                <IssueRow
                  key={issue.number}
                  issue={issue}
                  handing={handing === issue.number}
                  onHand={() => hand.mutate(issue)}
                  onOpenAgent={issue.handed?.agent && !issue.handed.retired ? () => onSelect({ kind: 'agent', ref: `${project}/${issue.handed!.agent}` }) : undefined}
                />
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function IssueRow({ issue, handing, onHand, onOpenAgent }: { issue: T.Issue; handing: boolean; onHand: () => void; onOpenAgent?: () => void }) {
  const t = useT();
  const h = issue.handed;
  const pr = h?.pullRequest;
  return (
    <div className="panel min-w-0 rounded-2xl px-4 py-3.5" data-issue={issue.number} data-issue-agent={h?.agent || undefined}>
      <div className="flex min-w-0 items-start gap-3">
        <CircleDot className="mt-0.5 size-4 shrink-0 text-emerald-400" />
        <div className="min-w-0 flex-1">
          <a href={issue.url} target="_blank" rel="noreferrer" className="group flex min-w-0 items-center gap-1.5 text-[14px] font-medium text-primary hover:text-brand-300">
            <span className="truncate">{issue.title}</span>
            <ExternalLink className="size-3.5 shrink-0 text-faint group-hover:text-brand-300" />
          </a>
          <p className="truncate font-mono text-[11.5px] text-subtle">#{issue.number}</p>
          {issue.labels && issue.labels.length > 0 && (
            <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-1">
              {issue.labels.map((l) => (
                <span key={l.name} className="inline-flex h-5 max-w-48 items-center rounded-full border px-2 text-[11px] font-medium leading-none" style={labelStyle(l.color)} title={l.description}>
                  <span className="truncate">{l.name}</span>
                </span>
              ))}
            </div>
          )}
        </div>
      </div>
      <div className="mt-2.5 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-muted">
        {issue.author && (
          <span className="flex min-w-0 items-center gap-1.5">
            {issue.authorAvatar ? <img src={issue.authorAvatar} alt="" className="size-4 shrink-0 rounded-full" /> : <User className="size-3.5 shrink-0" />}
            <span className="min-w-0 truncate">{issue.author}</span>
          </span>
        )}
        {(issue.comments ?? 0) > 0 && (
          <span className="flex items-center gap-1.5">
            <MessageSquare className="size-3.5" />
            {t('memory.pulls.comments', { count: issue.comments ?? 0 })}
          </span>
        )}
        {h && (
          <span className="flex min-w-0 items-center gap-1.5" data-issue-handed>
            <Bot className="size-3.5 shrink-0" />
            {!h.agent ? (
              <span className="flex items-center gap-1.5">
                <LoaderCircle className="size-3 animate-spin" />
                {t('issues.starting')}
              </span>
            ) : onOpenAgent ? (
              <button type="button" onClick={onOpenAgent} className="min-w-0 truncate font-medium text-brand-300 hover:underline" title={h.agentTitle}>
                {t('issues.with', { name: h.agent })}
              </button>
            ) : (
              <span className="min-w-0 truncate">{t('issues.retired', { name: h.agent })}</span>
            )}
            {h.auto && <Badge>{t('issues.auto')}</Badge>}
          </span>
        )}
        {pr && (
          <a href={pr.url} target="_blank" rel="noreferrer" className="flex items-center gap-1.5 hover:text-brand-300" data-issue-pr={pr.number}>
            <GitPullRequest className="size-3.5" />
            {t('issues.pullRequest', { number: pr.number })}
            <Badge variant={stateVariants[pr.state] ?? 'default'}>{stateLabels[pr.state] ? t(stateLabels[pr.state]) : pr.state}</Badge>
          </a>
        )}
        <span className="ml-auto text-faint">{issue.updatedAt && timeAgo(issue.updatedAt)}</span>
        {!handedNow(issue) && (
          <Tip label={t('issues.handTip', { number: issue.number })}>
            <Button size="sm" variant="ghost" disabled={handing} onClick={onHand} data-issue-hand={issue.number}>
              {handing ? <LoaderCircle className="size-3.5 animate-spin" /> : <Bot className="size-3.5" />}
              {h ? t('issues.handAgain') : t('issues.hand')}
            </Button>
          </Tip>
        )}
      </div>
    </div>
  );
}
