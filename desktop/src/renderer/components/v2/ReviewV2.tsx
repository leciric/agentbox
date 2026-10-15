// UI v2's review: #301's carousel (ReadyToReview.tsx), opened from the inbox, as
// in v1, from the sidebar and the lead chat's "N to review", and from an
// agent's own view, whose card (AgentReviewCard) says what it made. Its one
// set of actions is the carousel's footer, the same in both layouts.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, CircleCheck, Film, GitMerge, GitPullRequest, Globe, Images, LoaderCircle } from 'lucide-react';
import { createContext, useContext, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { agentReviewPrefix, useReviewPages } from '../../lib/inboxPages';
import { changesPending } from '../../lib/leadFirst';
import { startWalk } from '../../lib/review';
import { useMediaCounts } from '../../lib/mediaPages';
import { cn, errorMessage } from '../../lib/utils';
import { ReviewDialog, type ReviewSession } from '../ReadyToReview';
import { Button } from '../ui/button';

// OpenReview opens the carousel, the one way in: on an agent (item, the one
// an agent's card shows), or walking through every agent waiting for review,
// or one project's (the sidebar's "N to review", the lead chat's banner;
// Home's "all" can too). Nothing is marked seen or reviewed until the
// carousel has shown an agent, and only its buttons record a verdict.
export type OpenReview = (opts?: { project?: string; item?: T.ReviewItem }) => void;
const OpenReviewContext = createContext<OpenReview | null>(null);

// useOpenReview is openReview, under ReviewV2.
export const useOpenReview = () => useContext(OpenReviewContext);

// projectReviewKey is one project's agents ready to review: a page under
// ['review'], so refreshInbox refreshes it and opening one drops it at once.
const projectReviewKey = (project: string) => ['review', 'project', project];
const readyFor = (project?: string) => api.readyToReviewPage(new URLSearchParams({ limit: '200' }), project);

// useProjectReview is the agents of a project waiting for review.
export function useProjectReview(project: string, enabled = true) {
  return useQuery({ queryKey: projectReviewKey(project), queryFn: () => readyFor(project), refetchInterval: 60_000, enabled });
}

// agentReviewKey is one agent's review item, wherever it is: ready, or
// reviewed on the work as it still is. It is refetched as the lists are
// (lib/inboxPages.ts refreshInbox, lib/events.ts), but kept out of ['review'],
// whose entries are all pages.
const agentReviewKey = (ref: string) => [agentReviewPrefix, ref];

async function agentReview(ref: string): Promise<T.ReviewItem | null> {
  const project = ref.split('/')[0];
  const q = new URLSearchParams({ limit: '200' });
  const [ready, reviewed] = await Promise.all([api.readyToReviewPage(q, project), api.reviewedPage(q, project)]);
  return ready.items.find((i) => i.ref === ref) ?? reviewed.items.find((i) => i.ref === ref) ?? null;
}

// useAgentReview is an agent's review item, or null while it has none: it
// hasn't finished, or is at work again.
export function useAgentReview(ref: string) {
  return useQuery({ queryKey: agentReviewKey(ref), queryFn: () => agentReview(ref) });
}

// ReviewV2 gives what's under it a way to open the carousel on any agent.
export function ReviewV2({ onSelect, children }: { onSelect: (view: View) => void; children: ReactNode }) {
  const queryClient = useQueryClient();
  const [session, setSession] = useState<ReviewSession | null>(null);
  const pages = useReviewPages(false, session !== null);
  const openReview: OpenReview = (opts = {}) => {
    if (opts.item) return setSession(startWalk([], opts));
    const { project } = opts;
    const page = project ? queryClient.fetchQuery({ queryKey: projectReviewKey(project), queryFn: () => readyFor(project), staleTime: 0 }) : readyFor();
    void page.then(({ items }) => setSession(startWalk(items ?? [], opts))).catch((err) => toast.error(errorMessage(err)));
  };
  return (
    <OpenReviewContext.Provider value={openReview}>
      {children}
      <ReviewDialog session={session} pages={pages} onSession={setSession} onSelect={onSelect} />
    </OpenReviewContext.Provider>
  );
}

// AgentReviewCard sits above a finished agent's composer: what it made, its
// pull request, and Review changes, which opens the carousel on it. While the
// agent works on changes asked for, it says so instead.
export function AgentReviewCard({ agent }: { agent: T.Agent }) {
  const t = useT();
  const open = useOpenReview();
  const review = useAgentReview(agent.ref);
  const counts = useMediaCounts({ project: agent.project, agent: agent.name });
  const item = review.data;
  const asked = changesPending(agent);
  if (!open || (!item && !asked)) return null;
  const kinds = counts.data?.kinds ?? {};
  const made = [
    kinds.screenshot ? { icon: Images, text: t('v2.card.screenshots', { count: kinds.screenshot }) } : null,
    kinds.recording ? { icon: Film, text: t('v2.card.recordings', { count: kinds.recording }) } : null,
    kinds.page ? { icon: Globe, text: t('v2.card.pages', { count: kinds.page }) } : null,
  ].filter((x) => x !== null);
  const pr = item?.pr;
  const prTone = !pr ? '' : pr.state === 'merged' ? 'text-violet-300' : pr.checks === 'failing' || pr.conflict ? 'text-rose-300' : 'text-secondary';
  return (
    <div className="mb-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-xl border border-line-strong bg-overlay px-3 py-2 shadow-lg backdrop-blur-xl" data-review-card={agent.ref}>
      <span className="flex min-w-0 items-center gap-2">
        {asked ? <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" /> : <CircleCheck className="size-4 shrink-0 text-subtle" />}
        <span className="shrink-0 text-[12.5px] font-medium text-title">
          {asked ? t('v2.card.requested') : item?.verdict ? t('v2.card.reviewed') : t('v2.card.ready')}
        </span>
      </span>
      <span className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-subtle">
        {made.map(({ icon: Icon, text }) => (
          <span key={text} className="inline-flex items-center gap-1">
            <Icon className="size-3.5 text-faint" />
            {text}
          </span>
        ))}
        {pr && (
          <span className={cn('inline-flex items-center gap-1', prTone)} data-review-card-pr={pr.number}>
            {pr.state === 'merged' ? <GitMerge className="size-3.5" /> : <GitPullRequest className="size-3.5" />}#{pr.number} {prLabel(t, pr)}
          </span>
        )}
      </span>
      {item && (
        <Button size="sm" variant={asked ? 'secondary' : 'primary'} className="ml-auto h-7" onClick={() => open?.({ item })} data-review-open>
          {t('v2.card.review')}
          <ArrowRight />
        </Button>
      )}
    </div>
  );
}

function prLabel(t: ReturnType<typeof useT>, pr: T.PullRequest): string {
  if (pr.state === 'merged') return t('v2.card.prMerged');
  if (pr.state === 'closed') return t('v2.card.prClosed');
  if (pr.draft) return t('memory.pulls.draft');
  if (pr.conflict) return t('memory.pulls.conflicts');
  if (pr.checks === 'failing') return t('v2.card.checksFailing');
  if (pr.checks === 'passing') return t('v2.card.checksPass');
  return t('v2.card.prOpen');
}


// ReviewCount is a project's "N to review", drawn only while it has agents
// waiting: the sidebar's button on its lead (compact) and the lead chat's
// banner above the composer. Both walk the carousel through that project's
// agents, one after another (openReview).
export function ReviewCount({ project, variant }: { project: string; variant: 'button' | 'banner' }) {
  const t = useT();
  const openReview = useOpenReview();
  const ready = useProjectReview(project, !!openReview);
  const count = ready.data?.items?.length ?? 0;
  if (!openReview || count === 0) return null;
  const open = (e: { stopPropagation: () => void }) => {
    e.stopPropagation();
    openReview({ project });
  };
  if (variant === 'button') {
    return (
      <button
        type="button"
        onClick={open}
        className="mt-0.5 shrink-0 rounded-full border border-line-strong bg-surface px-1.5 py-px text-[10.5px] font-medium tabular-nums text-secondary transition hover:border-line-heavy hover:bg-surface-strong hover:text-title"
        data-v2-review-button={project}
      >
        {t('v2.review.toReview', { count })}
      </button>
    );
  }
  return (
    <div className="mx-auto mb-2 flex w-full max-w-3xl items-center gap-2 rounded-xl border border-line bg-surface-faint px-3 py-1.5 text-[12.5px]" data-v2-review-banner={project}>
      <span className="size-1.5 shrink-0 rounded-full ring-1 ring-inset ring-secondary" aria-hidden />
      <span className="min-w-0 flex-1 truncate text-secondary">{t('v2.review.readyBanner', { count })}</span>
      <button type="button" onClick={open} className="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-0.5 font-medium text-title hover:bg-surface-strong" data-v2-review-banner-open>
        {t('v2.review.review')}
        <ArrowRight className="size-3.5" />
      </button>
    </div>
  );
}
