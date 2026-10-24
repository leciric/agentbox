// Work is every piece of work agents did, live agents' and past ones', newest
// first and grouped by day, of one project or of every one (GET /v1/work).
// It took the place of Today, the Past agents list and the review's Reviewed
// tab. A row opens the review (ReviewDialog) on that agent, live or past: its
// report on top, its media, diff and details, and its verdicts only while it
// waits for one; "N to review" opens it filtered to what waits.
import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ArrowRight, Briefcase, Film, GitBranch, Images, LoaderCircle, Search } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useLanguage, useT, type MessageKey } from '../lib/i18n';
import { pageItems, useReviewPages } from '../lib/inboxPages';
import { projectLabel } from '../lib/projectName';
import { type Walk } from '../lib/review';
import { useNow } from '../lib/useNow';
import { byDay, dayLabel, outcomeOf, type WorkFilter, workFilters, workKey, workQuery } from '../lib/work';
import { cn, errorMessage } from '../lib/utils';
import { MoreMedia } from './MediaTab';
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
  return (
    <div className="h-full overflow-y-auto" data-work-page={project ?? ''}>
      <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
        <div className="mb-5 flex flex-wrap items-end gap-3">
          <div className="min-w-0">
            <h1 className="text-2xl font-semibold tracking-tight text-title">{t('work.title')}</h1>
            <p className="mt-1 text-sm text-muted">{t('work.description')}</p>
          </div>
          {projectFilter && <div className="ml-auto">{projectFilter}</div>}
        </div>
        <WorkList key={`${project ?? ''}:${initial}`} project={project} initial={initial} onSelect={onSelect} />
      </div>
    </div>
  );
}

// WorkList is the list with its filters and search, for the page or for
// Home (limit, with onShowAll to the whole page).
export function WorkList({
  project,
  initial = '',
  limit = 50,
  onShowAll,
  onSelect,
}: {
  project?: string;
  initial?: WorkFilter;
  limit?: number;
  onShowAll?: () => void;
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
  const pages = useInfiniteQuery({
    queryKey: [...workKey, project ?? '', filter, q, limit],
    queryFn: ({ pageParam }) => api.workPage(workQuery(filter, q, limit, pageParam || undefined), project),
    initialPageParam: '',
    getNextPageParam: (last) => (onShowAll ? undefined : last.next || undefined),
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  });
  const items = useMemo(() => pageItems(pages.data), [pages.data]);
  const counts = pages.data?.pages[0]?.counts ?? {};
  const groups = byDay(items);
  const byRef = useMemo(() => new Map(items.map((w) => [w.item.ref, w])), [items]);

  // The review on a row: what waits walks on through the others waiting.
  const [session, setSession] = useState<Walk | null>(null);
  const reviewPages = useReviewPages(false, session !== null);
  const open = (w: T.WorkItem) => {
    const waiting = w.status === 'review' ? items.filter((i) => i.status === 'review').map((i) => i.item.ref) : null;
    setSession({ item: w.item, order: waiting, project });
  };
  const openChat = (item: T.ReviewItem, past: boolean) => {
    setSession(null);
    onSelect(past ? { kind: 'pastAgents', ref: item.ref } : { kind: 'agent', ref: item.ref, tab: 'chat' });
  };
  const words = { today: t('work.today'), yesterday: t('work.yesterday') };

  return (
    <div className="grid min-w-0 gap-3" data-work={project ?? ''} data-work-filter={filter}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <div role="tablist" aria-label={t('work.filters')} className="flex min-w-0 flex-wrap items-center gap-0.5 rounded-lg border border-line bg-surface-faint p-0.5 text-[12.5px]" data-work-filters>
          {workFilters.map((f) => (
            <button
              key={f || 'all'}
              type="button"
              role="tab"
              aria-selected={filter === f}
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
          <div className="flex flex-col items-center gap-2 px-4 py-8 text-center text-[13px] text-subtle" data-work-empty>
            <Briefcase className="size-5 text-faint" />
            {q || filter ? t('work.noMatch') : t('work.empty')}
          </div>
        ) : (
          groups.map((g) => (
            <section key={g.day} data-work-day={g.day}>
              <h2 className="border-b border-line-faint bg-surface-faint px-4 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-subtle">
                {dayLabel(g.day, new Date(now), language, words)}
              </h2>
              <ul className="divide-y divide-line-faint">
                {g.items.map((w) => (
                  <WorkRow key={w.item.ref} work={w} projectName={project ? undefined : projectLabel(w.item.project, projects.data)} onOpen={() => open(w)} />
                ))}
              </ul>
            </section>
          ))
        )}
        {!onShowAll && <MoreMedia pages={pages} />}
      </Panel>
      {onShowAll && (pages.data?.pages[0]?.total ?? 0) > items.length && (
        <button className="flex items-center gap-1 justify-self-start text-[13px] font-medium text-brand-300 hover:text-brand-200" onClick={onShowAll} data-work-show-all>
          {t('work.showAll')}
          <ArrowRight className="size-3.5" />
        </button>
      )}
      <ReviewDialog session={session} pages={reviewPages} onSession={setSession} work={(ref) => byRef.get(ref)} onOpenChat={openChat} />
    </div>
  );
}

// WorkRow is one agent's work: what it was, what came of it, its pull
// request, what it changed and kept, and when it finished.
function WorkRow({ work: w, projectName, onOpen }: { work: T.WorkItem; projectName?: string; onOpen: () => void }) {
  const t = useT();
  const i = w.item;
  const outcome = outcomeOf(w);
  return (
    <li className="flex min-w-0 items-start gap-3 px-4 py-2.5 transition-colors hover:bg-surface-faint" data-work-row={i.ref} data-work-status={w.status}>
      <button type="button" className="flex min-w-0 flex-1 items-start gap-3 text-left" onClick={onOpen}>
        <AIIcon ai={i.ai || 'claude'} className={cn('mt-0.5 size-4 shrink-0', w.past && 'opacity-60')} />
        <span className="grid min-w-0 flex-1 gap-1">
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className={cn('min-w-0 truncate text-[13px] font-medium text-primary', !i.title && 'font-mono')} title={i.title || i.agent}>
              {i.title || i.agent}
            </span>
            {i.title && <span className="shrink-0 font-mono text-[11px] text-subtle">{i.agent}</span>}
            {w.status === 'review' && (
              <Badge variant="info" className="shrink-0" data-work-to-review>
                {t('work.toReview')}
              </Badge>
            )}
            {w.status === 'reviewed' && <span className="shrink-0 text-[11px] text-subtle">{t(i.verdict === 'changes' ? 'work.verdict.changes' : i.verdict === 'dismissed' ? 'work.verdict.dismissed' : 'work.verdict.reviewed')}</span>}
            <OutcomeBadge outcome={outcome} />
          </span>
          <span className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-subtle">
            {projectName && <span className="max-w-[40%] shrink-0 truncate">{projectName}</span>}
            {i.branch && (
              <span className="flex min-w-0 max-w-full items-center gap-1 font-mono">
                <GitBranch className="size-3 shrink-0" />
                <span className="truncate">{i.branch}</span>
              </span>
            )}
            {i.changes && i.changes.files > 0 && (
              <span className="shrink-0 font-mono text-[11px]" data-work-changes>
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
      {i.pr && i.pr.number > 0 ? <PrChip pr={i.pr} className="mt-0.5" dataAttr="data-work-pr" /> : <span className="mt-0.5 shrink-0 text-[11px] text-faint">{t('work.noPr')}</span>}
      <span className="mt-0.5 shrink-0 whitespace-nowrap text-right text-[11.5px] tabular-nums text-subtle" title={new Date(w.at).toLocaleString()}>
        {clockTime(w.at)}
      </span>
    </li>
  );
}
