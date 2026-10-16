// UI v2's sidebar, lead-first: most of the time is spent talking to each
// project's lead, so the list is Home and one row per lead, with its icon,
// its last line and its live agents' counts (working, to review, asks,
// failed). Each project's live agents fold under it, one quiet row each:
// neutral text and one small state mark, with colour only for what needs you
// (asks, failed); finished ones are in Past agents. Under Home, search and a
// filter popover (T3Code's: state, project, removable chips) narrow it. The
// projects the user pinned come first, in the order they dragged them, then
// the ones with something going on, most recent first, and docked at the
// bottom, above the foot, the Idle list of the rest, folded unless nothing is
// going on, and under it the projects the user hid, folded. The
// project on screen never folds into Idle: the chat you're in stays in view.
// A project's options are on a right-click of its row. The reading is
// lib/leadFirst.ts's, lib/projectOrder.ts's and lib/sidebarHidden.ts's.
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Archive, Check, ChevronRight, CircleArrowUp, Eye, EyeOff, FolderPlus, House, Images, ListChecks, ListFilter, LoaderCircle, MoonStar, RefreshCw, Search, Settings, ShieldAlert, SquarePen, X } from 'lucide-react';
import { Fragment, useEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode } from 'react';
import * as A from '../../../shared/api';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { pageItems, useQuestions, useReviewHead } from '../../lib/inboxPages';
import { activeProjects, agentSt, attention, awaitsApproval, countSt, filterCount, leadLine, leadBusy, liveOrder, projectRows, toggle, type AgentSt, type Filters, type ProjectRows, type Row } from '../../lib/leadFirst';
import { movePin, orderProjects, togglePin } from '../../lib/projectOrder';
import { HelpMenu } from './Tour';
import { setFilterChoice, setHiddenOpen, setPins, useV2Projects } from '../../lib/v2Projects';
import { arrange, hide, pinsAfterHide, sections, unhide, type Section } from '../../lib/sidebarHidden';
import { useSidebarLayout } from '../../lib/useSidebarLayout';
import { useGrace } from '../../lib/grace';
import { projectLabel } from '../../lib/projectName';
import { useReviewing } from '../../lib/reviewAlert';
import { useNow } from '../../lib/useNow';
import { updateHint } from '../../lib/appUpdate';
import { isNightly, isUpgrade } from '../../lib/nightly';
import { useAppUpdate } from '../../lib/useAppUpdate';
import { cn, errorMessage, humanBytes, humanRate, shortcut, timeAgo } from '../../lib/utils';
import { chatLabel, useMood } from '../../lib/agentStatus';
import { AgentAvatar } from '../state';
import { useCpuHistory } from '../../lib/useCpuHistory';
import { Sparkline } from '../Sparkline';
import { openNewTask, requestComposerFocus } from '../../lib/newTask';
import { useRemoving } from '../../lib/removing';
import { openPanelOn } from '../../lib/rightPanel';
import { AgentContextMenu } from '../AgentContextMenu';
import { Logo } from '../Sidebar';
import { ProjectIcon, ProjectIconDialog } from '../ProjectIcon';
import { ReviewCount, useOpenReview } from './ReviewV2';
import { projectActions, type ProjectAction } from './ProjectHeader';
import { TalkToProject } from './TalkToProject';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuMore, ContextMenuSeparator, ContextMenuTrigger } from '../ui/context-menu';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';

const stLabel: Record<AgentSt, MessageKey> = {
  needs: 'v2.st.needs',
  error: 'v2.st.error',
  review: 'v2.st.review',
  working: 'v2.st.working',
  done: 'v2.st.done',
};

// The counts on a folded lead, in the order they read.
const countKeys: { st: AgentSt; key: MessageKey }[] = [
  { st: 'working', key: 'v2.count.working' },
  { st: 'review', key: 'v2.count.review' },
  { st: 'needs', key: 'v2.count.needs' },
  { st: 'error', key: 'v2.count.error' },
];

export function SidebarV2({ view, onSelect, onAddProject, onNewAgent }: { view: View; onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const activity = useQuery({ queryKey: ['projectActivity'], queryFn: api.projectActivity, refetchInterval: 60_000 });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  // The same test as Sidebar's: a nightly is one by its version, or by the update check's word.
  const update = useQuery({ queryKey: ['update'], queryFn: api.update, staleTime: Infinity });
  const info = useQuery({ queryKey: ['app-info'], queryFn: () => window.agentbox.info(), staleTime: Infinity });
  const nightly = isNightly(info.data?.version) || !!update.data?.nightly;
  const reviewing = useReviewing();
  const head = useReviewHead(reviewing);
  const questions = useQuestions();
  const kept = useV2Projects();
  const [q, setQ] = useState('');
  const filters: Filters = { st: kept.st, projects: kept.projects, q };
  const onFilters = (f: Filters) => {
    if (f.st !== kept.st || f.projects !== kept.projects) setFilterChoice(f.st, f.projects);
    setQ(f.q);
  };
  const [expanded, setExpanded] = useState<ReadonlyMap<string, boolean>>(new Map());
  const [dragging, setDragging] = useState<string | null>(null);
  const [drop, setDrop] = useState<{ name: string; edge: 'before' | 'after' } | null>(null);

  const rows: Row[] = useMemo(() => {
    const ctx = {
      review: new Set((reviewing ? (head.data?.items ?? []) : []).map((i) => i.ref)),
      asking: new Set(pageItems(questions.data).map((n) => n.ref)),
    };
    return (agents.data ?? []).map((agent) => ({ agent, st: agentSt(agent, ctx) }));
  }, [agents.data, head.data, questions.data, reviewing]);
  const label = (p: T.Project) => projectLabel(p);
  const current = view.kind === 'agent' ? view.ref : undefined;
  // The agent on screen opens its project once, so its row is in view; the
  // chevron folds it again like any other.
  const currentAgentProject = current?.split('/')[0];
  useEffect(() => {
    if (currentAgentProject) setExpanded((s) => (s.has(currentAgentProject) ? s : new Map(s).set(currentAgentProject, true)));
  }, [currentAgentProject]);

  // Each lead's chat, the same queries its row reads: a lead mid-turn counts
  // as working, for the filter, the counts and the sort, and with one waiting
  // on the user's permission keeps its project out of the Idle list.
  const leadChats = useQueries({
    queries: (projects.data ?? []).map((p) => ({ queryKey: ['projectChat', p.name], queryFn: () => api.projectChat(p.name), refetchInterval: 60_000 })),
  });
  const leadKey = (projects.data ?? []).map((p, i) => `${p.name}\t${leadChats[i]?.data?.chat ?? ''}`).join('\n');
  const leads = useMemo(() => new Map(leadKey ? leadKey.split('\n').map((l) => l.split('\t') as [string, string]) : []), [leadKey]);
  const busyNow = useMemo(() => new Set([...leads].filter(([, chat]) => leadBusy(chat)).map(([name]) => name)), [leads]);
  const activeNow = useMemo(() => activeProjects(rows, leads, pageItems(questions.data).map((n) => n.ref)), [rows, leads, questions.data]);
  // Both with a grace (lib/grace.ts): a busy project goes through moments
  // with nothing to show for it, between a lead's turns or as an agent
  // finishes, which would drop it to Idle and bring it back.
  const busy = useGrace(busyNow);
  const activeGrace = useGrace(activeNow);
  // The project on screen, its lead's or one of its agents', counts as
  // active: folding it away would lose the chat you're reading.
  const onScreen = view.kind === 'project' ? view.project : currentAgentProject;
  const active = useMemo(() => (onScreen && !activeGrace.has(onScreen) ? new Set([...activeGrace, onScreen]) : activeGrace), [activeGrace, onScreen]);

  const order = useMemo(() => {
    return orderProjects({
      projects: projects.data ?? [],
      pins: kept.pins,
      activity: new Map((activity.data ?? []).map((a) => [a.project, a])),
      active,
      label,
    });
    // label is projectLabel's, the same each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projects.data, activity.data, active, kept.pins]);
  const pinned = new Set(kept.pins);
  const ctx = { busy, keep: pinned };
  const { layout, save } = useSidebarLayout();
  const hiddenSet = new Set(layout.hidden);
  // A hidden project comes back while it has something going on, which the
  // project on screen alone doesn't count as: opened from Hidden, it stays there.
  const arranged = arrange(order, layout, activeGrace);
  const lists = {
    pinned: projectRows(arranged.pinned, rows, filters, expanded, label, ctx),
    rest: projectRows(arranged.rest, rows, filters, expanded, label, ctx),
    idle: projectRows(arranged.idle, rows, filters, expanded, label, ctx),
    hidden: projectRows(arranged.hidden, rows, filters, expanded, label, ctx),
  };
  const searching = q.trim() !== '';
  // Until the agents are in, every project would read idle.
  const loaded = !!agents.data;
  // With nothing above it, the Idle list is the list: open, and not a fold.
  const idleOnly = lists.pinned.length + lists.rest.length === 0;
  // Otherwise it is folded, and a click opens it only until something starts:
  // an agent starting never leaves it open under the project it started in.
  const [idleOpened, setIdleOpened] = useState(false);
  useEffect(() => setIdleOpened(false), [idleOnly]);
  // Docked at the bottom, it opens below the fold: a click scrolls it into view.
  const idleRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (idleOpened) idleRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [idleOpened]);
  // A search looks in it too, and opens it on a match.
  const idleOpen = idleOnly || idleOpened || (searching && lists.idle.length > 0);
  const hiddenOpen = kept.hiddenOpen || (searching && lists.hidden.length > 0) || (!!onScreen && lists.hidden.some((p) => p.project.name === onScreen));
  const nothing = projects.data && projects.data.length > 0 && lists.pinned.length + lists.rest.length + lists.idle.length + lists.hidden.length === 0;

  // Review all goes after the first list's label, whichever list is first.
  const openReview = useOpenReview();
  const toReview = reviewing ? (head.data?.total ?? 0) : 0;
  let reviewPlaced = false;
  const reviewAll = () => {
    if (reviewPlaced || !openReview || toReview === 0) return null;
    reviewPlaced = true;
    return (
      <Tip label={t('v2.review.allHint')}>
        <button
          type="button"
          onClick={() => openReview()}
          className="ml-1.5 shrink-0 rounded-full border border-line-strong bg-surface px-1.5 py-px text-[10.5px] font-medium normal-case tracking-normal tabular-nums text-secondary transition hover:border-line-heavy hover:bg-surface-strong hover:text-title"
          data-v2-review-all={toReview}
        >
          {t('v2.review.all', { count: toReview })}
        </button>
      </Tip>
    );
  };

  const lead = (p: ProjectRows, pin?: { draggable: true }) => (
    <ProjectLead
      key={p.project.name}
      project={p.project}
      all={p.all}
      shown={p.shown}
      approvals={p.approvals}
      open={p.open}
      pinned={pinned.has(p.project.name)}
      hidden={hiddenSet.has(p.project.name)}
      view={view}
      onToggle={() =>
        setExpanded((s) => new Map(s).set(p.project.name, !p.open))
      }
      onPin={() => {
        const pin = !pinned.has(p.project.name);
        setPins(togglePin(kept.pins, p.project.name));
        // Pinning a hidden project brings it back.
        if (pin && hiddenSet.has(p.project.name)) save(unhide(layout, p.project.name));
      }}
      onHide={() => {
        if (hiddenSet.has(p.project.name)) return save(unhide(layout, p.project.name));
        // A pin is never hidden: hiding one unpins it, so it moves to Hidden now.
        if (pinned.has(p.project.name)) setPins(pinsAfterHide(kept.pins, p.project.name));
        save(hide(layout, p.project.name));
      }}
      onSelect={onSelect}
      onNewAgent={() => onNewAgent(p.project.name)}
      drag={
        pin && {
          over: drop?.name === p.project.name ? drop.edge : undefined,
          onStart: () => setDragging(p.project.name),
          onEnd: () => {
            setDragging(null);
            setDrop(null);
          },
          onOver: (edge) => dragging && dragging !== p.project.name && setDrop({ name: p.project.name, edge }),
          onDrop: () => {
            if (dragging && drop) setPins(movePin(kept.pins, dragging, drop.name, drop.edge));
            setDragging(null);
            setDrop(null);
          },
        }
      }
    />
  );

  const { top, dock } = sections(lists);
  const section = (s: Section) => {
    if (s === 'pinned')
      return (
        <div key={s} data-v2-pinned>
          <ListHead extra={reviewAll()}>{t('v2.sidebar.pinned')}</ListHead>
          {lists.pinned.map((p) => lead(p, { draggable: true }))}
        </div>
      );
    if (s === 'rest')
      return (
        <div key={s} data-v2-rest>
          <ListHead extra={reviewAll()}>{t('shell.sidebar.projects')}</ListHead>
          {lists.rest.map((p) => lead(p))}
        </div>
      );
    if (s === 'idle')
      return (
        <div key={s} ref={idleRef} data-v2-idle={idleOnly ? 'only' : idleOpen ? 'open' : 'closed'}>
          {idleOnly ? (
            <ListHead extra={reviewAll()}>
              {t('v2.sidebar.idle')}
              <span className="ml-1.5 font-normal tabular-nums normal-case tracking-normal text-subtle">{lists.idle.length}</span>
            </ListHead>
          ) : (
            <button
              type="button"
              aria-expanded={idleOpen}
              onClick={() => setIdleOpened(!idleOpened)}
              className="flex w-full items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-left text-[12px] text-secondary hover:border-line-strong hover:bg-surface hover:text-title"
              data-v2-idle-toggle
            >
              <ChevronRight className={cn('size-3.5 shrink-0 transition-transform', idleOpen && 'rotate-90')} />
              <span className="font-medium">{t('v2.sidebar.idle')}</span>
              <span className="tabular-nums text-subtle">· {lists.idle.length}</span>
              <span className="min-w-0 truncate text-subtle">· {t('v2.sidebar.idleHint')}</span>
            </button>
          )}
          {idleOpen && <div className={cn(!idleOnly && 'opacity-80')}>{lists.idle.map((p) => lead(p))}</div>}
        </div>
      );
    return (
      <div key={s} className="mt-2 opacity-70 first:mt-0" data-v2-hidden={hiddenOpen ? 'open' : 'closed'}>
        <button
          type="button"
          aria-expanded={hiddenOpen}
          onClick={() => setHiddenOpen(!kept.hiddenOpen)}
          className="flex w-full items-center gap-1.5 rounded-md px-2.5 py-1 text-left text-[11.5px] text-subtle hover:bg-surface hover:text-secondary"
          data-v2-hidden-toggle
        >
          <ChevronRight className={cn('size-3 shrink-0 transition-transform', hiddenOpen && 'rotate-90')} />
          <EyeOff className="size-3 shrink-0" />
          <span>{t('v2.sidebar.hidden')}</span>
          <span className="tabular-nums">· {lists.hidden.length}</span>
        </button>
        {hiddenOpen && lists.hidden.map((p) => lead(p))}
      </div>
    );
  };

  return (
    <aside className="flex w-[288px] shrink-0 flex-col border-r border-line bg-rail backdrop-blur-xl" data-sidebar-v2>
      <div className={cn('flex h-14 shrink-0 items-center gap-2 px-4', nightly && 'nightly-sky')} data-nightly={nightly || undefined}>
        <Logo className="size-7" />
        {/* A nightly's badge goes under the name: beside it, the header's "Add project" overflows the 288px column. */}
        <div className="flex min-w-0 flex-col items-start gap-0.5">
          <span className="text-[14px] font-semibold tracking-tight text-title">AgentBox</span>
          {nightly && (
            <Tip label={t('shell.sidebar.nightlyTip', { version: info.data?.version ? `, ${info.data.version}` : '' })}>
              <span className="nightly-badge inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-px text-[10.5px] font-medium">
                <MoonStar className="size-3" aria-hidden />
                {t('shell.sidebar.nightly')}
              </span>
            </Tip>
          )}
        </div>
        <span className="ml-auto" />
        <HelpMenu />
        <button type="button" className="flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md px-1.5 py-1 text-[12px] text-subtle transition hover:bg-surface-strong hover:text-primary" onClick={onAddProject} data-v2-add-project>
          <FolderPlus className="size-4" />
          {t('shell.home.addProject')}
        </button>
      </div>

      {/* Never sideways: what's too long in it truncates (each row's flex children are min-w-0). */}
      <nav className="flex min-h-0 flex-1 flex-col overflow-y-auto overflow-x-hidden px-2 pb-3 pt-1 [&>*]:shrink-0" aria-label={t('v2.sidebar.chats')}>
        <NewTaskRow />
        <HomeRow active={view.kind === 'homeChat' || view.kind === 'home'} onClick={() => onSelect({ kind: 'homeChat' })} />
        <FilterBar filters={filters} onFilters={onFilters} projects={projects.data ?? []} rows={rows} />
        {projects.data?.length === 0 && (
          <button className="mx-2 mt-1 flex w-[calc(100%-1rem)] items-center gap-2 rounded-lg border border-dashed border-line-strong px-3 py-3 text-left text-[13px] text-subtle hover:border-line-heavy hover:text-tertiary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
            {t('shell.sidebar.addFirst')}
          </button>
        )}
        {loaded && top.map(section)}
        {loaded && nothing && <div className="px-2.5 py-1.5 text-[11.5px] text-subtle">{t('v2.sidebar.nothingMatches')}</div>}
        {/* Idle and Hidden dock at the bottom, just above the foot, the free space above them. */}
        {loaded && dock.length > 0 && (
          <div className="mt-auto pt-3" data-v2-dock>
            {dock.map(section)}
          </div>
        )}
      </nav>

      {/* The foot is one row of icons, each named by its tooltip: Settings,
          which opens straight away, Past agents, Media and Jobs, the places
          across every project, the one open highlighted, and the update check.
          Home, the chat and its Status tab, heads the list above; which
          AgentBox this window drives is under Settings → General. */}
      <div className="flex items-center gap-1 border-t border-line p-2" data-v2-foot>
        {(
          [
            { kind: 'settings', icon: Settings, label: t('common.settings'), alert: setup.data && !setup.data.ready },
            { kind: 'pastAgents', icon: Archive, label: t('pastAgents.title'), alert: false },
            { kind: 'media', icon: Images, label: t('shell.nav.media'), alert: false },
            { kind: 'jobs', icon: ListChecks, label: t('shell.nav.jobs'), alert: false },
          ] as const
        ).map(({ kind, icon: Icon, label, alert }) => {
          const active = view.kind === kind;
          return (
            <Tip key={kind} label={label}>
              <button
                type="button"
                aria-label={label}
                aria-current={active ? 'page' : undefined}
                onClick={() => onSelect({ kind })}
                className={cn(
                  'relative flex size-8 shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
                  active && 'bg-surface-raised text-title',
                )}
                data-v2-foot-item={kind}
              >
                <Icon className={cn('size-4', active ? 'text-brand-300' : 'text-subtle')} />
                {alert && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-amber-400" title={t('shell.sidebar.needsAttention')} aria-label={t('shell.sidebar.needsAttention').toLowerCase()} />}
              </button>
            </Tip>
          );
        })}
        <UpdateCheck />
      </div>
    </aside>
  );
}

function ListHead({ children, extra }: { children: ReactNode; extra?: ReactNode }) {
  return (
    <div className="flex h-7 items-center px-2.5 pt-2">
      <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{children}</span>
      {extra}
    </div>
  );
}

// FilterBar is the search box and the filter button beside it, and under
// them a chip for each filter that's on, each removable, with Reset.
function FilterBar({ filters, onFilters, projects, rows }: { filters: Filters; onFilters: (f: Filters) => void; projects: T.Project[]; rows: Row[] }) {
  const t = useT();
  const on = filterCount(filters);
  const counts = countSt(rows.map((r) => r.st));
  const chips: { key: string; label: string; st?: AgentSt; remove: () => void }[] = [
    ...filters.st.map((st) => ({ key: `st:${st}`, label: t(stLabel[st]), st, remove: () => onFilters({ ...filters, st: filters.st.filter((x) => x !== st) }) })),
    ...filters.projects.map((name) => ({
      key: `p:${name}`,
      label: projectLabel(name, projects),
      remove: () => onFilters({ ...filters, projects: filters.projects.filter((x) => x !== name) }),
    })),
  ];
  return (
    <div className="shrink-0 px-1 pb-0.5 pt-2" data-v2-filters>
      <div className="flex items-center gap-1.5">
        <label className="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg border border-line bg-surface-faint px-2 focus-within:border-line-heavy">
          <Search className="size-3.5 shrink-0 text-faint" />
          <input
            value={filters.q}
            onChange={(e) => onFilters({ ...filters, q: e.target.value })}
            onKeyDown={(e) => e.key === 'Escape' && filters.q && (e.stopPropagation(), onFilters({ ...filters, q: '' }))}
            placeholder={t('v2.filters.search')}
            aria-label={t('v2.filters.search')}
            className="min-w-0 flex-1 bg-transparent text-[12.5px] text-primary outline-none placeholder:text-faint"
            data-v2-search
          />
          {filters.q && (
            <button type="button" aria-label={t('v2.filters.clearSearch')} onClick={() => onFilters({ ...filters, q: '' })} className="text-faint hover:text-title">
              <X className="size-3.5" />
            </button>
          )}
        </label>
        <Popover>
          <Tip label={t('v2.filters.button')}>
          <PopoverTrigger asChild>
            <button
              type="button"
              aria-label={t('v2.filters.button')}
              className={cn(
                'relative flex size-8 shrink-0 items-center justify-center rounded-lg border transition',
                on ? 'border-line-heavy bg-surface-strong text-title' : 'border-line bg-surface-faint text-subtle hover:text-title',
              )}
              data-v2-filter-button
            >
              <ListFilter className="size-4" />
              {on > 0 && <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-surface-raised px-1 text-[9.5px] font-semibold text-title ring-1 ring-line-strong">{on}</span>}
            </button>
          </PopoverTrigger>
          </Tip>
          <PopoverContent align="start" className="grid w-[300px] gap-3 p-3" data-v2-filter-popover>
            <FilterGroup title={t('v2.filters.state')}>
              {liveOrder.map((st) => (
                <Chip key={st} on={filters.st.includes(st)} onClick={() => onFilters({ ...filters, st: toggle(filters.st, st) })} data={`st:${st}`}>
                  <StMark st={st} />
                  {t(stLabel[st])}
                  <span className="tabular-nums text-faint">{counts[st]}</span>
                </Chip>
              ))}
            </FilterGroup>
            {projects.length > 1 && (
              <FilterGroup title={t('v2.filters.project')}>
                {projects.map((p) => (
                  <Chip key={p.name} on={filters.projects.includes(p.name)} onClick={() => onFilters({ ...filters, projects: toggle(filters.projects, p.name) })} data={`p:${p.name}`}>
                    <span className="max-w-[180px] truncate">{projectLabel(p)}</span>
                  </Chip>
                ))}
              </FilterGroup>
            )}
          </PopoverContent>
        </Popover>
      </div>
      {chips.length > 0 && (
        <div className="mt-1.5 flex flex-wrap items-center gap-1">
          {chips.map((c) => (
            <span key={c.key} className="inline-flex max-w-full items-center gap-1 rounded-full border border-line bg-surface px-2 py-0.5 text-[11px] text-secondary" data-v2-chip={c.key}>
              {c.st && <StMark st={c.st} />}
              <span className="min-w-0 truncate">{c.label}</span>
              <button type="button" aria-label={t('v2.filters.remove', { name: c.label })} onClick={c.remove} className="-mr-0.5 text-faint hover:text-title">
                <X className="size-3" />
              </button>
            </span>
          ))}
          <button type="button" onClick={() => onFilters({ ...filters, st: [], projects: [] })} className="px-1 text-[11px] text-subtle hover:text-title" data-v2-reset>
            {t('v2.filters.reset')}
          </button>
        </div>
      )}
    </div>
  );
}

function FilterGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <div className="pb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{title}</div>
      <div className="flex flex-wrap gap-1">{children}</div>
    </div>
  );
}

function Chip({ on, onClick, data, children }: { on: boolean; onClick: () => void; data: string; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn('inline-flex min-w-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11.5px]', on ? 'border-line-heavy bg-surface-strong text-title' : 'border-line text-secondary hover:bg-surface')}
      data-v2-chip-option={data}
    >
      {on && <Check className="size-3 text-secondary" />}
      {children}
    </button>
  );
}

// StMark is an agent's one state mark: neutral for work and review, colour
// only for what needs you.
export function StMark({ st, className }: { st: AgentSt; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        'size-1.5 shrink-0 rounded-full',
        st === 'needs' && 'bg-amber-400',
        st === 'error' && 'bg-rose-400',
        st === 'review' && 'ring-1 ring-inset ring-secondary',
        st === 'working' && 'animate-pulse bg-secondary',
        st === 'done' && 'bg-faint',
        className,
      )}
      data-st={st}
    />
  );
}

// Lead is a chat's row. A click opens it; a right-click (or Ctrl+click on a
// Mac) is for the row's menu only, where it has one, and opens nothing. The
// rest of props is ContextMenuTrigger's, as its asChild child.
function Lead({ active, onClick, children, data, ...props }: { active: boolean; onClick: () => void; children: ReactNode; data?: Record<string, string> } & Omit<ComponentProps<'div'>, 'onClick'>) {
  return (
    <div
      {...props}
      role="button"
      tabIndex={0}
      aria-current={active ? 'page' : undefined}
      onClick={(e) => {
        if (e.button !== 0 || e.ctrlKey) return;
        onClick();
      }}
      onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onClick())}
      className={cn('group relative flex min-w-0 cursor-pointer items-start gap-2 rounded-lg px-2 py-2 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400', active ? 'bg-surface-raised' : 'hover:bg-surface')}
      {...data}
    >
      {children}
    </div>
  );
}

// NewTaskRow opens New task (NewTaskDialog): pick a project, or none for the
// Home chat, and its chat opens to write the task in.
function NewTaskRow() {
  const t = useT();
  return (
    <button
      type="button"
      onClick={openNewTask}
      className="group mb-1 flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] font-medium text-secondary transition-colors hover:bg-surface hover:text-title"
      data-v2-new-task
    >
      <span className="flex size-6 shrink-0 items-center justify-center">
        <SquarePen className="size-4" />
      </span>
      <span className="flex-1">{t('v2.sidebar.newTask')}</span>
      <kbd className="rounded-md bg-surface-raised px-1.5 font-mono text-[10.5px] text-subtle">{shortcut('N')}</kbd>
    </button>
  );
}

// HomeRow is the Home chat, across every project, with its last line.
function HomeRow({ active, onClick }: { active: boolean; onClick: () => void }) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', A.HomeProject], queryFn: () => api.projectChat(A.HomeProject), refetchInterval: 60_000 });
  const line = leadLine(chat.data?.last);
  const now = useNow(60_000);
  return (
    <Lead active={active} onClick={onClick} data={{ 'data-v2-home': '' }}>
      <span className="flex size-6 shrink-0 items-center justify-center rounded-md bg-surface-strong text-secondary">
        <House className="size-3.5" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[13px] font-medium text-title">{t('shell.nav.home')}</span>
          {chat.data?.chat === 'running' && <LoaderCircle className="size-3 shrink-0 animate-spin text-subtle" />}
          {chat.data?.last && <span className="ml-auto shrink-0 text-[11px] text-faint">{timeAgo(chat.data.last.at, now)}</span>}
        </span>
        <span className="block truncate text-[11.5px] text-subtle">{line ? (line.mine ? t('v2.sidebar.you', { text: line.text }) : line.text) : t('v2.sidebar.homeHint')}</span>
      </span>
    </Lead>
  );
}

type Drag = {
  over?: 'before' | 'after';
  onStart: () => void;
  onEnd: () => void;
  onOver: (edge: 'before' | 'after') => void;
  onDrop: () => void;
};

// ProjectLead is a project's lead, the main chat, with its icon, its last
// line and its live agents' counts; the chevron opens its agents under it,
// and a right-click on it opens its menu, the project header's ⋯
// (projectActions).
// A pinned one drags among the pins.
function ProjectLead({
  project,
  all,
  shown,
  approvals,
  open,
  pinned,
  hidden,
  view,
  onToggle,
  onPin,
  onHide,
  onSelect,
  onNewAgent,
  drag,
}: {
  project: T.Project;
  all: Row[];
  shown: Row[];
  approvals: number;
  open: boolean;
  pinned: boolean;
  hidden: boolean;
  view: View;
  onToggle: () => void;
  onPin: () => void;
  onHide: () => void;
  onSelect: (view: View) => void;
  onNewAgent: () => void;
  drag?: Drag;
}) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', project.name], queryFn: () => api.projectChat(project.name), refetchInterval: 60_000 });
  const now = useNow(60_000);
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage, enabled: open, refetchInterval: 5000 });
  const usageNow = usage.data?.agents ? usage.data : undefined;
  const history = useCpuHistory(usageNow);
  const [iconOpen, setIconOpen] = useState(false);
  const [talk, setTalk] = useState(false);
  // Talk to another project, picked: the menu, closing, mustn't take the
  // focus back to the row, outside the picker opening under it, which closed
  // the picker as it opened (ProjectMenu's guard, for its ⋯).
  const talking = useRef(false);
  const leadRef = `${project.name}/${A.LeadName}`;
  // Talk to another project needs another project.
  const others = (useQuery({ queryKey: ['projects'], queryFn: api.projects }).data?.length ?? 0) > 1;
  const line = leadLine(chat.data?.last);
  const counts = countSt(all.map((r) => r.st));
  // The lead at work counts as one working.
  if (leadBusy(chat.data?.chat)) counts.working++;
  const counted = counts.working + counts.review + counts.needs + counts.error > 0;
  const name = projectLabel(project);
  const active = view.kind === 'project' && view.project === project.name;
  const open_ = () => onSelect({ kind: 'project', project: project.name });
  const { main, more } = projectActions(t, {
    project,
    others,
    pinned,
    onNewTask: () => {
      open_();
      requestComposerFocus(leadRef);
    },
    onTalk: () => {
      talking.current = true;
      setTimeout(() => setTalk(true), 0);
    },
    onPin,
    onSettings: () => {
      open_();
      openPanelOn(leadRef, 'settings');
    },
    onChangeIcon: () => setIconOpen(true),
    onNewAgent,
  });
  const item = (a: ProjectAction) => [
    a.separatorBefore && <ContextMenuSeparator key={`${a.key}-sep`} />,
    <ContextMenuItem key={a.key} icon={a.icon} onSelect={a.onSelect} data-v2-project-action={a.key}>
      {a.label}
    </ContextMenuItem>,
  ];
  return (
    <div
      className={cn('relative mb-0.5 rounded-xl transition-colors', open && all.length > 0 && 'mb-1.5 bg-surface/50 pb-1 ring-1 ring-inset ring-line-faint', drag?.over === 'before' && 'before:absolute before:inset-x-2 before:-top-px before:h-0.5 before:rounded-full before:bg-brand-400', drag?.over === 'after' && 'after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-brand-400')}
      data-v2-project={project.name}
      draggable={!!drag}
      onDragStart={
        drag &&
        ((e) => {
          e.dataTransfer.effectAllowed = 'move';
          e.dataTransfer.setData('text/plain', project.name);
          drag.onStart();
        })
      }
      onDragEnd={drag?.onEnd}
      onDragOver={
        drag &&
        ((e) => {
          e.preventDefault();
          const box = e.currentTarget.getBoundingClientRect();
          drag.onOver(e.clientY < box.top + box.height / 2 ? 'before' : 'after');
        })
      }
      onDrop={
        drag &&
        ((e) => {
          e.preventDefault();
          drag.onDrop();
        })
      }
    >
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <Lead
            active={active}
            onClick={() => {
              onSelect({ kind: 'project', project: project.name });
              requestComposerFocus(leadRef);
            }}
            data={{ 'data-v2-lead': project.name }}
          >
            <Tip label={t(open ? 'v2.sidebar.hideAgents' : 'v2.sidebar.showAgents', { project: name })}>
              <button
                type="button"
                aria-label={t(open ? 'v2.sidebar.hideAgents' : 'v2.sidebar.showAgents', { project: name })}
                aria-expanded={open}
                onClick={(e) => {
                  e.stopPropagation();
                  onToggle();
                }}
                className={cn('mt-1 flex size-4 shrink-0 items-center justify-center rounded text-faint hover:bg-surface-strong hover:text-title', all.length === 0 && 'invisible')}
                data-v2-toggle={project.name}
              >
                <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
              </button>
            </Tip>
            <ProjectIcon project={project} className="mt-px" />
            <span className="min-w-0 flex-1">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[13px] font-medium text-title">{name}</span>
                {chat.data?.chat === 'running' && <LoaderCircle className="size-3 shrink-0 animate-spin text-subtle" />}
                {chat.data?.chat === 'waiting' && <span className="size-1.5 shrink-0 rounded-full bg-amber-400" aria-label={t('shell.sidebar.needsYou')} />}
                {chat.data?.last && <span className="ml-auto shrink-0 text-[11px] text-faint">{timeAgo(chat.data.last.at, now)}</span>}
              </span>
              {/* "N to review" ends this line, not the name's: there it left the name a few letters. */}
              <span className="flex min-w-0 items-center gap-2">
                <span className="min-w-0 flex-1 truncate text-[11.5px] text-subtle" data-v2-lead-line>
                  {line ? (line.mine ? t('v2.sidebar.you', { text: line.text }) : line.text) : t('v2.sidebar.leadHint')}
                </span>
                <ReviewCount project={project.name} variant="button" />
              </span>
              {/* Its agents' permission requests wait in this chat too, as cards. */}
              {approvals > 0 && (
                <span className="mt-1 flex items-center gap-1 text-[10.5px] font-medium tabular-nums text-amber-300" data-v2-approvals={approvals}>
                  <ShieldAlert className="size-3 shrink-0" />
                  {t('v2.sidebar.approvals', { count: approvals })}
                </span>
              )}
              {!open && counted && (
                <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10.5px] tabular-nums" data-v2-counts>
                  {countKeys.map(({ st, key }) =>
                    counts[st] ? (
                      <span key={st} className={cn('inline-flex items-center gap-1', st === 'needs' ? 'text-amber-300' : st === 'error' ? 'text-rose-300' : 'text-subtle')} data-v2-count={st}>
                        <StMark st={st} />
                        {t(key, { count: counts[st] })}
                      </span>
                    ) : null,
                  )}
                </span>
              )}
            </span>
          </Lead>
        </ContextMenuTrigger>
        <ContextMenuContent
          data-v2-project-menu={project.name}
          onCloseAutoFocus={(e) => {
            if (!talking.current) return;
            talking.current = false;
            e.preventDefault();
          }}
        >
          {[
            ...main.flatMap(item),
            <ContextMenuItem key="hide" icon={hidden ? Eye : EyeOff} onSelect={onHide} data-v2-project-action={hidden ? 'unhide' : 'hide'}>
              {t(hidden ? 'v2.sidebar.unhide' : 'v2.sidebar.hide')}
            </ContextMenuItem>,
            <ContextMenuMore key="more" label={t('common.more')}>{more.flatMap(item)}</ContextMenuMore>,
          ]}
        </ContextMenuContent>
      </ContextMenu>
      {/* Talk to another project's picker hangs under the row, from a point out
          of the flow: TalkToProject wraps its anchor in an inline-flex span,
          which here, in the row's block, would add a line under the preview. */}
      <span aria-hidden className="pointer-events-none absolute right-2 top-11 size-0">
        <TalkToProject project={project.name} leadRef={leadRef} open={talk} onOpenChange={setTalk} onPicked={open_} anchor={<span className="block size-0" />} />
      </span>
      <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />
      {open && (
        <div className="relative ml-[15px] mr-1 mt-0.5 flex flex-col gap-px border-l border-line pl-[7px]" data-v2-agents={project.name}>
          {shown.map((r) => (
            <Fragment key={r.agent.ref}>
              <AgentRow row={r} active={view.kind === 'agent' && view.ref === r.agent.ref} onSelect={onSelect} sample={usageNow?.agents.find((u) => u.ref === r.agent.ref)} cpu={history.get(r.agent.ref) ?? []} />
            </Fragment>
          ))}
          {shown.length === 0 && <div className="px-2 py-1 text-[11.5px] text-faint">{t(all.length === 0 ? 'v2.sidebar.noAgents' : 'v2.sidebar.noneMatch')}</div>}
        </div>
      )}
    </div>
  );
}

// AgentRow is one agent under its lead, two lines beside its avatar: its
// title, and under it its number and where it is. Colour is only for what
// needs you; a running machine adds a quiet CPU graph and its share, with
// memory and disk in the tooltip. A right-click opens its menu
// (AgentContextMenu) and nothing else; while it is being destroyed it reads
// "removing…".
function AgentRow({ row, active, onSelect, sample, cpu }: { row: Row; active: boolean; onSelect: (view: View) => void; sample?: T.AgentUsage; cpu: number[] }) {
  const t = useT();
  const { agent, st } = row;
  const removing = useRemoving().has(agent.ref);
  const mood = useMood(agent);
  const label = chatLabel(agent);
  const live = !removing && st === 'working' && agent.state === 'running' && sample;
  const status = removing
    ? t('v2.tag.removing')
    : st === 'needs'
      ? t(awaitsApproval(agent) ? 'v2.tag.approval' : 'v2.tag.asks')
      : st === 'review' || st === 'done'
        ? t(stLabel[st])
        : label.text;
  const usageTip = live ? t('project.all.usage', { cpu: sample.cpu.toFixed(0), memory: humanBytes(sample.memory), read: humanRate(sample.diskRead), write: humanRate(sample.diskWrite) }) : '';
  return (
    <AgentContextMenu agent={agent} active={active} onSelect={onSelect}>
      <button
        type="button"
        onClick={(e) => {
          if (e.button !== 0 || e.ctrlKey) return;
          onSelect({ kind: 'agent', ref: agent.ref });
        }}
        onContextMenu={(e) => e.stopPropagation()}
        aria-current={active ? 'page' : undefined}
        title={[agent.title ? `${agent.name} · ${agent.title}` : agent.name, usageTip].filter(Boolean).join('\n')}
        className={cn(
          'group relative flex w-full min-w-0 items-center gap-2.5 rounded-lg py-1.5 pl-2 pr-2.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400',
          active ? 'bg-surface-raised' : 'hover:bg-surface',
          removing && 'opacity-60',
        )}
        data-v2-agent={agent.ref}
        data-v2-agent-st={st}
        data-v2-removing={removing || undefined}
      >
        {active && <span aria-hidden className="absolute inset-y-2 left-0 w-[2px] rounded-full bg-brand-400" />}
        <AgentAvatar ai={agent.ai} mood={mood} state={agent.state} seed={agent.ref} className="size-7 rounded-lg" />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="flex min-w-0 items-center gap-2">
            <span className={cn('min-w-0 flex-1 truncate text-[12.5px] font-medium leading-[18px]', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-primary', !agent.title && 'font-mono text-[11.5px]')}>{agent.title || agent.name}</span>
            {live && <Sparkline values={cpu} className="text-faint group-hover:text-subtle" />}
          </span>
          <span className="flex min-w-0 items-center gap-1 text-[11px] leading-4">
            {agent.title && (
              <>
                <span className="min-w-0 max-w-[55%] truncate font-mono text-[10.5px] text-faint" data-v2-agent-num>
                  {agent.name}
                </span>
                <span aria-hidden className="shrink-0 text-ghost">·</span>
              </>
            )}
            <span
              className={cn('min-w-0 truncate', st === 'needs' ? 'font-medium text-amber-300' : st === 'error' ? 'font-medium text-rose-300' : 'text-subtle')}
              data-v2-tag={attention(st) || removing || undefined}
            >
              {status}
              {!removing && agent.changesRequested && st === 'working' && ` · ${t('v2.tag.changes')}`}
            </span>
            {live && <span className="ml-auto shrink-0 pl-1 font-mono text-[10px] tabular-nums text-faint">{sample.cpu.toFixed(0)}%</span>}
          </span>
        </span>
      </button>
    </AgentContextMenu>
  );
}

// UpdateCheck is the foot's update button, an icon as T3Code has it: Check
// for updates asks the daemon for the latest release now (GET /v1/update/release,
// which also makes a newer one the offer); once there is one, it updates the
// app in place, or opens the release page where this install can't.
function UpdateCheck() {
  const t = useT();
  const queryClient = useQueryClient();
  const update = useQuery({ queryKey: ['update'], queryFn: api.update, staleTime: Infinity });
  const app = useAppUpdate();
  const [checking, setChecking] = useState(false);
  const current = update.data?.current ?? '';
  const offer = update.data?.available && isUpgrade(update.data.available.version, current) ? update.data.available : undefined;
  const check = async () => {
    setChecking(true);
    try {
      const latest = await api.latestRelease();
      if (isUpgrade(latest.version, current)) await queryClient.invalidateQueries({ queryKey: ['update'] });
      else toast(t('v2.update.latest', { version: current }));
    } catch (err) {
      toast.error(t('v2.update.failed'), { description: errorMessage(err) });
    } finally {
      setChecking(false);
    }
  };
  if (offer) {
    const label = app.label ?? t('v2.update.to', { version: offer.version });
    return (
      <Tip label={app.updating ? label : `${label} · ${updateHint(app.support, offer.version)}`}>
        <button
          type="button"
          aria-label={label}
          disabled={app.updating}
          onClick={() => void app.start(offer.url)}
          className="ml-auto flex size-8 shrink-0 items-center justify-center rounded-lg text-emerald-300 transition hover:bg-emerald-500/15 disabled:cursor-progress"
          data-v2-update={offer.version}
        >
          {app.updating ? <LoaderCircle className="size-4 animate-spin" /> : <CircleArrowUp className="size-4" />}
        </button>
      </Tip>
    );
  }
  const label = checking ? t('v2.update.checking') : t('v2.update.check');
  return (
    <Tip label={label}>
      <button
        type="button"
        aria-label={label}
        disabled={checking}
        onClick={() => void check()}
        className="ml-auto flex size-8 shrink-0 items-center justify-center rounded-lg text-muted transition hover:bg-surface hover:text-primary disabled:cursor-progress"
        data-v2-update-check
      >
        <RefreshCw className={cn('size-4', checking && 'animate-spin')} />
      </button>
    </Tip>
  );
}
