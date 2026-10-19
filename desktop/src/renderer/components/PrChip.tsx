import { GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, GitMerge } from 'lucide-react';
import type * as T from '../../shared/api';
import { prChecksText, prState } from '../lib/agentStatus';
import { useT } from '../lib/i18n';
import { cn } from '../lib/utils';

const checkMark: Record<string, string> = { passing: '✓', failing: '✕', pending: '•' };
const checkTone: Record<string, string> = { passing: 'text-emerald-300', failing: 'text-rose-300', pending: 'text-amber-300' };

// The chip's colours by what the pull request is: open, draft, merged or
// closed. Failing checks turn an open one red, the thing to look at.
function tone(pr: Pick<T.PullRequest, 'state' | 'draft' | 'checks'>): { icon: typeof GitPullRequest; cls: string } {
  if (pr.state === 'merged') return { icon: GitMerge, cls: 'bg-violet-400/10 text-violet-300 ring-violet-400/25 hover:bg-violet-400/20' };
  if (pr.state === 'closed') return { icon: GitPullRequestClosed, cls: 'bg-rose-400/10 text-rose-300 ring-rose-400/25 hover:bg-rose-400/20' };
  if (pr.draft) return { icon: GitPullRequestDraft, cls: 'bg-surface-raised text-muted ring-line-strong hover:bg-surface-vivid hover:text-primary' };
  if (pr.checks === 'failing') return { icon: GitPullRequest, cls: 'bg-rose-400/10 text-rose-300 ring-rose-400/30 hover:bg-rose-400/20' };
  return { icon: GitPullRequest, cls: 'bg-emerald-400/10 text-emerald-300 ring-emerald-400/25 hover:bg-emerald-400/20' };
}

// PrChip is a pull request as a small pill in the agent lists: its icon, its
// number and, when it has them, its checks, in the colour of its state. It
// opens the pull request on GitHub in the system browser, without opening the
// row it sits in. dataAttr names it for the list it's in.
export function PrChip({ pr, className, dataAttr = 'data-pr-chip' }: { pr: Pick<T.PullRequest, 'number' | 'url' | 'title' | 'state' | 'draft' | 'checks' | 'conflict'>; className?: string; dataAttr?: string }) {
  const t = useT();
  const { icon: Icon, cls } = tone(pr);
  const state = prState(pr);
  const title = [`#${pr.number} ${state}`, pr.checks && t('agent.pr.checks', { checks: prChecksText(pr.checks) }), pr.conflict && t('agent.pr.conflictsBase'), pr.title].filter(Boolean).join(' · ');
  const classes = cn('inline-flex shrink-0 items-center gap-1 rounded-full px-1.5 py-px font-mono text-[10.5px] leading-4 ring-1 ring-inset transition', cls, className);
  const body = (
    <>
      <Icon className="size-3 shrink-0" />#{pr.number}
      {pr.checks && <span className={checkTone[pr.checks]}>{checkMark[pr.checks]}</span>}
      {pr.conflict && <span className="text-rose-300" aria-label={t('agent.pr.conflicts')}>⚠</span>}
    </>
  );
  const attrs = { [dataAttr]: pr.number, title, 'aria-label': t('agent.pr.label', { number: pr.number, state }) };
  if (!pr.url) return <span className={classes} {...attrs}>{body}</span>;
  return (
    <a
      href={pr.url}
      className={cn(classes, 'cursor-pointer')}
      {...attrs}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        void window.agentbox.openExternal(pr.url);
      }}
    >
      {body}
    </a>
  );
}
