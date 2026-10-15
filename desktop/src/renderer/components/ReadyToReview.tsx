import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowRight,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ClipboardCheck,
  ExternalLink,
  GitBranch,
  GitMerge,
  GitPullRequest,
  Image as ImageIcon,
  LoaderCircle,
  Play,
  X,
} from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { pagesFor } from '../lib/pages';
import { projectLabel } from '../lib/projectName';
import {
  countLabel,
  mergeBlock,
  nextPending,
  queueOf,
  reviewedKey,
  reviewKey,
  reviewKeys,
  reviewShortcut,
  slidesOf,
  step,
  swipeStep,
  withoutAgent,
  type MergeBlock,
  type ReviewAction,
  type Slide,
} from '../lib/review';
import { isUnseen, markAllSeen, useReviewing, useUnseen } from '../lib/reviewAlert';
import { useNow } from '../lib/useNow';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { Recording } from './MediaTab';
import { PageIcon } from './pages/PageThumb';
import { useProjectPages } from './pages/usePages';
import { checksVariants, PullRequestView, stateVariants } from './PullRequestModal';
import { checksLabels, stateLabels, usePullLabels, usePullMerge } from './PullRequestsPanel';
import { AIIcon } from './state';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Kbd, Notice } from './ui/card';
import { Dialog, DialogContent, DialogTitle } from './ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// ReadyToReview is the way in to "Ready to review": a button with how many
// agents finished and wait for the user to look at their work, opening the
// list of them, newest first, and below it the ones already reviewed. In a
// project's header it lists that project's; in the top bar, every project's.
// Reviewing turned off in Settings hides it.
export function ReadyToReview(props: { project?: string; onSelect: (view: View) => void; compact?: boolean }) {
  return useReviewing() ? <ReviewButton {...props} /> : null;
}

// A review session is the agent on screen and, when it walks through what's
// waiting, the order it walks them in (lib/review.ts queueOf). An agent
// reopened from Reviewed has none.
type Session = { item: T.ReviewItem; order: string[] | null };

function ReviewButton({ project, onSelect, compact = false }: { project?: string; onSelect: (view: View) => void; compact?: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [session, setSession] = useState<Session | null>(null);
  const [showReviewed, setShowReviewed] = useState(false);
  // The list changes on the event stream (lib/events.ts): a finish, a pull
  // request re-read, a verdict. The poll is a backstop, and what notices an
  // agent that went back to work.
  const list = useQuery({ queryKey: reviewKey(project), queryFn: () => api.readyToReview(project), refetchInterval: 60_000 });
  const reviewed = useQuery({ queryKey: reviewedKey(project), queryFn: () => api.reviewed(project), enabled: open && showReviewed });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const now = useNow(60_000);
  const items = list.data ?? [];
  const unseen = useUnseen();
  // What was new when the list opened stays marked while it's open.
  const [shown, setShown] = useState<ReadonlySet<string>>(new Set());
  const isNew = (i: T.ReviewItem) => isUnseen(unseen, i) || isUnseen(shown, i);
  const fresh = items.filter((i) => isUnseen(unseen, i)).length;
  const label = t('review.button');

  const begin = (item: T.ReviewItem, order: string[] | null) => {
    setOpen(false);
    setSession({ item, order });
  };
  // The agent on screen as the list has it now (its pull request merged, say),
  // and the one after it in the walk.
  const current = session && (items.find((i) => i.ref === session.item.ref) ?? session.item);
  const next = session?.order ? nextPending(session.order, session.item.ref, items) : undefined;
  const queue = session?.order ? queueOf(session.order, items) : null;
  const position = queue && session ? { n: queue.indexOf(session.item.ref) + 1, count: queue.length } : undefined;
  const advance = () => {
    if (next && session) setSession({ ...session, item: next });
    else {
      if (session?.order) toast(t('review.allDone'));
      setSession(null);
    }
  };

  return (
    <>
      <Popover
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          // Opening the list is looking at what's new.
          setShown(next ? unseen : new Set());
          if (next) markAllSeen();
        }}
      >
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={items.length ? t('review.buttonCount', { count: items.length }) : label}
            title={label}
            data-ready-to-review={project ?? 'all'}
            data-ready-new={fresh > 0 ? fresh : undefined}
            className={cn(
              'relative flex shrink-0 items-center gap-1.5 rounded-full text-[12px] font-medium text-muted ring-1 ring-inset ring-line transition hover:bg-surface-raised hover:text-primary',
              compact ? 'size-8 justify-center' : 'h-8 px-3',
              open && 'bg-surface-raised text-primary',
            )}
          >
            <ClipboardCheck className="size-4 shrink-0" />
            {!compact && <span className="whitespace-nowrap">{label}</span>}
            {/* The count is only for what needs review: nothing waiting, no badge. */}
            {items.length > 0 &&
              (compact ? (
                <span
                  data-ready-count
                  className={cn('absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-500 px-1 text-[10px] font-semibold tabular-nums text-white ring-2 ring-[var(--color-ink)]', fresh > 0 && 'animate-pulse')}
                >
                  {countLabel(items.length)}
                </span>
              ) : (
                <span data-ready-count className={cn('rounded-full bg-brand-500 px-1.5 text-[10.5px] font-semibold tabular-nums leading-4 text-white', fresh > 0 && 'animate-pulse')}>
                  {countLabel(items.length)}
                </span>
              ))}
          </button>
        </PopoverTrigger>
        <PopoverContent className="w-[min(440px,calc(100vw-2rem))] p-0" data-review-list onOpenAutoFocus={(e) => e.preventDefault()}>
          <div className="flex items-center gap-2 border-b border-line px-4 py-2.5">
            <span className="text-[14px] font-medium text-title">{label}</span>
            {items.length > 0 && <span className="text-[12px] text-subtle">{t('review.waiting', { count: items.length })}</span>}
            {items.length > 0 && (
              <Button size="sm" variant="primary" className="ml-auto" data-review-all onClick={() => begin(items[0], items.map((i) => i.ref))}>
                <Play />
                {t('review.reviewAll')}
              </Button>
            )}
          </div>
          <div className="max-h-[min(600px,calc(var(--radix-popover-content-available-height)-56px))] overflow-y-auto p-1.5">
            {items.length === 0 ? (
              <div className="px-4 py-8 text-center text-[13px] text-muted">{list.isPending ? t('common.loading') : t('review.empty')}</div>
            ) : (
              items.map((item) => (
                <ReviewRow key={item.ref} item={item} isNew={isNew(item)} now={now} projects={projects.data} onOpen={() => begin(item, items.map((i) => i.ref))} />
              ))
            )}
            <div className="mt-1 border-t border-line-faint pt-1" data-reviewed-section>
              <button
                type="button"
                aria-expanded={showReviewed}
                data-reviewed-toggle
                onClick={() => setShowReviewed((s) => !s)}
                className="flex w-full items-center gap-1.5 rounded-md px-3 py-2 text-left text-[12px] font-medium text-muted transition hover:bg-surface-raised hover:text-primary"
              >
                <ChevronDown className={cn('size-3.5 transition-transform', !showReviewed && '-rotate-90')} />
                {t('review.reviewedSection')}
                {reviewed.data && reviewed.data.length > 0 && <span className="text-subtle">{reviewed.data.length}</span>}
              </button>
              {showReviewed &&
                (reviewed.data?.length ? (
                  reviewed.data.map((item) => <ReviewRow key={item.ref} item={item} isNew={false} now={now} projects={projects.data} onOpen={() => begin(item, null)} />)
                ) : (
                  <div className="px-4 py-3 text-[12px] text-subtle">{reviewed.isPending ? t('common.loading') : t('review.reviewedEmpty')}</div>
                ))}
            </div>
          </div>
        </PopoverContent>
      </Popover>
      <Dialog open={current !== null} onOpenChange={(next) => !next && setSession(null)}>
        {current && <ReviewCarousel key={current.ref} item={current} position={position} next={next} onNext={advance} onClose={() => setSession(null)} onSelect={onSelect} />}
      </Dialog>
    </>
  );
}

// ReviewRow is one agent in the list: waiting, or (with a verdict) reviewed.
function ReviewRow({ item, isNew, now, projects, onOpen }: { item: T.ReviewItem; isNew: boolean; now: number; projects?: T.Project[]; onOpen: () => void }) {
  const t = useT();
  const done = !!item.verdict;
  return (
    <button
      type="button"
      data-review-row={done ? undefined : item.ref}
      data-reviewed-row={done ? item.ref : undefined}
      data-review-new={isNew ? '' : undefined}
      className={cn('flex w-full min-w-0 items-start gap-3 rounded-lg px-3 py-2.5 text-left transition hover:bg-surface-raised', isNew && 'bg-brand-500/10 ring-1 ring-inset ring-brand-500/30')}
      onClick={onOpen}
    >
      <AIIcon ai={item.ai || 'claude'} className={cn('mt-0.5 size-4 shrink-0', done && 'opacity-60')} />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-baseline gap-2">
          <span className={cn('truncate text-[13px] font-medium', done ? 'text-secondary' : 'text-primary', !item.title && 'font-mono')}>{item.title || item.agent}</span>
          {isNew && <span className="shrink-0 rounded-full bg-brand-500 px-1.5 text-[9.5px] font-semibold uppercase leading-4 text-white">{t('review.new')}</span>}
          <span className="ml-auto shrink-0 text-[11px] text-subtle">
            {done && item.reviewedAt ? <VerdictText item={item} now={now} /> : timeAgo(item.finishedAt, now)}
          </span>
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
  );
}

function VerdictText({ item, now }: { item: T.ReviewItem; now?: number }) {
  const t = useT();
  const when = item.reviewedAt ? timeAgo(item.reviewedAt, now) : '';
  return <>{t(item.verdict === 'dismissed' ? 'review.verdict.dismissed' : 'review.verdict.reviewed', { when })}</>;
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
  reviewed: 'review.key.reviewed',
  dismiss: 'review.key.dismiss',
  merge: 'review.key.merge',
};

// ReviewCarousel is everything one agent made, a slide at a time, in the
// order it made them: screenshots, recordings (they play right here) and
// Hatch pages, then its pull request, which can be merged from here. The
// slide fills the middle, a filmstrip below it, what the agent said beside
// it. Keys, the arrows, the filmstrip and a swipe move through the slides;
// past the last one, Next goes on to the next agent waiting. Reviewed and
// Dismiss take the agent off the list, until its work changes again, and go
// on too.
function ReviewCarousel({
  item,
  position,
  next,
  onNext,
  onClose,
  onSelect,
}: {
  item: T.ReviewItem;
  position?: { n: number; count: number };
  next?: T.ReviewItem;
  onNext: () => void;
  onClose: () => void;
  onSelect: (view: View) => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const media = useQuery({ queryKey: ['media', item.ref], queryFn: () => api.media(item.ref) });
  const pages = useProjectPages(item.project);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // The pull request as the project's list has it now, merged under us say,
  // and whether the project's account may merge at all.
  const pulls = useQuery({ queryKey: ['pulls', item.project], queryFn: () => api.projectPullRequests(item.project), enabled: !!item.pr });
  const pr = (item.pr && pulls.data?.pullRequests.find((p) => p.number === item.pr?.number)) || item.pr;
  const slides = slidesOf(media.data ?? [], pagesFor(pages.data, item.ref), pr);
  const [index, setIndex] = useState(0);
  const at = Math.min(index, Math.max(0, slides.length - 1));
  const slide = slides[at];
  const last = at >= slides.length - 1;
  const go = (by: number) => setIndex(step(at, slides.length, by));
  const labels = usePullLabels(item.project);
  const merge = usePullMerge(item.project, () => {
    void queryClient.invalidateQueries({ queryKey: ['review'] });
    void queryClient.invalidateQueries({ queryKey: ['reviewed'] });
  });
  const blocked = pr ? mergeBlock(pr, pulls.data) : null;
  const canMerge = !!pr && blocked !== 'merged' && blocked !== 'closed';
  const mergeReason = blocked && blocked !== 'merged' && blocked !== 'closed' ? t(blockLabels[blocked], { base: pr?.baseBranch ?? '' }) : undefined;
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const pending = !item.verdict;

  const verdict = useMutation({
    mutationFn: (v: string) => api.review(item.ref, { verdict: v, fingerprint: item.fingerprint }),
    onMutate: () => {
      queryClient.setQueriesData<T.ReviewItem[]>({ queryKey: ['review'] }, (list) => withoutAgent(list, item.ref));
      onNext();
    },
    onSuccess: (_r, v) => toast(t(v === 'reviewed' ? 'review.marked' : 'review.dismissed', { agent: item.title || item.agent })),
    onError: (err) => toast.error(t('review.failed', { error: errorMessage(err) })),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['review'] });
      void queryClient.invalidateQueries({ queryKey: ['reviewed'] });
    },
  });

  const act = (action: ReviewAction) => {
    switch (action) {
      case 'previous':
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
      case 'reviewed':
      case 'dismiss':
        if (pending && !verdict.isPending) verdict.mutate(action === 'reviewed' ? 'reviewed' : 'dismissed');
        return;
      case 'merge':
        // To the pull request first; there, its Merge.
        if (!pr) return;
        if (slide?.kind !== 'pr') return setIndex(slides.length - 1);
        if (canMerge && !mergeReason && !merge.running.has(pr.number)) merge.request(pr);
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

  const nextButton = next ? (
    <Button size="sm" variant={last ? 'primary' : 'secondary'} onClick={onNext} data-review-next title={next.title || next.agent}>
      {t('review.nextAgent')}
      <ArrowRight />
    </Button>
  ) : (
    <Button size="sm" variant={last ? 'primary' : 'secondary'} onClick={onClose} data-review-close>
      {t('review.done')}
    </Button>
  );

  return (
    <>
      <DialogContent
        className="flex h-[min(94vh,1040px)] max-h-[94vh] w-[calc(100vw-2rem)] max-w-[1560px] flex-col gap-0 overflow-hidden p-0"
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
                  <Badge variant={item.verdict === 'dismissed' ? 'default' : 'success'} className="shrink-0" data-review-verdict={item.verdict}>
                    <VerdictText item={item} />
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
                {t('review.agentPosition', { n: position.n, count: position.count })}
              </span>
            )}
            <Button size="sm" variant="ghost" onClick={() => onSelect({ kind: 'agent', ref: item.ref })}>
              {t('review.openAgent')}
            </Button>
            {pending && (
              <>
                <Button size="sm" variant="secondary" disabled={verdict.isPending} onClick={() => act('dismiss')} data-review-dismiss>
                  <X />
                  {t('review.dismiss')}
                </Button>
                <Button size="sm" variant="primary" disabled={verdict.isPending} onClick={() => act('reviewed')} data-review-done>
                  <Check />
                  {t('review.reviewed')}
                </Button>
              </>
            )}
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
              <Notice className="mx-4 mb-2">{t('memory.pulls.mergeFailed', { error: merge.failed[slide.pr.number] })}</Notice>
            )}
            {/* One bar under the slide: the filmstrip, where it is, and what to do next. */}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line bg-modal px-4 py-2">
              {slides.length > 0 && <Filmstrip slides={slides} at={at} onPick={setIndex} />}
              <div className="ml-auto flex shrink-0 items-center gap-2">
                {slide?.kind === 'pr' && (
                  <>
                    {canMerge && mergeReason && (
                      <span className="max-w-[260px] text-[12px] text-amber-200" data-merge-blocked>
                        {mergeReason}
                      </span>
                    )}
                    <Button size="sm" variant="ghost" onClick={() => void window.agentbox.openExternal(slide.pr.url)}>
                      <ExternalLink />
                      {t('pulls.detail.openOnGitHub')}
                    </Button>
                    {canMerge && (
                      <Button
                        size="sm"
                        variant={next ? 'secondary' : 'primary'}
                        disabled={!!mergeReason || merge.running.has(slide.pr.number)}
                        title={mergeReason}
                        onClick={() => merge.request(slide.pr)}
                        data-review-merge
                        data-merge-running={merge.running.has(slide.pr.number) || undefined}
                      >
                        {merge.running.has(slide.pr.number) ? <LoaderCircle className="animate-spin" /> : <GitMerge />}
                        {t('memory.pulls.merge')}
                      </Button>
                    )}
                  </>
                )}
                {last && next && slide?.kind !== 'pr' && <span className="hidden max-w-[220px] truncate text-[12px] text-subtle xl:inline">{next.title || next.agent}</span>}
                {nextButton}
              </div>
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
              <Shortcuts pending={pending} canMerge={canMerge && !mergeReason} hasNext={!!next} />
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
      {pr.draft ? <Badge>{t('memory.pulls.draft')}</Badge> : <Badge variant={stateVariants[pr.state] ?? 'default'}>{stateLabels[pr.state] ? t(stateLabels[pr.state]) : pr.state}</Badge>}
      {pr.state === 'open' && pr.checks && (
        <Badge variant={checksVariants[pr.checks] ?? 'default'}>{t('memory.pulls.checks', { state: checksLabels[pr.checks] ? t(checksLabels[pr.checks]) : pr.checks })}</Badge>
      )}
      {pr.state === 'open' && pr.conflict && <Badge variant="danger">{t('memory.pulls.conflicts')}</Badge>}
    </span>
  );
}

// Shortcuts lists the review's keys, the ones that do something here.
function Shortcuts({ pending, canMerge, hasNext }: { pending: boolean; canMerge: boolean; hasNext: boolean }) {
  const t = useT();
  const shown = reviewKeys.filter(
    (k) => (k.action !== 'reviewed' && k.action !== 'dismiss') || pending,
  ).filter((k) => (k.action !== 'merge' || canMerge) && (k.action !== 'nextAgent' || hasNext));
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
    strip.current?.children[at]?.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
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
          <img src={mediaUrl(slide.media)} alt={slide.media.name} draggable={false} className="max-h-full max-w-full rounded-lg object-contain shadow-[0_24px_80px_-24px_var(--ab-shadow-deep)] ring-1 ring-line" />
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
