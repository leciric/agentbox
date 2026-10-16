import { useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import { ArrowRight, Check, ChevronDown, ChevronLeft, ChevronRight, ExternalLink, GitBranch, GitMerge, GitPullRequest, Image as ImageIcon, MessageSquareText, LoaderCircle, Play } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { pagesFor } from '../lib/pages';
import { projectLabel } from '../lib/projectName';
import { pageItems, pageTotal, refreshInbox, useReviewPages, withoutAgent } from '../lib/inboxPages';
import { type Walk, goBack, goForward, mergeBlock, nextPending, remainingAfter, wrapWalk, walkItems, queueOf, reviewKeys, reviewShortcut, slidesOf, step, swipeStep, type MergeBlock, type ReviewAction, type Slide } from '../lib/review';
import { defaultMethod, loadMethodPrefs, rememberMethod } from '../lib/mergeMethod';
import { isUnseen, markReadySeen, useUnseen } from '../lib/reviewAlert';
import { useNow } from '../lib/useNow';
import { requestComposerFocus } from '../lib/newTask';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { MoreMedia, Recording } from './MediaTab';
import { PageIcon } from './pages/PageThumb';
import { useProjectPages } from './pages/usePages';
import { checksVariants, PullRequestView, stateVariants } from './PullRequestModal';
import { checksLabels, methodLabels, stateLabels, usePullLabels, usePullMerge } from './PullRequestsPanel';
import { AIIcon } from './state';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Kbd, Notice } from './ui/card';
import { Dialog, DialogContent, DialogTitle, fullWhenNarrow } from './ui/dialog';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';

// "Ready to review" is a section of the top bar's inbox (components/Inbox.tsx):
// the agents that finished and wait for the user to look at their work,
// newest first, and below them the ones already reviewed, folded. Both lists
// load a page at a time as they're scrolled (lib/inboxPages.ts). Picking one
// opens the review carousel (ReviewDialog), which Review all walks through
// what's waiting. Opening an agent there is seeing it (it leaves the
// inbox's count), not reviewing it: Mark reviewed moves it to Reviewed, until
// its work changes. Reviewing turned off in Settings leaves
// the section out.

// A review session is the agent on screen and, when it walks through what's
// waiting, the order it walks them in (lib/review.ts queueOf). An agent
// reopened from Reviewed has none.
// project, when set, keeps the walk to that project's agents (openReview).
export type ReviewSession = Walk;

type ReviewPages = ReturnType<typeof useReviewPages>;

// ReviewSection is the inbox's list of what's waiting. shown is what was
// new when the inbox opened, which stays marked while it's open.
export function ReviewSection({ pages, shown, open, onBegin }: { pages: ReviewPages; shown: ReadonlySet<string>; open: boolean; onBegin: (session: ReviewSession) => void }) {
  const t = useT();
  const [showReviewed, setShowReviewed] = useState(false);
  const reviewed = useReviewPages(true, open && showReviewed);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const now = useNow(60_000);
  const unseen = useUnseen();
  const isNew = (i: T.ReviewItem) => isUnseen(unseen, i) || isUnseen(shown, i);
  const items = pageItems(pages.data);
  const done = pageItems(reviewed.data);
  const order = items.map((i) => i.ref);
  return (
    <section className="border-b border-line-faint pb-1" data-review-list>
      <div className="flex items-center gap-2 px-2.5 pb-1 pt-2">
        <span className="text-[11px] font-medium uppercase tracking-wide text-subtle">{t('review.button')}</span>
        {items.length > 0 && <span className="text-[11px] text-subtle">{t('review.waiting', { count: pageTotal(pages.data) })}</span>}
        {items.length > 0 && (
          <Button size="sm" variant="primary" className="ml-auto h-7" data-review-all onClick={() => onBegin({ item: items[0], order })}>
            <Play />
            {t('review.reviewAll')}
          </Button>
        )}
      </div>
      {items.length === 0 ? (
        <div className="px-3 py-3 text-[12.5px] text-muted">{pages.isPending ? t('common.loading') : t('review.empty')}</div>
      ) : (
        <>
          {items.map((item) => (
            <ReviewRow key={item.ref} item={item} isNew={isNew(item)} now={now} projects={projects.data} onOpen={() => onBegin({ item, order })} />
          ))}
          <MoreMedia pages={pages} />
        </>
      )}
      <div data-reviewed-section>
        <button
          type="button"
          aria-expanded={showReviewed}
          data-reviewed-toggle
          onClick={() => setShowReviewed((s) => !s)}
          className="flex w-full items-center gap-1.5 rounded-md px-3 py-1.5 text-left text-[12px] font-medium text-muted transition hover:bg-surface-raised hover:text-primary"
        >
          <ChevronDown className={cn('size-3.5 transition-transform', !showReviewed && '-rotate-90')} />
          {t('review.reviewedSection')}
          {done.length > 0 && <span className="text-subtle">{pageTotal(reviewed.data)}</span>}
        </button>
        {showReviewed &&
          (done.length ? (
            <>
              {done.map((item) => (
                <ReviewRow key={item.ref} item={item} isNew={false} now={now} projects={projects.data} onOpen={() => onBegin({ item, order: null })} />
              ))}
              <MoreMedia pages={reviewed} />
            </>
          ) : (
            <div className="px-4 py-3 text-[12px] text-subtle">{reviewed.isPending ? t('common.loading') : t('review.reviewedEmpty')}</div>
          ))}
      </div>
    </section>
  );
}

// ReviewDialog is the carousel on the session's agent, as the list has it now
// (its pull request merged, say), with the one after it in the walk.
// UI v2's has a Reviewed tab (ReviewV2): tabs sit in the carousel's header,
// and list, while there is one, is shown in the dialog in its place.
export function ReviewDialog({
  session,
  pages,
  onSession,
  onSelect,
  tabs,
  list,
  onCloseList,
}: {
  session: ReviewSession | null;
  pages: ReviewPages;
  onSession: (session: ReviewSession | null) => void;
  onSelect: (view: View) => void;
  tabs?: ReactNode;
  list?: ReactNode;
  onCloseList?: () => void;
}) {
  const t = useT();
  const items = walkItems(pageItems(pages.data), session?.project);
  const current = session && (items.find((i) => i.ref === session.item.ref) ?? session.item);
  const next = session?.order ? nextPending(session.order, session.item.ref, items) : undefined;
  const queue = session?.order ? queueOf(session.order, items) : null;
  const position = queue && session ? { n: queue.indexOf(session.item.ref) + 1, count: queue.length } : undefined;
  // A walk that reaches the end of what's loaded reads the next page.
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages;
  useEffect(() => {
    if (session?.order && !next && hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [session?.order, next, hasNextPage, isFetchingNextPage, fetchNextPage]);
  const advance = () => {
    if (next && session) onSession(goForward(session, next));
    else if (session?.order) {
      // The end of the walk: look again at what waits (agents skipped with
      // Next or Back, or finished since) before saying it's all done.
      const remaining = remainingAfter(session.order, session.item.ref, items);
      if (remaining.length) onSession({ ...session, ended: { remaining: remaining.length } });
      else {
        toast(t('review.allDone'));
        onSession(null);
      }
    } else onSession(null);
  };
  const carryOn = () => {
    if (!session?.order) return;
    const remaining = remainingAfter(session.order, session.item.ref, items);
    onSession(remaining.length ? wrapWalk(session, remaining) : null);
  };
  return (
    <Dialog
      open={current !== null || !!list}
      onOpenChange={(open) => {
        if (open) return;
        onSession(null);
        onCloseList?.();
      }}
    >
      {list}
      {!list && current && session?.ended && (
        <DialogContent className="max-w-sm p-6" data-review-more>
          <DialogTitle className="text-base">{t('review.moreToReview', { count: session.ended.remaining })}</DialogTitle>
          <div className="mt-4 flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => onSession(null)}>
              {t('common.close')}
            </Button>
            <Button size="sm" variant="primary" onClick={carryOn} data-review-continue>
              {t('review.continue')}
              <ArrowRight />
            </Button>
          </div>
        </DialogContent>
      )}
      {!list && current && !session?.ended && <ReviewCarousel key={current.ref} item={current} position={position} next={next} canBack={!!session?.trail?.length} onNext={advance} onBack={() => session && onSession(goBack(session))} onClose={() => onSession(null)} onSelect={onSelect} tabs={tabs} />}
    </Dialog>
  );
}

// ReviewRow is one agent in the list: waiting, or (with a verdict) reviewed.
export function ReviewRow({ item, isNew, now, projects, onOpen }: { item: T.ReviewItem; isNew: boolean; now: number; projects?: T.Project[]; onOpen: () => void }) {
  const t = useT();
  const done = !!item.verdict;
  const queryClient = useQueryClient();
  const markReviewed = () => markItemReviewed(queryClient, item, t);
  return (
    <div
      data-review-row={done ? undefined : item.ref}
      data-reviewed-row={done ? item.ref : undefined}
      data-review-new={isNew ? '' : undefined}
      className={cn('flex w-full min-w-0 items-start gap-1 rounded-lg pr-2 transition hover:bg-surface-raised', isNew && 'bg-brand-500/10 ring-1 ring-inset ring-brand-500/30')}
    >
      <button type="button" className="flex min-w-0 flex-1 items-start gap-3 rounded-lg px-3 py-2.5 text-left" onClick={onOpen}>
        <AIIcon ai={item.ai || 'claude'} className={cn('mt-0.5 size-4 shrink-0', done && 'opacity-60')} />
        <span className="grid min-w-0 flex-1 gap-0.5">
          <span className="flex min-w-0 items-baseline gap-2">
            <span className={cn('truncate text-[13px] font-medium', done ? 'text-secondary' : 'text-primary', !item.title && 'font-mono')}>{item.title || item.agent}</span>
            {isNew && <span className="shrink-0 rounded-full bg-brand-500 px-1.5 text-[9.5px] font-semibold uppercase leading-4 text-white">{t('review.new')}</span>}
            <span className="ml-auto shrink-0 text-[11px] text-subtle">{done && item.reviewedAt ? <VerdictText item={item} now={now} /> : timeAgo(item.finishedAt, now)}</span>
          </span>
          <span className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-muted">
            <span className="shrink-0 truncate">{projectLabel(item.project, projects)}</span>
            {item.branch && (
              <>
                <span aria-hidden className="text-faint">
                  ·
                </span>
                <GitBranch className="size-3 shrink-0 text-faint" />
                <span className="min-w-0 truncate font-mono text-[11px]">{item.branch}</span>
              </>
            )}
            {item.pr && <span className="ml-auto shrink-0 font-mono text-[11px] text-secondary">#{item.pr.number}</span>}
          </span>
        </span>
      </button>
      {!done && (
        <Button size="sm" variant="secondary" className="mt-2 h-7 shrink-0" onClick={markReviewed} data-review-mark>
          <Check />
          {t('review.markReviewed')}
        </Button>
      )}
    </div>
  );
}

// markItemReviewed records the verdict on the work as shown and takes the
// agent off the list at once.
function markItemReviewed(queryClient: QueryClient, item: T.ReviewItem, t: ReturnType<typeof useT>) {
  queryClient.setQueriesData<T.ReviewPage | InfiniteData<T.ReviewPage, string>>({ queryKey: ['review'] }, (data) => withoutAgent(data, item.ref));
  return api
    .review(item.ref, { verdict: 'reviewed', fingerprint: item.fingerprint })
    .then(() => toast(t('review.marked', { agent: item.title || item.agent })))
    .catch((err) => toast.error(t('review.failed', { error: errorMessage(err) })))
    .finally(() => refreshInbox(queryClient));
}

function VerdictText({ item, now }: { item: T.ReviewItem; now?: number }) {
  const t = useT();
  const when = item.reviewedAt ? timeAgo(item.reviewedAt, now) : '';
  return <>{t(item.verdict === 'dismissed' ? 'review.verdict.dismissed' : item.verdict === 'changes' ? 'review.verdict.changes' : 'review.verdict.reviewed', { when })}</>;
}

const blockLabels: Record<Exclude<MergeBlock, 'merged' | 'closed'>, MessageKey> = {
  draft: 'review.mergeBlocked.draft',
  failing: 'review.mergeBlocked.failing',
  conflict: 'review.mergeBlocked.conflict',
  noPermission: 'review.mergeBlocked.noPermission',
};

const keyLabels: Record<ReviewAction, MessageKey> = {
  previous: 'review.key.slides',
  next: 'review.key.slides',
  first: 'review.key.slides',
  last: 'review.key.slides',
  nextAgent: 'review.key.nextAgent',
  previousAgent: 'review.key.previousAgent',
  requestChanges: 'review.key.requestChanges',
  merge: 'review.key.merge',
};

// ReviewCarousel is everything one agent made, a slide at a time, in the
// order it made them: screenshots, recordings (they play right here) and
// Hatch pages, then its pull request, which can be merged from here. The
// slide fills the middle, a filmstrip below it, what the agent said beside
// it. Keys, the arrows, the filmstrip and a swipe move through the slides;
// past the last one, Next goes on to the next agent waiting. Opening an agent
// here is reviewing it: it leaves the list for Reviewed at once, until its
// work changes again.
function ReviewCarousel({
  item,
  position,
  next,
  canBack,
  onNext,
  onBack,
  onClose,
  onSelect,
  tabs,
}: {
  item: T.ReviewItem;
  position?: { n: number; count: number };
  next?: T.ReviewItem;
  // canBack is whether an agent was shown before this one in the walk.
  canBack: boolean;
  onNext: () => void;
  onBack: () => void;
  onClose: () => void;
  onSelect: (view: View) => void;
  tabs?: ReactNode;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const media = useQuery({
    queryKey: ['media', item.ref],
    queryFn: () => api.media(item.ref),
  });
  const pages = useProjectPages(item.project);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // The pull request as the project's list has it now, merged under us say,
  // and whether the project's account may merge at all.
  const pulls = useQuery({
    queryKey: ['pulls', item.project],
    queryFn: () => api.projectPullRequests(item.project),
    enabled: !!item.pr,
  });
  const pr = (item.pr && pulls.data?.pullRequests.find((p) => p.number === item.pr?.number)) || item.pr;
  const slides = slidesOf(media.data ?? [], pagesFor(pages.data, item.ref), pr);
  const [index, setIndex] = useState(0);
  const at = Math.min(index, Math.max(0, slides.length - 1));
  const slide = slides[at];
  const last = at >= slides.length - 1;
  const go = (by: number) => setIndex(step(at, slides.length, by));
  const labels = usePullLabels(item.project);
  const merge = usePullMerge(item.project, () => refreshInbox(queryClient));
  const blocked = pr ? mergeBlock(pr, pulls.data) : null;
  const canMerge = !!pr && blocked !== 'merged' && blocked !== 'closed';
  const mergeReason = blocked && blocked !== 'merged' && blocked !== 'closed' ? t(blockLabels[blocked], { base: pr?.baseBranch ?? '' }) : undefined;
  const swipe = useRef<{ x: number; y: number } | null>(null);

  // Opening an agent's work is seeing it, which only leaves the inbox's count.
  useEffect(() => markReadySeen({ ref: item.ref, fingerprint: item.fingerprint }), [item.ref, item.fingerprint]);

  // The footer's slots never move: Back, Request changes, the merge reason,
  // Merge, Mark reviewed, and Next (Finish on the last agent).
  const [marking, setMarking] = useState(false);
  const [picked, setPicked] = useState<string>();
  const [methodOpen, setMethodOpen] = useState(false);
  const method = picked && merge.methods.includes(picked) ? picked : defaultMethod(merge.methods, loadMethodPrefs(), item.project);
  const pickMethod = (m: string) => {
    setPicked(m);
    rememberMethod(item.project, m);
  };
  const prOpen = pr?.state === 'open';
  const markReviewed = () => {
    setMarking(true);
    void markItemReviewed(queryClient, item, t);
    onNext();
  };
  const mergeAndGo = () => pr && merge.mergeWith(pr, method, markReviewed);
  // Request changes goes to the agent's chat, to write in.
  const requestChanges = () => {
    requestComposerFocus(item.ref);
    onClose();
    onSelect({ kind: 'agent', ref: item.ref, tab: 'chat' });
  };
  // The merge slot is always there; it says why it can't merge.
  const footerReason = !pr ? t('review.mergeBlocked.none') : blocked === 'merged' ? t('review.mergeBlocked.merged') : blocked === 'closed' ? t('review.mergeBlocked.closed') : mergeReason;
  const mergeDisabled = !prOpen || !!mergeReason || merge.running.has(pr?.number ?? 0);

  const act = (action: ReviewAction) => {
    switch (action) {
      case 'previous':
        // Before the first slide is the agent before.
        if (at === 0 && canBack) return onBack();
        return go(-1);
      case 'next':
        // Past the last slide is the next agent.
        if (last && next) return onNext();
        return go(1);
      case 'first':
        return setIndex(0);
      case 'last':
        return setIndex(slides.length - 1);
      case 'nextAgent':
        if (next) onNext();
        return;
      case 'previousAgent':
        if (canBack) onBack();
        return;
      case 'requestChanges':
        return requestChanges();
      case 'merge':
        // The merge methods, for an open pull request that can merge.
        if (!mergeDisabled) setMethodOpen(true);
    }
  };

  const onKeyDown = (e: KeyboardEvent) => {
    // A video's own controls take the arrows to seek, a field its text, and
    // the pull request's tabs their arrows.
    const target = e.target as HTMLElement;
    if (e.defaultPrevented || target instanceof HTMLVideoElement || target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target.isContentEditable) return;
    if (target.getAttribute('role') === 'tab' && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) return;
    const action = reviewShortcut(e);
    if (!action) return;
    e.preventDefault();
    act(action);
  };
  const onPointerDown = (e: PointerEvent) => {
    swipe.current = { x: e.clientX, y: e.clientY };
  };
  const onPointerUp = (e: PointerEvent) => {
    if (!swipe.current) return;
    const by = swipeStep(e.clientX - swipe.current.x, e.clientY - swipe.current.y);
    swipe.current = null;
    if (by) go(by);
  };

  return (
    <>
      <DialogContent
        className={cn('flex h-[min(94vh,1040px)] max-h-[94vh] w-[calc(100vw-2rem)] max-w-[1560px] flex-col gap-0 overflow-hidden p-0', fullWhenNarrow)}
        data-review-carousel={item.ref}
        onKeyDown={onKeyDown}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          (event.currentTarget as HTMLElement | null)?.focus();
        }}
      >
        <header className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line px-5 py-2.5 pr-14">
          <div className="flex min-w-0 flex-1 basis-[420px] items-start gap-3">
            <AIIcon ai={item.ai || 'claude'} className="mt-1 size-5 shrink-0" />
            <div className="grid min-w-0 flex-1 gap-1">
              <div className="flex min-w-0 items-center gap-2">
                <DialogTitle className={cn('truncate text-[16px] leading-snug', !item.title && 'font-mono')}>{item.title || item.agent}</DialogTitle>
                {item.verdict && (
                  <Badge variant={item.verdict === 'dismissed' ? 'default' : item.verdict === 'changes' ? 'warning' : 'success'} className="shrink-0" data-review-verdict={item.verdict}>
                    <VerdictText item={item} />
                  </Badge>
                )}
                {item.machineKept && (
                  <Badge variant="warning" className="shrink-0" data-review-machine-kept>
                    {t('review.machineKept')}
                  </Badge>
                )}
              </div>
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted">
                <span className="font-medium text-secondary">{projectLabel(item.project, projects.data)}</span>
                {item.branch && (
                  <span className="flex min-w-0 items-center gap-1">
                    <GitBranch className="size-3 shrink-0 text-faint" />
                    <span className="truncate font-mono text-[11.5px]">{item.branch}</span>
                  </span>
                )}
                {pr && <PrState pr={pr} />}
                <span className="text-faint">· {t('review.finished', { when: timeAgo(item.finishedAt) })}</span>
              </div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {position && position.count > 1 && (
              <span className="mr-1 text-[12px] tabular-nums text-subtle" data-review-agent-position={`${position.n}/${position.count}`}>
                {t('review.agentPosition', {
                  n: position.n,
                  count: position.count,
                })}
              </span>
            )}
            {tabs}
          </div>
          {/* Narrow, the summary has no column of its own. */}
          {item.summary && <NarrowSummary text={item.summary} />}
        </header>
        <div className="flex min-h-0 flex-1">
          <section className="flex min-w-0 flex-1 flex-col">
            {/* The pull request is read and selected like the modal's, so it takes no swipe. */}
            <div
              className={cn('relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-surface-faint', slide?.kind !== 'pr' && 'touch-pan-y select-none')}
              onPointerDown={slide?.kind === 'pr' ? undefined : onPointerDown}
              onPointerUp={slide?.kind === 'pr' ? undefined : onPointerUp}
            >
              {slide?.kind === 'pr' ? (
                <div className="flex size-full flex-col bg-modal" data-review-slide="pr">
                  <PullRequestView
                    inline
                    bare
                    project={item.project}
                    pr={slide.pr}
                    canMerge={canMerge}
                    mergeRunning={merge.running.has(slide.pr.number)}
                    mergeError={merge.failed[slide.pr.number]}
                    onMerge={() => merge.request(slide.pr)}
                    edits={labels.edits}
                    onToggleLabel={(label, on) => labels.toggleLabel(slide.pr, label, on)}
                  />
                </div>
              ) : slide ? (
                <SlideView key={slide.id} slide={slide} project={item.project} />
              ) : (
                <div className="grid max-w-sm justify-items-center gap-2 p-10 text-center">
                  <ImageIcon className="size-8 text-faint" />
                  <div className="text-sm font-medium text-secondary">{media.isPending ? t('common.loading') : t('review.nothing')}</div>
                  {!media.isPending && <div className="text-xs text-subtle">{t('review.nothingBody')}</div>}
                </div>
              )}
              {slides.length > 1 && slide?.kind !== 'pr' && <CarouselArrow side="left" disabled={at === 0} label={t('review.previous')} onClick={() => go(-1)} />}
              {slides.length > 1 && !last && <CarouselArrow side="right" disabled={false} label={t('review.next')} onClick={() => go(1)} />}
            </div>
            {slide?.kind === 'pr' && merge.failed[slide.pr.number] && (
              <Notice className="mx-4 mb-2">
                {t('memory.pulls.mergeFailed', {
                  error: merge.failed[slide.pr.number],
                })}
              </Notice>
            )}
            {/* One bar under the slide: the filmstrip, where it is, and what to do next. */}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line bg-modal px-4 py-2">
              {slides.length > 0 && <Filmstrip slides={slides} at={at} onPick={setIndex} />}
              <div className="ml-auto flex shrink-0 items-center gap-2">
                {slide?.kind === 'pr' && (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => void window.agentbox.openExternal(slide.pr.url)}>
                      <ExternalLink />
                      {t('pulls.detail.openOnGitHub')}
                    </Button>
                  </>
                )}
              </div>
            </div>
            <div className="flex items-center gap-2 border-t border-line bg-modal px-4 py-2" data-review-actions>
              <Button size="sm" variant="ghost" disabled={!canBack} onClick={onBack} data-review-back title={t('review.backTitle')} aria-label={t('review.back')}>
                <ChevronLeft />
                <span className="max-sm:hidden">{t('review.back')}</span>
                <span className="max-md:hidden">
                  <Kbd>P</Kbd>
                </span>
              </Button>
              <Button size="sm" variant="secondary" onClick={requestChanges} data-review-changes title={t('review.requestChanges')} aria-label={t('review.requestChanges')}>
                <MessageSquareText />
                <span className="max-sm:hidden">{t('review.requestChanges')}</span>
                <span className="max-md:hidden">
                  <Kbd>C</Kbd>
                </span>
              </Button>
              <span className="ml-auto min-w-0 truncate text-[12px] text-amber-200 [:root[data-appearance=light]_&]:text-amber-700" data-merge-blocked title={footerReason}>
                {mergeDisabled ? footerReason : merge.failed[pr?.number ?? 0] ? t('memory.pulls.mergeFailed', { error: merge.failed[pr?.number ?? 0] }) : ''}
              </span>
              <div className="flex shrink-0 items-stretch" data-review-merge-split>
                <Button size="sm" variant="primary" className="rounded-r-none" disabled={mergeDisabled} title={footerReason ?? t(methodLabels[method] ?? 'memory.pulls.merge')} onClick={mergeAndGo} data-review-merge data-merge-method={method}>
                  {pr && merge.running.has(pr.number) ? <LoaderCircle className="animate-spin" /> : <GitMerge />}
                  <span className="max-sm:hidden">{t('review.mergeNumber', { number: pr?.number ?? 0 })}</span>
                </Button>
                <Menu open={methodOpen} onOpenChange={setMethodOpen}>
                  <MenuTrigger asChild>
                    <Button size="sm" variant="primary" className="rounded-l-none border-l border-white/25 px-1.5" disabled={mergeDisabled} aria-label={t('memory.pulls.method')} data-review-merge-method>
                      <ChevronDown />
                    </Button>
                  </MenuTrigger>
                  <MenuContent className="min-w-40">
                    {merge.methods.map((m) => (
                      <MenuItem key={m} icon={m === method ? Check : undefined} onSelect={() => pickMethod(m)} data-merge-option={m}>
                        {methodLabels[m] ? t(methodLabels[m]) : m}
                      </MenuItem>
                    ))}
                  </MenuContent>
                </Menu>
              </div>
              <Button size="sm" variant="secondary" disabled={marking} onClick={markReviewed} data-review-done>
                <Check />
                <span className="max-md:hidden">{t('review.markReviewed')}</span>
              </Button>
              <Button size="sm" variant="default" onClick={onNext} data-review-next title={next ? next.title || next.agent : undefined}>
                {t(next ? 'review.nextAgent' : 'review.finish')}
                <Kbd>N</Kbd>
                <ArrowRight />
              </Button>
            </div>
          </section>

          <aside className="hidden w-[300px] shrink-0 flex-col border-l border-line bg-modal lg:flex xl:w-[340px]" data-review-summary>
            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
              <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.summary')}</h3>
              {item.summary ? <Markdown text={item.summary} className="text-[13px]" /> : <p className="text-[12.5px] italic text-subtle">{t('review.noSummary')}</p>}
              {item.changes && item.changes.files > 0 && (
                <p className="mt-4 flex items-center gap-2 text-[12px] text-muted" data-review-changes>
                  {t('review.changes', { files: item.changes.files })}
                  <span className="font-mono text-emerald-300">+{item.changes.insertions}</span>
                  <span className="font-mono text-rose-300">−{item.changes.deletions}</span>
                </p>
              )}
            </div>
            <div className="border-t border-line-faint px-5 py-3">
              <Shortcuts canMerge={!mergeDisabled} hasNext={!!next} hasBack={canBack} />
            </div>
          </aside>
        </div>
      </DialogContent>
      {merge.dialog}
    </>
  );
}

// NarrowSummary is what the agent said, under the header when the window is
// too narrow for its column: two lines, and the rest on More.
function NarrowSummary({ text }: { text: string }) {
  const t = useT();
  const [more, setMore] = useState(false);
  return (
    <div className="flex w-full min-w-0 items-start gap-2 lg:hidden" data-review-summary-narrow>
      <div className={cn('min-w-0 flex-1 overflow-hidden text-[12.5px]', !more && 'max-h-[2.9em]')}>
        <Markdown text={text} className="text-[12.5px] [&_p]:my-0" />
      </div>
      <button type="button" className="shrink-0 text-[12px] font-medium text-brand-300 hover:underline" onClick={() => setMore((m) => !m)}>
        {t(more ? 'review.less' : 'review.more')}
      </button>
    </div>
  );
}

// PrState is the pull request in the header: its number, whether it's open,
// merged or a draft, and its checks and conflict.
function PrState({ pr }: { pr: T.PullRequest }) {
  const t = useT();
  return (
    <span className="flex items-center gap-1.5" data-review-pr-state={pr.state}>
      <GitPullRequest className="size-3 shrink-0 text-faint" />
      <span className="font-mono text-[11.5px] text-secondary">#{pr.number}</span>
      <span className="min-w-0 max-w-[28ch] truncate text-[11.5px] font-medium text-primary" title={pr.title}>
        {pr.title}
      </span>
      {pr.draft ? <Badge>{t('memory.pulls.draft')}</Badge> : <Badge variant={stateVariants[pr.state] ?? 'default'}>{stateLabels[pr.state] ? t(stateLabels[pr.state]) : pr.state}</Badge>}
      {pr.state === 'open' && pr.checks && (
        <Badge variant={checksVariants[pr.checks] ?? 'default'}>
          {t('memory.pulls.checks', {
            state: checksLabels[pr.checks] ? t(checksLabels[pr.checks]) : pr.checks,
          })}
        </Badge>
      )}
      {pr.state === 'open' && pr.conflict && <Badge variant="danger">{t('memory.pulls.conflicts')}</Badge>}
    </span>
  );
}

// Shortcuts lists the review's keys, the ones that do something here.
function Shortcuts({ canMerge, hasNext, hasBack }: { canMerge: boolean; hasNext: boolean; hasBack: boolean }) {
  const t = useT();
  const shown = reviewKeys.filter((k) => (k.action !== 'merge' || canMerge) && (k.action !== 'nextAgent' || hasNext) && (k.action !== 'previousAgent' || hasBack));
  return (
    <div className="grid gap-1.5 text-[12px] text-muted" data-review-shortcuts>
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.shortcuts')}</h3>
      {shown.map((k) => (
        <span key={k.action} className="flex items-center gap-2">
          <span className="flex w-12 gap-1">
            {k.keys.map((key) => (
              <Kbd key={key}>{key}</Kbd>
            ))}
          </span>
          <span>{t(keyLabels[k.action])}</span>
        </span>
      ))}
      <span className="flex items-center gap-2">
        <span className="flex w-12 gap-1">
          <Kbd>Esc</Kbd>
        </span>
        <span>{t('review.key.close')}</span>
      </span>
    </div>
  );
}

// Filmstrip is every slide as a thumbnail, the one shown ringed, and where it
// is among them.
function Filmstrip({ slides, at, onPick }: { slides: Slide[]; at: number; onPick: (i: number) => void }) {
  const t = useT();
  const strip = useRef<HTMLDivElement>(null);
  useEffect(() => {
    strip.current?.children[at]?.scrollIntoView({
      block: 'nearest',
      inline: 'nearest',
      behavior: 'smooth',
    });
  }, [at]);
  return (
    <div className="flex min-w-0 flex-1 items-center gap-3" data-review-position={`${at + 1}/${slides.length}`}>
      <div ref={strip} className="flex min-w-0 gap-2 overflow-x-auto p-0.5 [scrollbar-width:thin]" data-review-filmstrip>
        {slides.map((s, i) => (
          <button
            key={s.id}
            type="button"
            aria-label={t('review.goTo', { n: i + 1 })}
            title={thumbTitle(s)}
            aria-current={i === at || undefined}
            data-review-thumb={s.kind}
            onClick={() => onPick(i)}
            onPointerDown={(e) => e.stopPropagation()}
            className={cn(
              'relative h-12 w-20 shrink-0 overflow-hidden rounded-md bg-surface-raised ring-2 ring-inset transition',
              i === at ? 'opacity-100 ring-brand-400' : 'opacity-75 ring-line hover:opacity-100',
            )}
          >
            <Thumb slide={s} />
          </button>
        ))}
      </div>
      <span className="shrink-0 text-[12px] tabular-nums text-subtle">{t('review.position', { n: at + 1, count: slides.length })}</span>
    </div>
  );
}

// thumbTitle is a thumbnail's whole name, as its tooltip.
const thumbTitle = (s: Slide) => (s.kind === 'page' ? s.page.title : s.kind === 'pr' ? `#${s.pr.number} ${s.pr.title}` : s.media.name);

function Thumb({ slide }: { slide: Slide }) {
  const t = useT();
  switch (slide.kind) {
    case 'screenshot':
      return <img src={mediaUrl(slide.media)} alt="" draggable={false} className="size-full object-cover" />;
    case 'recording':
      return (
        <span className="flex size-full items-center justify-center bg-black">
          <video src={mediaUrl(slide.media)} preload="metadata" muted className="absolute inset-0 size-full object-cover opacity-70" />
          <Play className="relative size-4 fill-white text-white" />
        </span>
      );
    case 'page':
      return (
        <span className="flex size-full flex-col items-start gap-1 p-1.5 text-left">
          <PageIcon className="size-3.5 shrink-0 text-muted" />
          <span className="w-full truncate text-[10.5px] font-medium leading-tight text-primary">{slide.page.title}</span>
        </span>
      );
    case 'pr':
      return (
        <span className="flex size-full flex-col items-center justify-center gap-0.5">
          <GitPullRequest className="size-4 text-brand-300" />
          <span className="text-[11px] font-medium text-primary">{t('review.slide.pr', { number: slide.pr.number })}</span>
        </span>
      );
  }
}

function CarouselArrow({ side, disabled, label, onClick }: { side: 'left' | 'right'; disabled: boolean; label: string; onClick: () => void }) {
  const Icon = side === 'left' ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      onPointerDown={(e) => e.stopPropagation()}
      className={cn(
        'absolute top-1/2 flex size-10 -translate-y-1/2 items-center justify-center rounded-full border border-line-strong bg-modal/90 text-secondary shadow-lg transition hover:text-primary disabled:pointer-events-none disabled:opacity-0',
        side === 'left' ? 'left-4' : 'right-4',
      )}
    >
      <Icon className="size-5" />
    </button>
  );
}

function SlideView({ slide, project }: { slide: Exclude<Slide, { kind: 'pr' }>; project: string }) {
  const t = useT();
  const [loaded, setLoaded] = useState(false);
  switch (slide.kind) {
    case 'screenshot':
      return (
        <div className="flex size-full items-center justify-center p-3 sm:p-6" data-review-slide="screenshot">
          <img
            src={mediaUrl(slide.media)}
            alt={slide.media.name}
            draggable={false}
            className="max-h-full max-w-full rounded-lg object-contain shadow-[0_24px_80px_-24px_var(--ab-shadow-deep)] ring-1 ring-line"
          />
        </div>
      );
    case 'recording':
      return (
        // Recording sizes its video for the Media viewer; here it fits the slide.
        <div className="flex size-full items-center justify-center bg-black [&_video]:max-h-full [&_video]:w-auto [&_video]:max-w-full" data-review-slide="recording">
          <Recording item={slide.media} />
        </div>
      );
    case 'page':
      return (
        <div className="flex size-full flex-col" data-review-slide="page">
          <div className="flex min-w-0 items-center gap-2 border-b border-line bg-modal px-4 py-2 text-[12.5px]">
            <PageIcon className="size-3.5 shrink-0 text-muted" />
            <span className="truncate font-medium text-primary">{slide.page.title}</span>
            {slide.page.url && (
              <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void window.agentbox.openExternal(slide.page.url)}>
                <ExternalLink />
                {t('review.openPage')}
              </Button>
            )}
          </div>
          <div className="relative min-h-0 flex-1">
            {!loaded && <div className="skeleton absolute inset-0 rounded-none" />}
            <iframe
              title={slide.page.title}
              src={api.pageUrl(project, slide.page.id)}
              // As the page preview frames it: an opaque origin, no reach into the app.
              sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals"
              referrerPolicy="no-referrer"
              onLoad={() => setLoaded(true)}
              className={cn('absolute inset-0 size-full border-0 bg-white', loaded ? 'opacity-100' : 'opacity-0')}
            />
          </div>
        </div>
      );
  }
}
