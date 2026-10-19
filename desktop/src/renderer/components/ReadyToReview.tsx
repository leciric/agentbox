import { useMutation, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import { PrNumberLink } from './PrNumberLink';
import { Check, ChevronDown, ChevronLeft, ChevronRight, ExternalLink, FileDiff, GitBranch, GitMerge, GitPullRequest, Info, Keyboard, LoaderCircle, MessageSquareText, Play, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { pagesFor } from '../lib/pages';
import { projectLabel } from '../lib/projectName';
import { pageItems, pageTotal, refreshInbox, useReviewPages, withoutAgent } from '../lib/inboxPages';
import { type Walk, firstLine, goBack, goForward, isTyping, mergeBlock, pressable, proofKey, nextPending, proofOf, remainingAfter, repeatable, wrapWalk, walkItems, walkOrder, queueOf, reviewKeys, reviewShortcut, step, type MergeBlock, type ReviewAction } from '../lib/review';
import { defaultMethod, loadMethodPrefs, rememberMethod } from '../lib/mergeMethod';
import { isUnseen, markReadySeen, useUnseen } from '../lib/reviewAlert';
import { useNow } from '../lib/useNow';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { languageOfPath, parsePatch } from '../lib/pulls';
import { drafts, splitGitDiff } from '../lib/reviewNotes';
import { Markdown } from './chat/Markdown';
import { DiffTable, useHighlighted } from './DiffTable';
import { NotesList, ReviewNotesContext, reviewNotesKey, useReviewNotes } from './ReviewNotes';
import { MoreMedia } from './MediaTab';
import { ReviewProof, type ProofHandle } from './ReviewProof';
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
      {!list && current && <ReviewCarousel key={current.ref} item={current} position={position} next={next} canBack={!!session?.trail?.length} onNext={advance} onBack={() => session && onSession(goBack(session))} tabs={tabs} />}
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

// armMs is how long M waits for its second press.
const armMs = 3000;

const keyLabels: Record<ReviewAction, MessageKey> = {
  merge: 'review.key.merge',
  requestChanges: 'review.key.requestChanges',
  reviewed: 'review.key.reviewed',
  nextItem: 'review.key.nextItem',
  previousItem: 'review.key.previousItem',
  toggleDiff: 'review.key.toggleDiff',
  nextFile: 'review.key.nextFile',
  previousFile: 'review.key.previousFile',
  previousMedia: 'review.key.previousMedia',
  nextMedia: 'review.key.nextMedia',
  play: 'review.key.play',
  previousChapter: 'review.key.previousChapter',
  nextChapter: 'review.key.nextChapter',
  expand: 'review.key.expand',
  toggleDetails: 'review.key.toggleDetails',
  openPr: 'review.key.openPr',
  help: 'review.key.help',
};

// isMac picks ⌘ over Ctrl in the hints.
const isMac = typeof navigator !== 'undefined' && navigator.platform.startsWith('Mac');

// A pane is what the body shows: the proof, which the review opens on, or
// the diff or the details in its place. D and I (and their buttons in the
// header) show one and, pressed again, go back to the proof.
type Pane = 'proof' | 'diff' | 'details';

// ReviewCarousel is one agent's review, built around the decision. The
// header says what changed (the title and the first line of what the agent
// said) and where its pull request stands (state and checks); the body is
// its proof (ReviewProof); the footer is the verdicts, Merge first. The
// diff, with its line notes (ReviewNotes), and the details (everything the
// agent said, its branch, the pull request's description and checks) are a
// key or a click away, in the proof's place. Every action has a key, shown
// on its button and listed by ?; none fires while typing (isTyping).
// Opening an agent only sees it; Merge, Mark reviewed and Request changes
// record a verdict and go on to the next agent.
function ReviewCarousel({
  item,
  position,
  next,
  canBack,
  onNext,
  onBack,
  tabs,
}: {
  item: T.ReviewItem;
  position?: { n: number; count: number };
  next?: T.ReviewItem;
  // canBack is whether an agent was shown before this one in the walk.
  canBack: boolean;
  onNext: () => void;
  onBack: () => void;
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
  const [pane, setPane] = useState<Pane>('proof');
  const toggle = (p: Pane) => setPane((now) => (now === p ? 'proof' : p));
  const [help, setHelp] = useState(false);
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
  // A merge can't be undone and M is one key, so from the keyboard it takes
  // two: the first arms it (the footer says so), a second within armMs merges.
  // A click on Merge is deliberate enough on its own.
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const id = setTimeout(() => setArmed(false), armMs);
    return () => clearTimeout(id);
  }, [armed]);
  const mergeKey = () => {
    if (!pr || mergeDisabled) return;
    if (!armed) return setArmed(true);
    setArmed(false);
    mergeAndGo();
  };
  // Request changes opens a box over the footer; what's written goes to the
  // agent's chat (the daemon wakes its machine), and the review goes on.
  // Notes on the diff's lines go with it (ReviewNotes), the drafts listed
  // in its box. Enter is a new line there; Ctrl or ⌘ with it sends.
  const [changing, setChanging] = useState(false);
  const [note, setNote] = useState('');
  const changesBox = useRef<HTMLTextAreaElement>(null);
  const notes = useReviewNotes(item.ref);
  const pendingNotes = drafts(notes.notes);
  const send = useMutation({
    mutationFn: (text: string) => api.requestChanges(item.ref, { text, fingerprint: item.fingerprint, notes: pendingNotes.map((n) => n.id) }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: reviewNotesKey(item.ref) });
      dropFromList(queryClient, item.ref);
      toast(t('review.changesSent', { agent: item.agent }));
      refreshInbox(queryClient);
      onNext();
    },
    onError: (err) => toast.error(t('review.changesFailed', { error: errorMessage(err) })),
  });
  const sendChanges = () => {
    const text = note.trim();
    if ((text || pendingNotes.length > 0) && !send.isPending) send.mutate(text);
  };
  // C focuses the box, opening it when it's closed.
  const requestChanges = () => {
    setChanging(true);
    changesBox.current?.focus();
  };
  // The merge slot is always there; it says why it can't merge.
  const footerReason = !pr ? t('review.mergeBlocked.none') : blocked === 'merged' ? t('review.mergeBlocked.merged') : blocked === 'closed' ? t('review.mergeBlocked.closed') : mergeReason;
  const mergeDisabled = !prOpen || !!mergeReason || merge.running.has(pr?.number ?? 0);
  const footerNote = mergeDisabled ? footerReason : merge.failed[pr?.number ?? 0] ? t('memory.pulls.mergeFailed', { error: merge.failed[pr?.number ?? 0] }) : '';

  // J and K move through the diff's files: in the tree, the file picked; in
  // the list, the one scrolled to the top. From the proof they open the diff.
  const body = useRef<HTMLElement>(null);
  const proofView = useRef<ProofHandle>(null);
  const fileAt = useRef(-1);
  const stepFile = (by: number) => {
    if (pane !== 'diff') return setPane('diff');
    const root = body.current;
    if (!root) return;
    const tree = [...root.querySelectorAll<HTMLElement>('[data-tree-file]')];
    if (tree.length) {
      const i = tree.findIndex((b) => b.getAttribute('aria-current') === 'true');
      const to = tree[step(i < 0 ? (by > 0 ? -1 : tree.length) : i, tree.length, by)];
      to?.click();
      to?.focus();
      return;
    }
    const files = [...root.querySelectorAll<HTMLElement>('[data-pull-file]')];
    if (!files.length) return;
    fileAt.current = step(fileAt.current < 0 && by < 0 ? files.length : fileAt.current, files.length, by);
    const file = files[fileAt.current];
    file.tabIndex = -1;
    file.scrollIntoView({ block: 'start' });
    file.focus({ preventScroll: true });
  };
  useEffect(() => {
    fileAt.current = -1;
  }, [pane]);

  const act = (action: ReviewAction) => {
    switch (action) {
      case 'merge':
        return mergeKey();
      case 'requestChanges':
        return requestChanges();
      case 'reviewed':
        return reviewAndGo();
      case 'nextItem':
        if (next) onNext();
        return;
      case 'previousItem':
        if (canBack) onBack();
        return;
      case 'toggleDiff':
        return toggle('diff');
      case 'toggleDetails':
        return toggle('details');
      case 'nextFile':
        return stepFile(1);
      case 'previousFile':
        return stepFile(-1);
      case 'openPr':
        if (pr) void window.agentbox.openExternal(pr.url);
        return;
      case 'previousMedia':
        return proofView.current?.step(-1);
      case 'nextMedia':
        return proofView.current?.step(1);
      case 'play':
        return proofView.current?.togglePlay();
      case 'previousChapter':
        return proofView.current?.chapter(-1);
      case 'nextChapter':
        return proofView.current?.chapter(1);
      case 'expand':
        return proofView.current?.toggleExpand();
      case 'help':
        return setHelp((h) => !h);
    }
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.defaultPrevented || isTyping(e.target as HTMLElement)) return;
    const action = reviewShortcut(e);
    if (!action || (e.repeat && !repeatable(action))) return;
    // The gallery's keys are the proof's: in the diff the arrows scroll, and
    // Space presses the button that has focus.
    if (proofKey(action) && (pane !== 'proof' || (action === 'play' && pressable(e.target as HTMLElement)))) return;
    // With the keys listed, only ? (or Esc) does anything: it closes them.
    if (help && action !== 'help') return;
    e.preventDefault();
    act(action);
  };

  const summary = firstLine(item.summary);
  const content = useRef<HTMLDivElement>(null);

  return (
    <>
      <DialogContent
        ref={content}
        className={cn('flex h-[min(94vh,1040px)] max-h-[94vh] w-[calc(100vw-2rem)] max-w-[1400px] flex-col gap-0 overflow-hidden p-0', fullWhenNarrow)}
        data-review-carousel={item.ref}
        data-review-pane={pane}
        onKeyDown={onKeyDown}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          (event.currentTarget as HTMLElement | null)?.focus();
        }}
        // Esc closes what's over the review first: the keys, a note's box,
        // the Request changes box, an expanded screenshot. Then the review.
        onEscapeKeyDown={(event) => {
          if (!help && !changing && proofView.current?.collapse()) {
            event.preventDefault();
            return;
          }
          if (help) {
            event.preventDefault();
            setHelp(false);
            content.current?.focus();
            return;
          }
          if (notes.composing || notes.editing !== null) {
            event.preventDefault();
            notes.compose(null);
            notes.setEditing(null);
            return;
          }
          if (!changing) return;
          event.preventDefault();
          setChanging(false);
          content.current?.focus();
        }}
      >
        <ReviewNotesContext.Provider value={notes}>
          {/* What changed, and where its pull request stands. */}
          <header className="grid gap-1 border-b border-line px-5 py-3 pr-14" inert={help}>
            <div className="flex min-w-0 items-center gap-3">
              <AIIcon ai={item.ai || 'claude'} className="size-5 shrink-0" />
              <DialogTitle className={cn('min-w-0 truncate text-[16px] leading-snug', !item.title && 'font-mono')} title={item.title || item.agent}>
                {item.title || item.agent}
              </DialogTitle>
              {item.verdict && (
                <Badge variant={item.verdict === 'dismissed' ? 'default' : item.verdict === 'changes' ? 'warning' : 'success'} className="shrink-0" data-review-verdict={item.verdict}>
                  <VerdictText item={item} />
                </Badge>
              )}
              <div className="ml-auto flex shrink-0 items-center gap-1">
                {position && position.count > 1 && (
                  <>
                    <Button size="icon-sm" variant="ghost" disabled={!canBack} onClick={onBack} title={`${t('review.backTitle')} (P)`} aria-label={t('review.key.previousItem')} data-review-back>
                      <ChevronLeft />
                    </Button>
                    <span className="text-[12px] tabular-nums text-subtle" data-review-agent-position={`${position.n}/${position.count}`}>
                      {t('review.position', { n: position.n, count: position.count })}
                    </span>
                    <Button size="icon-sm" variant="ghost" disabled={!next} onClick={onNext} title={next ? `${t('review.nextIs', { agent: next.title || next.agent })} (N)` : undefined} aria-label={t('review.key.nextItem')} data-review-next>
                      <ChevronRight />
                    </Button>
                  </>
                )}
                {tabs}
              </div>
            </div>
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 pl-8 text-[12.5px]">
              <p className={cn('min-w-0 flex-1 basis-[320px] truncate', summary ? 'text-secondary' : 'italic text-subtle')} title={item.summary || undefined} data-review-summary-line>
                {summary || t('review.noSummary')}
              </p>
              {pr && <PrStatus pr={pr} />}
              <div className="ml-auto flex shrink-0 items-center gap-1">
                <Button size="sm" variant={pane === 'diff' ? 'secondary' : 'ghost'} className="h-7" aria-pressed={pane === 'diff'} onClick={() => toggle('diff')} data-review-toggle="diff">
                  <FileDiff />
                  {t('review.diff')}
                  {item.changes && item.changes.files > 0 && (
                    <span className="font-mono text-[11.5px]">
                      <span className="text-emerald-400">+{item.changes.insertions}</span> <span className="text-rose-400">−{item.changes.deletions}</span>
                    </span>
                  )}
                  {pendingNotes.length > 0 && <NotesCount n={pendingNotes.length} />}
                  <Hint k="D" />
                </Button>
                <Button size="sm" variant={pane === 'details' ? 'secondary' : 'ghost'} className="h-7" aria-pressed={pane === 'details'} onClick={() => toggle('details')} data-review-toggle="details">
                  <Info />
                  {t('review.details')}
                  <Hint k="I" />
                </Button>
                {pr && (
                  <Button size="sm" variant="ghost" className="h-7" onClick={() => void window.agentbox.openExternal(pr.url)} title={t('pulls.detail.openOnGitHub')} data-review-github>
                    <ExternalLink />
                    {t('review.github')}
                    <Hint k="O" />
                  </Button>
                )}
              </div>
            </div>
          </header>
          <section ref={body} className="relative flex min-h-0 flex-1 flex-col overflow-y-auto" inert={help} data-review-body>
            {pane === 'proof' &&
              (media.isPending ? <div className="p-6 text-[13px] text-subtle">{t('common.loading')}</div> : <ReviewProof ref={proofView} key={item.ref} project={item.project} proof={proof} />)}
            {pane === 'diff' &&
              (pr ? (
                <div className="flex min-h-0 flex-1 flex-col bg-modal" data-review-slide="changes">
                  <PullRequestView
                    inline
                    bare
                    view="files"
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
              ) : (
                <AgentDiff agent={item.ref} />
              ))}
            {pane === 'details' && (
              <div className="mx-auto grid w-full max-w-4xl content-start gap-6 p-5" data-review-details>
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-muted">
                  <span className="font-medium text-secondary">{projectLabel(item.project, projects.data)}</span>
                  {item.branch && (
                    <span className="flex min-w-0 items-center gap-1">
                      <GitBranch className="size-3 shrink-0 text-faint" />
                      <span className="truncate font-mono text-[11.5px]">{item.branch}</span>
                    </span>
                  )}
                  <span className="text-faint">· {t('review.finished', { when: timeAgo(item.finishedAt) })}</span>
                  {item.machineKept && (
                    <Badge variant="warning" data-review-machine-kept>
                      {t('review.machineKept')}
                    </Badge>
                  )}
                </div>
                <section data-review-summary>
                  <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.summary')}</h3>
                  {item.summary ? <Markdown text={item.summary} className="text-[13px]" /> : <p className="text-[12.5px] italic text-subtle">{t('review.noSummary')}</p>}
                </section>
                {pr && (
                  <section className="flex min-w-0 flex-col overflow-hidden rounded-xl border border-line" data-review-slide="pr">
                    <h3 className="flex min-w-0 items-center gap-2 border-b border-line-faint px-4 py-2 text-[12.5px] font-medium text-primary">
                      <GitPullRequest className="size-3.5 shrink-0 text-subtle" />
                      <span className="min-w-0 truncate">{pr.title}</span>
                      <span className="shrink-0 font-mono text-subtle">#{pr.number}</span>
                    </h3>
                    <PullRequestView
                      inline
                      bare
                      view="description"
                      project={item.project}
                      pr={pr}
                      canMerge={canMerge}
                      mergeRunning={merge.running.has(pr.number)}
                      mergeError={merge.failed[pr.number]}
                      onMerge={() => merge.request(pr)}
                      edits={labels.edits}
                      onToggleLabel={(label, on) => labels.toggleLabel(pr, label, on)}
                    />
                  </section>
                )}
              </div>
            )}
          </section>
          {changing && (
            <div className="mx-4 mb-3 grid gap-2 rounded-xl border border-line-strong bg-well p-3" inert={help} data-review-changes-box>
              <div className="text-[12px] text-muted">{t('review.changesTo', { agent: item.agent })}</div>
              <NotesList notes={notes.notes} />
              <textarea
                ref={changesBox}
                autoFocus={notes.editing === null}
                rows={pendingNotes.length > 0 ? 2 : 3}
                value={note}
                onChange={(e) => setNote(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.nativeEvent.isComposing) {
                    e.preventDefault();
                    sendChanges();
                  }
                }}
                placeholder={pendingNotes.length > 0 ? t('review.notes.extra') : t('review.changesPlaceholder')}
                aria-label={t('review.requestChanges')}
                className="w-full resize-none rounded-lg bg-surface-faint px-2.5 py-2 text-[13px] text-primary outline-none ring-1 ring-line focus:ring-brand-400"
                data-review-changes-input
              />
              <div className="flex justify-end gap-2">
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setChanging(false);
                    content.current?.focus();
                  }}
                >
                  {t('common.cancel')}
                  <Hint k="Esc" />
                </Button>
                <Button size="sm" variant="primary" disabled={(!note.trim() && pendingNotes.length === 0) || send.isPending} onClick={sendChanges} data-review-changes-send>
                  {send.isPending ? <LoaderCircle className="animate-spin" /> : <MessageSquareText />}
                  {t('review.changesSend')}
                  <Hint k={isMac ? '⌘ Enter' : 'Ctrl Enter'} />
                </Button>
              </div>
            </div>
          )}
          {/* The verdicts, Merge last and loudest. Narrow, they wrap rather
              than drop their labels: icons alone left Request changes and
              Merge to guess (UX test, finding 8). */}
          <div className="flex flex-wrap items-center gap-2 border-t border-line bg-modal px-4 py-2.5" inert={help} data-review-actions>
            <Button size="sm" variant="ghost" className="max-md:hidden" onClick={() => setHelp(true)} aria-label={t('review.keys.title')} data-review-keys>
              <Keyboard />
              <Hint k="?" />
            </Button>
            <Button size="sm" variant="secondary" aria-expanded={changing} onClick={requestChanges} data-review-changes>
              <MessageSquareText />
              <span>{t('review.requestChanges')}</span>
              {pendingNotes.length > 0 && <NotesCount n={pendingNotes.length} />}
              <Hint k="C" />
            </Button>
            <Button size="sm" variant="secondary" disabled={marking} onClick={reviewAndGo} data-review-done title={next ? t('review.nextIs', { agent: next.title || next.agent }) : undefined}>
              <Check />
              <span>{t('review.markReviewed')}</span>
              <Hint k="R" />
            </Button>
            {/* Why Merge can't, beside it; narrow, on a line of its own under the actions. */}
            <span className={cn('ml-auto min-w-0 flex-1 truncate text-right text-[12px] text-amber-200 [:root[data-appearance=light]_&]:text-amber-700', footerNote && 'max-sm:order-last max-sm:basis-full max-sm:text-left')} data-merge-blocked title={footerReason}>
              {armed && pr && !mergeDisabled ? <span className="text-primary" data-review-merge-armed>{t('review.mergeArmed', { number: pr.number })}</span> : footerNote}
            </span>
            <div className="flex shrink-0 items-stretch" data-review-merge-split>
              <Button size="sm" variant="primary" className="rounded-r-none" disabled={mergeDisabled} title={footerReason ?? t(methodLabels[method] ?? 'memory.pulls.merge')} onClick={mergeAndGo} data-review-merge data-merge-method={method}>
                {pr && merge.running.has(pr.number) ? <LoaderCircle className="animate-spin" /> : <GitMerge />}
                <span>{pr ? t('review.mergeNumber', { number: pr.number }) : t('review.noPullRequest')}</span>
                {!mergeDisabled && <Hint k="M" onPrimary />}
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
          </div>
          {help && (
            <KeysHelp
              onClose={() => {
                setHelp(false);
                content.current?.focus();
              }}
            />
          )}
        </ReviewNotesContext.Provider>
      </DialogContent>
      {merge.dialog}
    </>
  );
}

// Hint is a button's key, left out where the window is narrow.
function Hint({ k, onPrimary }: { k: string; onPrimary?: boolean }) {
  return (
    <span className={cn('max-md:hidden', onPrimary && '[&_kbd]:border-white/30 [&_kbd]:bg-white/15 [&_kbd]:text-white')} aria-hidden>
      <Kbd>{k}</Kbd>
    </span>
  );
}

// NotesCount is how many line notes wait to go with Request changes.
function NotesCount({ n }: { n: number }) {
  return (
    <span className="rounded-full bg-amber-400/20 px-1.5 text-[11px] font-semibold tabular-nums text-amber-200 [:root[data-appearance=light]_&]:text-amber-700" data-review-notes-count>
      {n}
    </span>
  );
}
// AgentDiff is the agent's whole diff, for an agent that opened no pull
// request: file by file, as the pull request's are, notes and all.
function AgentDiff({ agent }: { agent: string }) {
  const t = useT();
  const diff = useQuery({ queryKey: ['diff', agent], queryFn: () => api.diff(agent) });
  if (diff.error) return <Notice className="m-5">{t('review.diffUnavailable', { error: errorMessage(diff.error) })}</Notice>;
  if (!diff.data) return <div className="p-6 text-[13px] text-subtle">{t('common.loading')}</div>;
  return (
    <div className="grid min-w-0 flex-1 content-start gap-3 bg-sunken p-4" data-review-diff>
      {splitGitDiff(diff.data).map((f) => (
        <AgentFileDiff key={f.path} path={f.path} patch={f.patch} />
      ))}
    </div>
  );
}

function AgentFileDiff({ path, patch }: { path: string; patch: string }) {
  const t = useT();
  const lines = useMemo(() => parsePatch(patch), [patch]);
  const tokens = useHighlighted(lines, languageOfPath(path));
  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-line-faint" data-pull-file={path}>
      <div className="truncate bg-surface-raised/40 px-3 py-2 font-mono text-[12.5px] text-secondary" title={path}>
        {path}
      </div>
      {lines.length > 0 ? (
        <div className="overflow-x-auto border-t border-line-faint bg-well" data-pull-diff={path}>
          <DiffTable path={path} lines={lines} tokens={tokens} />
        </div>
      ) : (
        <p className="px-3 py-2 text-[12px] text-subtle">{t('pulls.detail.noDiff')}</p>
      )}
    </div>
  );
}

// PrStatus is where the pull request stands, in the header: its number, its
// state when it isn't simply open, its checks and a conflict.
function PrStatus({ pr }: { pr: T.PullRequest }) {
  const t = useT();
  return (
    <span className="flex shrink-0 items-center gap-1.5" data-review-pr-state={pr.state}>
      <GitPullRequest className="size-3.5 shrink-0 text-faint" />
      <PrNumberLink pr={pr} className="font-mono text-[12px]" />
      {pr.draft ? <Badge>{t('memory.pulls.draft')}</Badge> : pr.state !== 'open' && <Badge variant={stateVariants[pr.state] ?? 'default'}>{stateLabels[pr.state] ? t(stateLabels[pr.state]) : pr.state}</Badge>}
      {pr.state === 'open' && pr.checks && (
        <Badge variant={checksVariants[pr.checks] ?? 'default'} data-review-checks={pr.checks}>
          {t('memory.pulls.checks', {
            state: checksLabels[pr.checks] ? t(checksLabels[pr.checks]) : pr.checks,
          })}
        </Badge>
      )}
      {pr.state === 'open' && pr.conflict && <Badge variant="danger">{t('memory.pulls.conflicts')}</Badge>}
    </span>
  );
}

// KeysHelp is the ? overlay: every key the review takes. It holds focus
// while it's open (the review behind it is inert), and ?, Esc or a click
// outside it closes it.
function KeysHelp({ onClose }: { onClose: () => void }) {
  const t = useT();
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => close.current?.focus(), []);
  const row = (keys: readonly string[], label: string) => (
    <li key={label} className="flex items-center gap-3">
      <span className="flex w-24 shrink-0 justify-end gap-1">
        {keys.map((k) => (
          <Kbd key={k}>{k}</Kbd>
        ))}
      </span>
      <span className="text-secondary">{label}</span>
    </li>
  );
  return (
    <div className="absolute inset-0 z-20 flex items-center justify-center bg-scrim p-6 backdrop-blur-sm" onClick={onClose} data-review-keys-help>
      <div role="dialog" aria-modal aria-labelledby="review-keys-title" className="grid w-full max-w-md gap-4 rounded-2xl border border-line-strong bg-modal p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <Keyboard className="size-4 text-subtle" />
          <h2 id="review-keys-title" className="flex-1 text-[14px] font-semibold text-title">
            {t('review.keys.title')}
          </h2>
          <button ref={close} type="button" onClick={onClose} aria-label={t('common.close')} className="rounded-md p-1 text-subtle transition-colors hover:bg-surface-raised hover:text-primary">
            <X className="size-4" />
          </button>
        </div>
        <ul className="grid gap-2 text-[12.5px]">
          {reviewKeys.map((k) => row(k.keys, t(keyLabels[k.action])))}
          {row([isMac ? '⌘ Enter' : 'Ctrl Enter'], t('review.key.send'))}
          {row(['Esc'], t('review.key.close'))}
        </ul>
        <p className="text-[12px] text-subtle">{t('review.keys.typing')}</p>
      </div>
    </div>
  );
}
