// UI v2's review: #301's carousel (ReadyToReview.tsx), opened from the
// sidebar and the lead chat's "N to review", and from an agent's own view,
// whose card (AgentReviewCard) says what it made. Its one set of actions is
// the carousel's footer, the same in both layouts. v2 has no inbox, so what
// was its Reviewed section is the modal's Reviewed tab: the agents already
// reviewed, the latest verdict first, each one reopened in the carousel.
import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, CircleCheck, Film, GitMerge, GitPullRequest, Globe, Images, LoaderCircle } from 'lucide-react';
import { createContext, useContext, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { agentReviewPrefix, inboxPageSize, pageItems, useReviewPages } from '../../lib/inboxPages';
import { changesPending } from '../../lib/leadFirst';
import { startWalk } from '../../lib/review';
import { useMediaCounts } from '../../lib/mediaPages';
import { projectLabel } from '../../lib/projectName';
import { useNow } from '../../lib/useNow';
import { cn, errorMessage } from '../../lib/utils';
import { MoreMedia } from '../MediaTab';
import { ReviewDialog, ReviewRow, type ReviewSession } from '../ReadyToReview';
import { Button } from '../ui/button';
import { DialogContent, DialogTitle } from '../ui/dialog';

// OpenReview opens the carousel, the one way in: on an agent (item, the one
// an agent's card shows), or walking through every agent waiting for review,
// or one project's (the sidebar's "N to review", the lead chat's banner;
// Home's "all" can too). Nothing is marked seen or reviewed until the
// carousel has shown an agent, and only its buttons record a verdict. With
// reviewed it opens on the Reviewed tab instead, of project's agents or all.
export type OpenReview = (opts?: { project?: string; item?: T.ReviewItem; reviewed?: boolean }) => void;
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

// reviewedKey is the agents already reviewed, of one project or ('') all:
// under ['reviewed'], so refreshInbox refreshes it.
const reviewedKey = (project?: string) => ['reviewed', 'project', project ?? ''];

// ReviewV2 gives what's under it a way to open the carousel on any agent.
export function ReviewV2({ onSelect, children }: { onSelect: (view: View) => void; children: ReactNode }) {
  const queryClient = useQueryClient();
  const [session, setSession] = useState<ReviewSession | null>(null);
  // The Reviewed tab, while it's on screen: whose agents it lists.
  const [reviewed, setReviewed] = useState<{ project?: string } | null>(null);
  const pages = useReviewPages(false, session !== null);
  const openReview: OpenReview = (opts = {}) => {
    if (opts.reviewed) {
      setSession(null);
      return setReviewed({ project: opts.project });
    }
    if (opts.item) return setSession(startWalk([], opts));
    const { project } = opts;
    const page = project ? queryClient.fetchQuery({ queryKey: projectReviewKey(project), queryFn: () => readyFor(project), staleTime: 0 }) : readyFor();
    void page
      .then(({ items }) => {
        const walk = startWalk(items ?? [], opts);
        // Nothing left to review from the Reviewed tab: it stays.
        if (!walk && reviewed) return;
        setReviewed(null);
        setSession(walk);
      })
      .catch((err) => toast.error(errorMessage(err)));
  };
  // The Reviewed tab lists the walk's project, or every project's.
  const scope = reviewed ?? { project: session?.project };
  const tabs = <ReviewTabs project={scope.project} on={reviewed || session?.item.verdict ? 'reviewed' : 'review'} onReview={() => openReview({ project: scope.project })} onReviewed={() => setReviewed(scope)} />;
  const open = (item: T.ReviewItem) => {
    setReviewed(null);
    setSession({ item, order: null });
  };
  return (
    <OpenReviewContext.Provider value={openReview}>
      {children}
      <ReviewDialog
        session={session}
        pages={pages}
        onSession={setSession}
        onSelect={onSelect}
        tabs={tabs}
        list={reviewed && <ReviewedList project={reviewed.project} tabs={tabs} onOpen={open} />}
        onCloseList={() => setReviewed(null)}
      />
    </OpenReviewContext.Provider>
  );
}

// ReviewTabs switches the modal between the carousel on what's waiting (To
// review, with how many) and the Reviewed list. A reviewed agent reopened in
// the carousel is under Reviewed, where its tab goes back to the list.
function ReviewTabs({ project, on, onReview, onReviewed }: { project?: string; on: 'review' | 'reviewed'; onReview: () => void; onReviewed: () => void }) {
  const t = useT();
  const waiting = useQuery({ queryKey: project ? projectReviewKey(project) : ['review', 'all'], queryFn: () => readyFor(project), refetchInterval: 60_000 });
  const count = waiting.data?.items?.length ?? 0;
  const tab = (active: boolean) =>
    cn('inline-flex h-6 items-center gap-1.5 rounded-md px-2 font-medium transition disabled:opacity-50', active ? 'bg-surface-strong text-title' : 'text-subtle hover:text-primary');
  return (
    <div role="tablist" aria-label={t('v2.reviewed.tabs')} className="flex shrink-0 items-center gap-0.5 rounded-lg border border-line bg-surface-faint p-0.5 text-[12px]" data-review-tabs>
      <button type="button" role="tab" aria-selected={on === 'review'} disabled={on !== 'review' && count === 0} className={tab(on === 'review')} onClick={() => on !== 'review' && onReview()} data-review-tab="review">
        {t('v2.reviewed.toReview')}
        {count > 0 && <span className="tabular-nums text-subtle">{count}</span>}
      </button>
      <button type="button" role="tab" aria-selected={on === 'reviewed'} className={tab(on === 'reviewed')} onClick={onReviewed} data-review-tab="reviewed">
        {t('review.reviewedSection')}
      </button>
    </div>
  );
}

// ReviewedList is the Reviewed tab: the agents already reviewed, the latest
// verdict first, a page at a time; one picked opens in the carousel.
function ReviewedList({ project, tabs, onOpen }: { project?: string; tabs: ReactNode; onOpen: (item: T.ReviewItem) => void }) {
  const t = useT();
  const now = useNow(60_000);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const pages = useInfiniteQuery({
    queryKey: reviewedKey(project),
    queryFn: ({ pageParam }) => api.reviewedPage(new URLSearchParams({ limit: String(inboxPageSize), ...(pageParam ? { cursor: pageParam } : {}) }), project),
    initialPageParam: '',
    getNextPageParam: (last) => last.next || undefined,
    placeholderData: keepPreviousData,
  });
  const items = pageItems(pages.data);
  return (
    <DialogContent
      className="flex max-h-[80vh] max-w-xl flex-col gap-0 p-0"
      data-reviewed-list={project ?? ''}
      // The dialog itself takes the focus, as the carousel does, not the
      // first tab, whose ring would read as the one selected.
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        (event.currentTarget as HTMLElement | null)?.focus();
      }}
    >
      <header className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-3 pr-14">
        <DialogTitle className="min-w-0 flex-1 truncate text-base">{project ? t('v2.reviewed.titleProject', { project: projectLabel(project, projects.data) }) : t('review.reviewedSection')}</DialogTitle>
        {tabs}
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {items.length ? (
          <>
            {items.map((item) => (
              <ReviewRow key={item.ref} item={item} isNew={false} now={now} projects={projects.data} onOpen={() => onOpen(item)} />
            ))}
            <MoreMedia pages={pages} />
          </>
        ) : (
          <div className="px-4 py-6 text-center text-[12.5px] text-subtle">{pages.isPending ? t('common.loading') : t('review.reviewedEmpty')}</div>
        )}
      </div>
    </DialogContent>
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
