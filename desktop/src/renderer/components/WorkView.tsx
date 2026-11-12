// Work is every piece of work agents did, live agents' and past ones', newest
// first and grouped by day, of one project or of every one (GET /v1/work).
// It took the place of Today, the Past agents list and the review's Reviewed
// tab. A row opens the review (ReviewDialog) on that agent, live or past: its
// report on top, its media, diff and details, and its verdicts only while it
// waits for one; "N to review" opens it filtered to what waits.
import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Briefcase, CornerDownRight, Film, GitBranch, Images, Layers, LoaderCircle, Search, Sparkles } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useLanguage, useT, type MessageKey } from '../lib/i18n';
import { pageItems, useReviewPages } from '../lib/inboxPages';
import { projectLabel } from '../lib/projectName';
import { type Walk } from '../lib/review';
import { useNow } from '../lib/useNow';
import { dayLabel, mergeHead, outcomeOf, type WorkFilter, workFilters, workKey, type WorkPages, workQuery, workRows } from '../lib/work';
import { workStatus } from '../lib/agentCounts';
import { useAgentCounts } from '../lib/useAgentCounts';
import { cn, errorMessage } from '../lib/utils';
import { MergeWhenGreenBadge } from './MergeWhenGreen';
import { PrChip } from './PrChip';
import { OutcomeBadge, ReviewDialog } from './ReadyToReview';
import { AIIcon } from './state';
import { Badge } from './ui/badge';
import { Notice, Panel } from './ui/card';
import { Input } from './ui/input';
import { Skeleton } from './ui/skeleton';
import { ProjectFilter } from './v2/ProjectFilter';

// How long the search box waits for the typing to pause.
const debounceMs = 250;

const filterLabels: Record<WorkFilter, MessageKey> = {
  '': 'work.filter.all',
  review: 'work.filter.review',
  open: 'work.filter.open',
  merged: 'work.filter.merged',
  none: 'work.filter.none',
  closed: 'work.filter.closed',
};

const clockTime = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

// WorkPage is the page as the app opens it, with a project picker: from is
// the project it opens on, or every project.
export function WorkPage({ project: from, filter, onSelect }: { project?: string; filter?: WorkFilter; onSelect: (view: View) => void }) {
  const [project, setProject] = useState(from ?? '');
  return <WorkView project={project || undefined} filter={filter} projectFilter={<ProjectFilter value={project} onChange={setProject} />} onSelect={onSelect} />;
}

// WorkView is the page: of project, or every project's with projectFilter
// (v2's picker) beside its title. filter is the one it opens on.
export function WorkView({ project, filter: initial = '', projectFilter, onSelect }: { project?: string; filter?: WorkFilter; projectFilter?: ReactNode; onSelect: (view: View) => void }) {
  const t = useT();
  const scroller = useRef<HTMLDivElement>(null);
  return (
    <div ref={scroller} className="h-full overflow-y-auto" data-work-page={project ?? ''}>
      <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
        <div className="mb-5 flex flex-wrap items-end gap-3">
          <div className="min-w-0">
            <h1 className="text-display font-semibold tracking-tight text-title">{t('work.title')}</h1>
            <p className="mt-1 text-sm text-muted">{t('work.description')}</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <button
              type="button"
              onClick={() => onSelect({ kind: 'recap' })}
              className="group inline-flex h-9 items-center gap-1.5 rounded-xl border border-brand-500/30 bg-gradient-to-b from-brand-500/15 to-brand-600/10 px-3 text-ui font-medium text-primary hover:border-brand-400/50 hover:from-brand-500/25"
            >
              <Sparkles className="size-3.5 text-brand-400 transition-transform group-hover:rotate-12" />
              {t('recap.open')}
            </button>
            {projectFilter}
          </div>
        </div>
        <WorkList key={`${project ?? ''}:${initial}`} project={project} initial={initial} scroller={scroller} onSelect={onSelect} />
      </div>
    </div>
  );
}

// WorkList is the list with its filters and search, pageSize at a time: the
// next page is read as the list is scrolled towards its end, and only the
// lines in view are drawn (scroller is what scrolls them), so a list with
// months of work loaded costs what a screenful does.
const pageSize = 50;

// How often the list looks for new work, and how many lines from the end of
// what is loaded the next page is read.
const refreshMs = 60_000;
const readAhead = 12;

// What a line is taken to measure until it is drawn and measured.
const dayHeight = 29;
const rowHeight = 61;

function WorkList({
  project,
  initial = '',
  scroller,
  onSelect,
}: {
  project?: string;
  initial?: WorkFilter;
  scroller: RefObject<HTMLDivElement | null>;
  onSelect: (view: View) => void;
}) {
  const t = useT();
  const language = useLanguage();
  const now = useNow(60_000);
  const [filter, setFilter] = useState<WorkFilter>(initial);
  const [query, setQuery] = useState('');
  const [q, setQ] = useState('');
  useEffect(() => {
    const timer = setTimeout(() => setQ(query.trim()), debounceMs);
    return () => clearTimeout(timer);
  }, [query]);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: !project });
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => [...workKey, project ?? '', filter, q], [project, filter, q]);
  const pages = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => api.workPage(workQuery(filter, q, pageSize, pageParam || undefined), project),
    initialPageParam: '',
    getNextPageParam: (last) => last.next || undefined,
    placeholderData: keepPreviousData,
  });
  // New work shows within the minute by reading the first page again, not
  // every page that was scrolled through (a query's own refetchInterval
  // reads them all, one after another): lib/work.ts mergeHead. What changes
  // further down is reported by an event, which does read them again.
  const busy = pages.isFetching;
  useEffect(() => {
    if (busy) return;
    const timer = setInterval(() => {
      if (queryClient.isFetching({ queryKey }) > 0) return;
      void api
        .workPage(workQuery(filter, q, pageSize), project)
        .then((head) => {
          if (queryClient.isFetching({ queryKey }) === 0) queryClient.setQueryData<WorkPages>(queryKey, (data) => mergeHead(data, head));
        })
        .catch(() => undefined); // the next minute tries again
    }, refreshMs);
    return () => clearInterval(timer);
  }, [busy, queryClient, queryKey, filter, q, project]);
  // Whether a row waits for review is the counts' reading (lib/agentCounts.ts
  // workStatus), as the sidebar's "N to review" that opens this page is: one
  // that asks you something, failed again or is back at work is counted
  // there, not here, and To review leaves it out and counts what it keeps.
  const agentCounts = useAgentCounts();
  const statusOf = (w: T.WorkItem) => workStatus(w, agentCounts.st(w.item.ref), agentCounts.loaded);
  const items = useMemo(() => {
    const all = pageItems(pages.data);
    return filter === 'review' ? all.filter((w) => workStatus(w, agentCounts.st(w.item.ref), agentCounts.loaded) === 'review') : all;
  }, [pages.data, filter, agentCounts]);
  const counts: Partial<Record<WorkFilter, number>> = { ...pages.data?.pages[0]?.counts };
  if (agentCounts.loaded && counts.review !== undefined) counts.review = (project ? agentCounts.of(project) : agentCounts.all).review;
  const rows = useMemo(() => workRows(items), [items]);
  const byRef = useMemo(() => new Map(items.map((w) => [w.item.ref, w])), [items]);
  // Work stacked on another agent's: its parent is whichever listed agent's
  // branch it stands on, whatever day that one is under.
  const byBranch = useMemo(() => new Map(items.filter((w) => w.item.branch).map((w) => [`${w.item.project}\n${w.item.branch}`, w])), [items]);
  const parentOf = (w: T.WorkItem) => (w.item.baseRef ? byBranch.get(`${w.item.project}\n${w.item.baseRef}`) : undefined);

  // The review on a row: what waits walks on through the others waiting.
  const [session, setSession] = useState<Walk | null>(null);
  const reviewPages = useReviewPages(false, session !== null);
  const open = (w: T.WorkItem) => {
    const waiting = statusOf(w) === 'review' ? items.filter((i) => statusOf(i) === 'review').map((i) => i.item.ref) : null;
    setSession({ item: w.item, order: waiting, project });
  };
  const openChat = (item: T.ReviewItem, past: boolean) => {
    setSession(null);
    onSelect(past ? { kind: 'pastAgents', ref: item.ref } : { kind: 'agent', ref: item.ref, tab: 'chat' });
  };
  const words = { today: t('work.today'), yesterday: t('work.yesterday') };

  // Only the lines in view, and a few around them, are in the page. The list
  // scrolls with the page, under its title and filters: listTop is how far
  // down the scroller it starts.
  const list = useRef<HTMLDivElement>(null);
  const [listTop, setListTop] = useState(0);
  const listed = rows.length > 0;
  useLayoutEffect(() => {
    const el = list.current;
    const box = scroller.current;
    if (!el || !box) return;
    const measure = () => setListTop(Math.round(el.getBoundingClientRect().top - box.getBoundingClientRect().top + box.scrollTop));
    measure();
    // The filters wrap onto more lines in a narrow window.
    const resized = new ResizeObserver(measure);
    resized.observe(box);
    return () => resized.disconnect();
  }, [scroller, listed]);
  const virtual = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (n) => (rows[n]?.kind === 'day' ? dayHeight : rowHeight),
    getItemKey: (n) => rows[n]?.key ?? n,
    overscan: 8,
    scrollMargin: listTop,
  });
  const lines = virtual.getVirtualItems();
  // The next page, as the end of what is loaded comes near.
  const lastShown = lines[lines.length - 1]?.index ?? -1;
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && lastShown >= rows.length - readAhead) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, lastShown, rows.length]);

  return (
    <div className="grid min-w-0 gap-3" data-work={project ?? ''} data-work-filter={filter} data-work-loaded={items.length}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        {/* Filters of one list, not tabs with panels of their own: a group of
            toggle buttons, the one on pressed. */}
        <div role="group" aria-label={t('work.filters')} className="flex min-w-0 flex-wrap items-center gap-0.5 rounded-lg border border-line bg-surface-faint p-0.5 text-ui" data-work-filters>
          {workFilters.map((f) => (
            <button
              key={f || 'all'}
              type="button"
              aria-pressed={filter === f}
              onClick={() => setFilter(f)}
              className={cn('inline-flex h-7 items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 font-medium transition', filter === f ? 'bg-surface-strong text-title' : 'text-subtle hover:text-primary')}
              data-work-filter-tab={f || 'all'}
            >
              {t(filterLabels[f])}
              {counts[f] !== undefined && <span className="tabular-nums text-subtle">{counts[f]}</span>}
            </button>
          ))}
        </div>
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-subtle" />
          <Input className="pl-8" aria-label={t('work.search')} placeholder={t('work.search')} value={query} spellCheck={false} onChange={(e) => setQuery(e.target.value)} data-work-search />
        </div>
        {pages.isFetching && !pages.isPending && <LoaderCircle className="size-3.5 shrink-0 animate-spin text-subtle" />}
      </div>

      <Panel className="overflow-hidden" aria-busy={pages.isPending}>
        {pages.isPending ? (
          <div className="divide-y divide-line-faint">
            {['w-48', 'w-36', 'w-56'].map((width) => (
              <div key={width} className="flex items-center gap-3 px-4 py-3">
                <Skeleton className={cn('h-3.5', width)} />
                <Skeleton className="ml-auto h-3 w-14" />
              </div>
            ))}
          </div>
        ) : pages.error ? (
          <div className="p-4">
            <Notice>{errorMessage(pages.error)}</Notice>
          </div>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-8 text-center text-ui text-subtle" data-work-empty>
            <Briefcase className="size-5 text-faint" />
            {q || filter ? t('work.noMatch') : t('work.empty')}
          </div>
        ) : (
          <div ref={list} role="list" className="relative" style={{ height: virtual.getTotalSize() }}>
            {lines.map((line) => {
              const row = rows[line.index];
              if (!row) return null;
              return (
                <div
                  key={line.key}
                  ref={virtual.measureElement}
                  data-index={line.index}
                  role="listitem"
                  className="absolute inset-x-0 top-0"
                  style={{ transform: `translateY(${line.start - virtual.options.scrollMargin}px)` }}
                >
                  {row.kind === 'day' ? (
                    <h2 className="border-b border-line-faint bg-surface-faint px-4 py-1.5 text-caption font-semibold uppercase tracking-wide text-subtle" data-work-day={row.day}>
                      {dayLabel(row.day, new Date(now), language, words)}
                    </h2>
                  ) : (
                    <WorkRow
                      work={row.work}
                      status={statusOf(row.work)}
                      depth={row.depth}
                      first={row.first}
                      stackedOn={parentOf(row.work)}
                      projectName={project ? undefined : projectLabel(row.work.item.project, projects.data)}
                      onOpen={() => open(row.work)}
                    />
                  )}
                </div>
              );
            })}
          </div>
        )}
        {hasNextPage && items.length > 0 && (
          <div data-work-more className="flex h-12 items-center justify-center gap-2 text-ui text-subtle">
            {isFetchingNextPage && (
              <>
                <LoaderCircle className="size-3.5 animate-spin" />
                {t('agent.mediaTab.loadingMore')}
              </>
            )}
          </div>
        )}
      </Panel>
      <ReviewDialog session={session} pages={reviewPages} onSession={setSession} work={(ref) => byRef.get(ref)} onOpenChat={openChat} />
    </div>
  );
}

// WorkRow is one agent's work: what it was, what came of it, its pull
// request, what it changed and kept, and when it finished.
// depth is how far down a stack of the day's work it sits, and stackedOn the
// work whose branch it stands on, listed under any day; first is the day's
// first, which has no line above it. status is its
// status as the counts read it (workStatus): to review, or the state that
// keeps it out of review.
function WorkRow({
  work: w,
  status,
  depth = 0,
  first,
  stackedOn,
  projectName,
  onOpen,
}: {
  work: T.WorkItem;
  status: string;
  depth?: number;
  first?: boolean;
  stackedOn?: T.WorkItem;
  projectName?: string;
  onOpen: () => void;
}) {
  const t = useT();
  const i = w.item;
  const outcome = outcomeOf(w);
  return (
    <div className={cn('flex min-w-0 items-start gap-3 px-4 py-2.5 transition-colors hover:bg-surface-faint', !first && 'border-t border-line-faint')} data-work-row={i.ref} data-work-status={status} data-work-stacked-on={stackedOn?.item.agent}>
      <button type="button" className="flex min-w-0 flex-1 items-start gap-3 text-left" onClick={onOpen} style={depth > 0 ? { paddingLeft: `${Math.min(depth, 3) * 1.5}rem` } : undefined}>
        {depth > 0 && <CornerDownRight aria-hidden className="-ml-5 mt-0.5 size-4 shrink-0 text-faint" />}
        <AIIcon ai={i.ai || 'claude'} className={cn('mt-0.5 size-4 shrink-0', w.past && 'opacity-60')} />
        <span className="grid min-w-0 flex-1 gap-1">
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className={cn('min-w-0 truncate text-ui font-medium text-primary', !i.title && 'font-mono')} title={i.title || i.agent}>
              {i.title || i.agent}
            </span>
            {i.title && <span className="shrink-0 font-mono text-caption text-subtle">{i.agent}</span>}
            {status === 'review' && (
              <Badge variant="info" className="shrink-0" data-work-to-review>
                {t('work.toReview')}
              </Badge>
            )}
            {(status === 'needs' || status === 'error' || status === 'working') && (
              <Badge variant={status === 'needs' ? 'warning' : status === 'error' ? 'danger' : 'default'} className="shrink-0" data-work-st={status}>
                {t(status === 'needs' ? 'v2.st.needs' : status === 'error' ? 'v2.st.error' : 'v2.st.working')}
              </Badge>
            )}
            {w.status === 'reviewed' && <span className="shrink-0 text-caption text-subtle">{t(i.verdict === 'changes' ? 'work.verdict.changes' : i.verdict === 'dismissed' ? 'work.verdict.dismissed' : 'work.verdict.reviewed')}</span>}
            <MergeWhenGreenBadge mark={i.mergeWhenGreen} />
            {/* "In progress" with no report yet only repeats the state beside it. */}
            {!(outcome === 'working' && (status === 'working' || status === 'needs' || status === 'error')) && <OutcomeBadge outcome={outcome} />}
          </span>
          <span className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-caption text-subtle">
            {projectName && <span className="max-w-[40%] shrink-0 truncate">{projectName}</span>}
            {stackedOn && (
              <span className="flex min-w-0 max-w-full items-center gap-1 text-sky-300" title={i.baseRef}>
                <Layers className="size-3 shrink-0" />
                <span className="truncate">{t('work.stackedOn', { name: stackedOn.item.title || stackedOn.item.agent })}</span>
              </span>
            )}
            {i.branch && (
              <span className="flex min-w-0 max-w-full items-center gap-1 font-mono">
                <GitBranch className="size-3 shrink-0" />
                <span className="truncate">{i.branch}</span>
              </span>
            )}
            {i.changes && i.changes.files > 0 && (
              <span className="shrink-0 font-mono text-caption" data-work-changes>
                {t('work.files', { count: i.changes.files })} <span className="text-emerald-400">+{i.changes.insertions}</span> <span className="text-rose-400">−{i.changes.deletions}</span>
              </span>
            )}
            {(w.screenshots ?? 0) > 0 && (
              <span className="inline-flex shrink-0 items-center gap-1" title={t('v2.card.screenshots', { count: w.screenshots ?? 0 })} data-work-screenshots={w.screenshots}>
                <Images className="size-3.5 text-faint" />
                {w.screenshots}
              </span>
            )}
            {(w.recordings ?? 0) > 0 && (
              <span className="inline-flex shrink-0 items-center gap-1" title={t('v2.card.recordings', { count: w.recordings ?? 0 })} data-work-recordings={w.recordings}>
                <Film className="size-3.5 text-faint" />
                {w.recordings}
              </span>
            )}
          </span>
        </span>
      </button>
      {i.pr && i.pr.number > 0 ? <PrChip pr={i.pr} className="mt-0.5" dataAttr="data-work-pr" /> : <span className="mt-0.5 shrink-0 text-caption text-faint">{t('work.noPr')}</span>}
      <span className="mt-0.5 shrink-0 whitespace-nowrap text-right text-caption tabular-nums text-subtle" title={new Date(w.at).toLocaleString()}>
        {clockTime(w.at)}
      </span>
    </div>
  );
}
