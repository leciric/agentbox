import { useMutation, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import { ArrowRight, Check, Clapperboard, ChevronDown, ChevronLeft, CircleAlert, CornerDownLeft, ExternalLink, FileDiff, Film, GitBranch, GitMerge, GitPullRequest, MessageSquareText, LoaderCircle, Play } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { pagesFor } from '../lib/pages';
import { projectLabel } from '../lib/projectName';
import { pageItems, pageTotal, refreshInbox, useReviewPages, withoutAgent } from '../lib/inboxPages';
import { type Walk, goBack, goForward, mergeBlock, nextPending, proofOf, remainingAfter, wrapWalk, walkItems, walkOrder, queueOf, reviewKeys, reviewShortcut, step, type MergeBlock, type ReviewAction } from '../lib/review';
import { defaultMethod, loadMethodPrefs, rememberMethod } from '../lib/mergeMethod';
import { isUnseen, markReadySeen, useUnseen } from '../lib/reviewAlert';
import { useNow } from '../lib/useNow';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { MoreMedia } from './MediaTab';
import { ReviewProof } from './ReviewProof';
import { TimelineWide } from './timeline/Timeline';
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
  tabs,
  list,
  onCloseList,
}: {
  session: ReviewSession | null;
  pages: ReviewPages;
  onSession: (session: ReviewSession | null) => void;
  tabs?: ReactNode;
  list?: ReactNode;
  onCloseList?: () => void;
}) {
  const t = useT();
  const items = walkItems(pageItems(pages.data), session?.project);
  const current = session && (items.find((i) => i.ref === session.item.ref) ?? session.item);
  const order = session && walkOrder(session);
  const next = session && order ? nextPending(order, session.item.ref, items) : undefined;
  const queue = order ? queueOf(order, items) : null;
  const position = queue && session ? { n: queue.indexOf(session.item.ref) + 1, count: queue.length } : undefined;
  // A walk that reaches the end of what's loaded reads the next page.
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages;
  const walking = session !== null;
  useEffect(() => {
    if (walking && !next && hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [walking, next, hasNextPage, isFetchingNextPage, fetchNextPage]);
  // advance goes on to the next agent waiting. At the end of the walk it
  // looks again at what waits (agents skipped, or finished since) and goes
  // round to them; only when nothing is left does the review close.
  const advance = () => {
    if (!session || !order) return;
    // A review opened on one agent keeps the order it had then.
    if (next) return onSession(goForward({ ...session, order: queue ?? order }, next));
    const remaining = remainingAfter(order, session.item.ref, items);
    if (remaining.length) return onSession(wrapWalk(session, remaining));
    toast(t('review.allDone'));
    onSession(null);
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
      {!list && current && <ReviewCarousel key={current.ref} item={current} position={position} next={next} canBack={!!session?.trail?.length} onNext={advance} onBack={() => session && onSession(goBack(session))} onClose={() => onSession(null)} tabs={tabs} />}
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
// agent off the list at once. quiet leaves out its toast, for the review,
// which goes on to the next agent: "marked reviewed" over a "Merged" toast
// read as if the merge hadn't happened.
export function markItemReviewed(queryClient: QueryClient, item: T.ReviewItem, t: ReturnType<typeof useT>, quiet = false) {
  dropFromList(queryClient, item.ref);
  return api
    .review(item.ref, { verdict: 'reviewed', fingerprint: item.fingerprint })
    .then(() => !quiet && toast(t('review.marked', { agent: item.title || item.agent })))
    .catch((err) => toast.error(t('review.failed', { error: errorMessage(err) })))
    .finally(() => refreshInbox(queryClient));
}

// dropFromList takes an agent off the lists on screen at once, before the
// daemon's answer refreshes them.
function dropFromList(queryClient: QueryClient, ref: string) {
  queryClient.setQueriesData<T.ReviewPage | InfiniteData<T.ReviewPage, string>>({ queryKey: ['review'] }, (data) => withoutAgent(data, ref));
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

// settleMs is how long after an agent comes up its Merge and Reviewed wait
// before taking a click.
const settleMs = 400;

const keyLabels: Record<ReviewAction, MessageKey> = {
  previous: 'review.key.slides',
  next: 'review.key.slides',
  first: 'review.key.slides',
  last: 'review.key.slides',
  reviewed: 'review.key.reviewed',
  previousAgent: 'review.key.previousAgent',
  requestChanges: 'review.key.requestChanges',
  merge: 'review.key.merge',
};

// A view is one of the review's tabs, in the order → walks them.
type View = 'proof' | 'timeline' | 'changes' | 'pr';
const views: View[] = ['proof', 'timeline', 'changes', 'pr'];

// ReviewCarousel is one agent's review, evidence first. It opens on Proof
// (ReviewProof): the agent's recording in a chapter for each thing it
// checked, its screenshots and pages, and what it said. Changes (its pull
// request's files, or its diff when it opened none) and the pull request come
// after, as tabs. ← → and the tabs move through them; past the pull request,
// → goes on to the next agent waiting, and before Proof ← to the one before.
// Opening an agent only sees it; Merge, Reviewed and Request changes record
// a verdict and go on to the next agent, and Finish closes the review.
function ReviewCarousel({
  item,
  position,
  next,
  canBack,
  onNext,
  onBack,
  onClose,
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
  const proof = proofOf(media.data ?? [], pagesFor(pages.data, item.ref));
  const [view, setView] = useState<View>('proof');
  const at = views.indexOf(view);
  const go = (by: number) => setView(views[step(at, views.length, by)]);
  const labels = usePullLabels(item.project);
  // The review's own "Merged" toast says whose it was and where it went.
  // There's no Undo: a merge can't be taken back.
  const merge = usePullMerge(
    item.project,
    (merged) => {
      toast.success(t('review.merged', { number: merged.number, agent: item.agent, base: merged.baseBranch || item.pr?.baseBranch || '' }), { description: merged.title });
      refreshInbox(queryClient);
    },
    { quiet: true },
  );
  const blocked = pr ? mergeBlock(pr, pulls.data) : null;
  const canMerge = !!pr && blocked !== 'merged' && blocked !== 'closed';
  const mergeReason = blocked && blocked !== 'merged' && blocked !== 'closed' ? t(blockLabels[blocked], { base: pr?.baseBranch ?? '' }) : undefined;

  // Opening an agent's work is seeing it, which only leaves the inbox's count.
  useEffect(() => markReadySeen({ ref: item.ref, fingerprint: item.fingerprint }), [item.ref, item.fingerprint]);

  // The footer's slots never move: Back, Request changes, the merge reason,
  // Merge and next, Reviewed and next, and Finish, in a slot of its own so
  // a double click on the last agent never ends the review.
  const [marking, setMarking] = useState(false);
  // A click that lands within settleMs of an agent coming up is the second
  // half of a double click on the one before: it isn't a verdict on this one.
  const shownAt = useRef(Number.POSITIVE_INFINITY);
  useEffect(() => {
    shownAt.current = performance.now();
  }, []);
  const settled = () => performance.now() - shownAt.current > settleMs;
  const [picked, setPicked] = useState<string>();
  const [methodOpen, setMethodOpen] = useState(false);
  const method = picked && merge.methods.includes(picked) ? picked : defaultMethod(merge.methods, loadMethodPrefs(), item.project);
  const pickMethod = (m: string) => {
    setPicked(m);
    rememberMethod(item.project, m);
  };
  const prOpen = pr?.state === 'open';
  // The review going on to the next agent is the news, so it marks quietly:
  // a "marked reviewed" toast would land over the "Merged" one, or over
  // "That's everything" at the end.
  const markReviewed = () => {
    setMarking(true);
    void markItemReviewed(queryClient, item, t, true);
    onNext();
  };
  const reviewAndGo = () => !marking && settled() && markReviewed();
  // Merged, it's reviewed too (quietly: the "Merged" toast is the news) and
  // the review goes on to the next agent.
  const mergeAndGo = () => pr && !mergeDisabled && settled() && merge.mergeWith(pr, method, markReviewed);
  // Request changes opens a box over the footer; what's written goes to the
  // agent's chat (the daemon wakes its machine), and the review goes on.
  const [changing, setChanging] = useState(false);
  const [note, setNote] = useState('');
  const send = useMutation({
    mutationFn: (text: string) => api.requestChanges(item.ref, { text, fingerprint: item.fingerprint }),
    onSuccess: () => {
      dropFromList(queryClient, item.ref);
      toast(t('review.changesSent', { agent: item.agent }));
      refreshInbox(queryClient);
      onNext();
    },
    onError: (err) => toast.error(t('review.changesFailed', { error: errorMessage(err) })),
  });
  const sendChanges = () => {
    const text = note.trim();
    if (text && !send.isPending) send.mutate(text);
  };
  const requestChanges = () => setChanging(true);
  // The merge slot is always there; it says why it can't merge.
  const footerReason = !pr ? t('review.mergeBlocked.none') : blocked === 'merged' ? t('review.mergeBlocked.merged') : blocked === 'closed' ? t('review.mergeBlocked.closed') : mergeReason;
  const mergeDisabled = !prOpen || !!mergeReason || merge.running.has(pr?.number ?? 0);
  const footerNote = mergeDisabled ? footerReason : merge.failed[pr?.number ?? 0] ? t('memory.pulls.mergeFailed', { error: merge.failed[pr?.number ?? 0] }) : '';

  const act = (action: ReviewAction) => {
    switch (action) {
      case 'previous':
        // Before Proof is the agent before.
        if (at === 0 && canBack) return onBack();
        return go(-1);
      case 'next':
        // Past the pull request is the next agent.
        if (at === views.length - 1 && next) return onNext();
        return go(1);
      case 'first':
        return setView('proof');
      case 'last':
        return setView('pr');
      case 'reviewed':
        return reviewAndGo();
      case 'previousAgent':
        if (canBack) onBack();
        return;
      case 'requestChanges':
        return requestChanges();
      case 'merge':
        return mergeAndGo();
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

  const said = (
    <section data-review-summary>
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.summary')}</h3>
      {item.summary ? <Markdown text={item.summary} className="text-[13px]" /> : <p className="text-[12.5px] italic text-subtle">{t('review.noSummary')}</p>}
      <div className="mt-5 max-lg:hidden">
        <Shortcuts canMerge={!mergeDisabled} hasBack={canBack} />
      </div>
    </section>
  );

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
        // Esc in the Request changes box closes the box, not the review.
        onEscapeKeyDown={(event) => {
          if (!changing) return;
          event.preventDefault();
          setChanging(false);
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
        </header>
        {/* What to look at: the proof first, then the code. */}
        <div role="tablist" aria-label={t('review.views')} className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-line px-4 py-1.5 text-[12.5px]" data-review-views>
          {views.map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={view === v}
              onClick={() => setView(v)}
              className={cn('inline-flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1 transition', view === v ? 'bg-surface-strong text-title' : 'text-secondary hover:bg-surface-raised hover:text-title')}
              data-review-view={v}
            >
              {v === 'proof' && (
                <>
                  <Film className="size-3.5" />
                  {t('review.tab.proof')}
                  {proof.recordings[0]?.chapters.length ? <span className="tabular-nums text-subtle">{proof.recordings[0].chapters.length}</span> : null}
                </>
              )}
              {v === 'timeline' && (
                <>
                  <Clapperboard className="size-3.5" />
                  {t('review.view.timeline')}
                </>
              )}
              {v === 'changes' && (
                <>
                  <FileDiff className="size-3.5" />
                  {t('review.tab.changes')}
                  {item.changes && item.changes.files > 0 && (
                    <span className="font-mono text-[11.5px]">
                      <span className="text-emerald-300">+{item.changes.insertions}</span> <span className="text-rose-300">−{item.changes.deletions}</span>
                    </span>
                  )}
                </>
              )}
              {v === 'pr' && (
                <>
                  <GitPullRequest className="size-3.5" />
                  {pr ? t('review.tab.pr', { number: pr.number }) : t('review.tab.noPr')}
                  {pr?.state === 'open' && pr.checks === 'failing' && <CircleAlert className="size-3.5 text-rose-300" />}
                </>
              )}
            </button>
          ))}
          {view === 'pr' && pr && (
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void window.agentbox.openExternal(pr.url)}>
              <ExternalLink />
              {t('pulls.detail.openOnGitHub')}
            </Button>
          )}
        </div>
        <section className="relative flex min-h-0 flex-1 flex-col overflow-y-auto">
          {view === 'proof' &&
            (media.isPending ? (
              <div className="p-6 text-[13px] text-subtle">{t('common.loading')}</div>
            ) : (
              <ReviewProof key={item.ref} project={item.project} proof={proof} aside={said} />
            ))}
          {(view === 'changes' || view === 'pr') && pr && (
            <div className="flex min-h-0 flex-1 flex-col bg-modal" data-review-slide={view}>
              <PullRequestView
                inline
                bare
                view={view === 'changes' ? 'files' : 'description'}
                project={item.project}
                pr={pr}
                canMerge={canMerge}
                mergeRunning={merge.running.has(pr.number)}
                mergeError={merge.failed[pr.number]}
                onMerge={() => merge.request(pr)}
                edits={labels.edits}
                onToggleLabel={(label, on) => labels.toggleLabel(pr, label, on)}
              />
            </div>
          )}
          {view === 'timeline' && <TimelineWide key={item.ref} project={item.project} agentRef={item.ref} />}
          {view === 'changes' && !pr && <AgentDiff agent={item.ref} />}
          {view === 'pr' && !pr && <div className="p-6 text-[13px] text-subtle">{t('review.noPrBody')}</div>}
        </section>
        {(view === 'changes' || view === 'pr') && pr && merge.failed[pr.number] && (
          <Notice className="mx-4 mb-2">
            {t('memory.pulls.mergeFailed', {
              error: merge.failed[pr.number],
            })}
          </Notice>
        )}
        {changing && (
          <div className="mx-4 mb-3 grid gap-2 rounded-xl border border-line-strong bg-well p-3" data-review-changes-box>
            <div className="text-[12px] text-muted">{t('review.changesTo', { agent: item.agent })}</div>
            <textarea
              autoFocus
              rows={3}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  sendChanges();
                }
              }}
              placeholder={t('review.changesPlaceholder')}
              aria-label={t('review.requestChanges')}
              className="w-full resize-none rounded-lg bg-surface-faint px-2.5 py-2 text-[13px] text-primary outline-none ring-1 ring-line focus:ring-brand-400"
              data-review-changes-input
            />
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => setChanging(false)}>
                {t('common.cancel')}
              </Button>
              <Button size="sm" variant="primary" disabled={!note.trim() || send.isPending} onClick={sendChanges} data-review-changes-send>
                {send.isPending ? <LoaderCircle className="animate-spin" /> : <MessageSquareText />}
                {t('review.changesSend')}
                <CornerDownLeft className="opacity-70" />
              </Button>
            </div>
          </div>
        )}
        {/* Narrow, the actions wrap rather than drop their labels: icons
            alone left Request changes and Merge to guess (UX test, finding 8). */}
        <div className="flex flex-wrap items-center gap-2 border-t border-line bg-modal px-4 py-2" data-review-actions>
          <Button size="sm" variant="ghost" disabled={!canBack} onClick={onBack} data-review-back title={t('review.backTitle')} aria-label={t('review.back')}>
            <ChevronLeft />
            <span>{t('review.back')}</span>
            <span className="max-md:hidden">
              <Kbd>P</Kbd>
            </span>
          </Button>
          <Button size="sm" variant="secondary" aria-expanded={changing} onClick={requestChanges} data-review-changes title={t('review.requestChanges')} aria-label={t('review.requestChanges')}>
            <MessageSquareText />
            <span>{t('review.requestChanges')}</span>
            <span className="max-md:hidden">
              <Kbd>C</Kbd>
            </span>
          </Button>
          {/* Why Merge can't, beside it; narrow, on a line of its own under the actions. */}
          <span className={cn('ml-auto min-w-0 flex-1 truncate text-right text-[12px] text-amber-200 [:root[data-appearance=light]_&]:text-amber-700', footerNote && 'max-sm:order-last max-sm:basis-full max-sm:text-left')} data-merge-blocked title={footerReason}>
            {footerNote}
          </span>
          <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
            <div className="flex shrink-0 items-stretch" data-review-merge-split>
              <Button size="sm" variant="primary" className="rounded-r-none" disabled={mergeDisabled} title={footerReason ?? t(methodLabels[method] ?? 'memory.pulls.merge')} onClick={mergeAndGo} data-review-merge data-merge-method={method}>
                {pr && merge.running.has(pr.number) ? <LoaderCircle className="animate-spin" /> : <GitMerge />}
                <span>{pr ? t('review.mergeAndNext', { number: pr.number }) : t('review.noPullRequest')}</span>
                {!mergeDisabled && (
                  <span className="max-md:hidden">
                    <Kbd>M</Kbd>
                  </span>
                )}
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
            <Button size="sm" variant="default" disabled={marking} onClick={reviewAndGo} data-review-done title={next ? t('review.nextIs', { agent: next.title || next.agent }) : undefined} aria-label={t('review.reviewedNext')}>
              <Check />
              <span>{t('review.reviewedNext')}</span>
              <span className="max-md:hidden">
                <Kbd>N</Kbd>
              </span>
            </Button>
            <span aria-hidden className="mx-1 h-5 w-px shrink-0 bg-line" />
            <Button size="sm" variant="ghost" onClick={onClose} data-review-finish>
              {t('review.finish')}
              <ArrowRight />
            </Button>
          </div>
        </div>
      </DialogContent>
      {merge.dialog}
    </>
  );
}

// AgentDiff is the agent's whole diff, for an agent that opened no pull
// request: its lines coloured as a diff, nothing more.
function AgentDiff({ agent }: { agent: string }) {
  const t = useT();
  const diff = useQuery({ queryKey: ['diff', agent], queryFn: () => api.diff(agent) });
  if (diff.error) return <Notice className="m-5">{t('review.diffUnavailable', { error: errorMessage(diff.error) })}</Notice>;
  if (!diff.data) return <div className="p-6 text-[13px] text-subtle">{t('common.loading')}</div>;
  return (
    <pre className="min-w-0 flex-1 overflow-x-auto bg-sunken py-3 font-mono text-[12px] leading-relaxed" data-review-diff>
      {diff.data.split('\n').map((line, i) => (
        <div
          key={i}
          className={cn(
            'px-4',
            line.startsWith('+') && !line.startsWith('+++') && 'bg-emerald-500/10 text-emerald-200 [:root[data-appearance=light]_&]:text-emerald-800',
            line.startsWith('-') && !line.startsWith('---') && 'bg-rose-500/10 text-rose-200 [:root[data-appearance=light]_&]:text-rose-800',
            (line.startsWith('diff ') || line.startsWith('@@')) && 'text-brand-300',
            !/^[+\-@d]/.test(line) && 'text-muted',
          )}
        >
          {line || ' '}
        </div>
      ))}
    </pre>
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
function Shortcuts({ canMerge, hasBack }: { canMerge: boolean; hasBack: boolean }) {
  const t = useT();
  const shown = reviewKeys.filter((k) => (k.action !== 'merge' || canMerge) && (k.action !== 'previousAgent' || hasBack));
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
