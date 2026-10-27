// UI v2's review: #301's carousel (ReadyToReview.tsx), opened from an
// agent's own view, whose card (AgentReviewCard) says what it made, and from
// the lead chat's evidence cards. Its one set of actions is the carousel's
// footer, the same in both layouts. "N to review", on the sidebar and above
// the lead chat's composer, opens the Work page (WorkView) on what waits,
// which also lists what was reviewed, and is where a walk through them starts.
import { useQuery } from '@tanstack/react-query';
import { PrNumberLink } from '../PrNumberLink';
import { ArrowRight, CircleCheck, Film, GitMerge, GitPullRequest, Globe, Images, LoaderCircle, Moon, Play } from 'lucide-react';
import { createContext, useContext, useState, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { agentReviewPrefix, useReviewPages } from '../../lib/inboxPages';
import { changesPending } from '../../lib/leadFirst';
import { startWalk } from '../../lib/review';
import { useMediaCounts } from '../../lib/mediaPages';
import type { WorkFilter } from '../../lib/work';
import { cn } from '../../lib/utils';
import { ReviewDialog, type ReviewSession } from '../ReadyToReview';
import { Button } from '../ui/button';

// OpenReview opens the carousel on an agent (item, the one an agent's card
// shows). Nothing is marked seen or reviewed until the carousel has shown
// it, and only its buttons record a verdict.
export type OpenReview = (opts: { item: T.ReviewItem }) => void;
const OpenReviewContext = createContext<OpenReview | null>(null);

// useOpenReview is openReview, under ReviewV2.
export const useOpenReview = () => useContext(OpenReviewContext);

// OpenWork opens the Work page: of a project or every one, on a filter.
export type OpenWork = (opts?: { project?: string; filter?: WorkFilter }) => void;
const OpenWorkContext = createContext<OpenWork | null>(null);

// useOpenWork is openWork, under ReviewV2.
export const useOpenWork = () => useContext(OpenWorkContext);

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

// ReviewV2 gives what's under it a way to open the carousel on any agent,
// and the Work page through onSelect.
export function ReviewV2({ children, onSelect }: { children: ReactNode; onSelect: (view: View) => void }) {
  const [session, setSession] = useState<ReviewSession | null>(null);
  const pages = useReviewPages(false, session !== null);
  const openReview: OpenReview = (opts) => setSession(startWalk([], opts));
  const openWork: OpenWork = (opts = {}) => onSelect({ kind: 'work', ...opts });
  return (
    <OpenWorkContext.Provider value={openWork}>
      <OpenReviewContext.Provider value={openReview}>
        {children}
        <ReviewDialog session={session} pages={pages} onSession={setSession} />
      </OpenReviewContext.Provider>
    </OpenWorkContext.Provider>
  );
}

// dock is how the review card and the banner sit on the composer: a tab
// joined to its top edge, opaque, so neither floats over the chat as a card
// of its own would, hiding the end of the conversation behind it (UX test,
// finding 8). The chat leaves room for it as it does for the composer.
const dock = 'mx-3 rounded-t-xl border border-b-0 border-line-strong bg-composer';

// AgentReviewCard sits above a finished agent's composer: what it made, its
// pull request, and Review changes, which opens the carousel on it. While the
// agent works on changes asked for, it says so instead.
//
// An agent whose machine is stopped or paused passes wake: the card then says
// so beside its title and carries the Start button, in place of the
// composer's own "asleep" bar (useReviewCardShown), so the two never stack.
export type Wake = { state: 'stopped' | 'paused'; starting: boolean; onStart: () => void };

// useReviewCardShown is whether AgentReviewCard draws anything for the agent.
export function useReviewCardShown(agent: T.Agent): boolean {
  const open = useOpenReview();
  const review = useAgentReview(agent.ref);
  return !!open && (!!review.data || changesPending(agent));
}

export function AgentReviewCard({ agent, wake }: { agent: T.Agent; wake?: Wake }) {
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
    <div className={cn(dock, 'flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2')} data-review-card={agent.ref}>
      <span className="flex min-w-0 items-center gap-2">
        {asked ? <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" /> : <CircleCheck className="size-4 shrink-0 text-subtle" />}
        <span className="shrink-0 text-[12.5px] font-medium text-title">
          {asked ? t('v2.card.requested') : item?.verdict ? t('v2.card.reviewed') : t('v2.card.ready')}
        </span>
        {wake && (
          <span className="inline-flex shrink-0 items-center gap-1 text-[12px] text-muted" data-review-card-asleep={wake.state}>
            <Moon className="size-3.5" />
            {t('v2.card.asleep', { state: wake.state })}
          </span>
        )}
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
            {pr.state === 'merged' ? <GitMerge className="size-3.5" /> : <GitPullRequest className="size-3.5" />}<PrNumberLink pr={pr} /> {prLabel(t, pr)}
          </span>
        )}
      </span>
      {(item || wake) && (
        <span className="ml-auto flex shrink-0 items-center gap-2">
          {wake && (
            <Button size="sm" variant="secondary" className="h-7" disabled={wake.starting} onClick={wake.onStart} data-review-card-wake>
              {wake.starting ? <LoaderCircle className="animate-spin" /> : <Play />}
              {t('chat.composer.wake', { state: wake.state })}
            </Button>
          )}
          {item && (
            <Button size="sm" variant={asked ? 'secondary' : 'primary'} className="h-7" onClick={() => open?.({ item })} data-review-open>
              {t('v2.card.review')}
              <ArrowRight />
            </Button>
          )}
        </span>
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
// banner above the composer. Both open the project's Work on what waits.
export function ReviewCount({ project, variant }: { project: string; variant: 'button' | 'banner' }) {
  const t = useT();
  const openWork = useOpenWork();
  const ready = useProjectReview(project, !!openWork);
  const count = ready.data?.items?.length ?? 0;
  if (!openWork || count === 0) return null;
  const open = (e: { stopPropagation: () => void }) => {
    e.stopPropagation();
    openWork({ project, filter: 'review' });
  };
  if (variant === 'button') {
    return (
      <button
        type="button"
        onClick={open}
        className="shrink-0 rounded-full border border-line-strong bg-surface px-1.5 py-px text-[10.5px] font-medium tabular-nums text-secondary transition hover:border-line-heavy hover:bg-surface-strong hover:text-title"
        data-v2-review-button={project}
      >
        {t('v2.review.toReview', { count })}
      </button>
    );
  }
  return (
    <div className={cn(dock, 'flex items-center gap-2 px-3 py-1.5 text-[12.5px]')} data-v2-review-banner={project}>
      <span className="size-1.5 shrink-0 rounded-full ring-1 ring-inset ring-secondary" aria-hidden />
      <span className="min-w-0 flex-1 truncate text-secondary">{t('v2.review.readyBanner', { count })}</span>
      <button type="button" onClick={open} className="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-0.5 font-medium text-title hover:bg-surface-strong" data-v2-review-banner-open>
        {t('v2.review.review')}
        <ArrowRight className="size-3.5" />
      </button>
    </div>
  );
}
