// UI v2's sidebar, lead-first: most of the time is spent talking to each
// project's lead, so the list is Home and one row per lead, with its icon,
// its last line and its live agents' counts (working, to review, asks,
// failed). Each project's live agents fold under it, one quiet row each:
// neutral text and one small state mark, with colour only for what needs you
// (asks, failed); finished ones are in Past agents. Under Home, search and a
// filter popover (T3Code's: state, project, removable chips) narrow it. The
// projects the user pinned come first, in the order they dragged them, then
// the ones with something going on by the sort menu's switch, then, folded,
// the Idle list of the rest, which opens by itself when nothing is going on.
// A project's options are on a right-click of its row. The reading is
// lib/leadFirst.ts's and lib/projectOrder.ts's.
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { ArrowLeftRight, ArrowUpDown, Check, ChevronRight, CircleArrowUp, FolderOpen, FolderPlus, House, ListChecks, ListFilter, LoaderCircle, Pin, PinOff, Plus, RefreshCw, Search, Settings, ShieldAlert, Smile, SquarePen, X } from 'lucide-react';
import { useEffect, useMemo, useState, type ComponentProps, type ReactNode } from 'react';
import * as A from '../../../shared/api';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { pageItems, useQuestions, useReviewHead } from '../../lib/inboxPages';
import { activeProjects, agentSt, attention, awaitsApproval, countSt, filterCount, leadLine, leadBusy, liveOrder, projectRows, toggle, type AgentSt, type Filters, type ProjectRows, type Row } from '../../lib/leadFirst';
import { movePin, orderProjects, sortOptions, togglePin, type SortBy } from '../../lib/projectOrder';
import { setFilterChoice, setIdleOpen, setPins, setSort, useV2Projects } from '../../lib/v2Projects';
import { useGrace } from '../../lib/grace';
import { projectLabel } from '../../lib/projectName';
import { useReviewing } from '../../lib/reviewAlert';
import { useNow } from '../../lib/useNow';
import { updateHint } from '../../lib/appUpdate';
import { isUpgrade } from '../../lib/nightly';
import { useAppUpdate } from '../../lib/useAppUpdate';
import { cn, errorMessage, shortcut, timeAgo } from '../../lib/utils';
import { openNewTask, requestComposerFocus } from '../../lib/newTask';
import { useRemoving } from '../../lib/removing';
import { openPanelOn } from '../../lib/rightPanel';
import { AgentContextMenu } from '../AgentContextMenu';
import { EnvironmentSwitcher } from '../EnvironmentSwitcher';
import { Logo } from '../Sidebar';
import { ProjectIcon, ProjectIconDialog } from '../ProjectIcon';
import { ReviewCount } from './ReviewV2';
import { TalkToProject } from './TalkToProject';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '../ui/context-menu';
import { Menu, MenuCheckboxItem, MenuContent, MenuLabel, MenuTrigger } from '../ui/menu';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';

// The views Settings' popover opens, which light its icon up.
const menuViews = new Set<View['kind']>(['settings', 'home', 'pastAgents', 'media', 'jobs']);

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
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
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
  const running = jobs.data?.filter((j) => j.status === 'running').length ?? 0;
  const [menuOpen, setMenuOpen] = useState(false);

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
  const active = useGrace(activeNow);

  const order = useMemo(() => {
    const working = new Map<string, number>();
    for (const name of busy) working.set(name, 1);
    for (const r of rows) if (r.st === 'working') working.set(r.agent.project, (working.get(r.agent.project) ?? 0) + 1);
    return orderProjects({
      projects: projects.data ?? [],
      pins: kept.pins,
      sort: kept.sort,
      activity: new Map((activity.data ?? []).map((a) => [a.project, a])),
      working,
      active,
      label,
    });
    // label is projectLabel's, the same each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projects.data, activity.data, rows, busy, active, kept.pins, kept.sort]);
  const pinned = new Set(kept.pins);
  const ctx = { busy, keep: pinned };
  const lists = {
    pinned: projectRows(order.pinned, rows, filters, expanded, label, ctx),
    rest: projectRows(order.rest, rows, filters, expanded, label, ctx),
    idle: projectRows(order.idle, rows, filters, expanded, label, ctx),
  };
  // Until the agents are in, every project would read idle.
  const loaded = !!agents.data;
  // With nothing above it, the Idle list is the list: open, and not a fold.
  const idleOnly = lists.pinned.length + lists.rest.length === 0;
  // A search looks in it too, and opens it on a match.
  const idleOpen = idleOnly || kept.idleOpen || (q.trim() !== '' && lists.idle.length > 0);
  const nothing = projects.data && projects.data.length > 0 && lists.pinned.length + lists.rest.length + lists.idle.length === 0;

  const lead = (p: ProjectRows, pin?: { draggable: true }) => (
    <ProjectLead
      key={p.project.name}
      project={p.project}
      all={p.all}
      shown={p.shown}
      approvals={p.approvals}
      open={p.open}
      pinned={pinned.has(p.project.name)}
      view={view}
      onToggle={() =>
        setExpanded((s) => new Map(s).set(p.project.name, !p.open))
      }
      onPin={() => setPins(togglePin(kept.pins, p.project.name))}
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

  return (
    <aside className="flex w-[288px] shrink-0 flex-col border-r border-line bg-rail backdrop-blur-xl" data-sidebar-v2>
      <div className="flex h-14 shrink-0 items-center gap-2.5 px-4">
        <Logo className="size-7" />
        <span className="text-[14px] font-semibold tracking-tight text-title">AgentBox</span>
        <Tip label={t('shell.sidebar.newProject')}>
          <button aria-label={t('shell.sidebar.newProject')} className="ml-auto rounded-md p-1.5 text-subtle transition hover:bg-surface-strong hover:text-primary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
          </button>
        </Tip>
      </div>

      <nav className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 pt-1" aria-label={t('v2.sidebar.chats')}>
        <NewTaskRow />
        <HomeRow active={view.kind === 'homeChat'} onClick={() => onSelect({ kind: 'homeChat' })} />
        <FilterBar filters={filters} onFilters={onFilters} projects={projects.data ?? []} rows={rows} />
        {projects.data?.length === 0 && (
          <button className="mx-2 mt-1 flex w-[calc(100%-1rem)] items-center gap-2 rounded-lg border border-dashed border-line-strong px-3 py-3 text-left text-[13px] text-subtle hover:border-line-heavy hover:text-tertiary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
            {t('shell.sidebar.addFirst')}
          </button>
        )}
        {loaded && lists.pinned.length > 0 && (
          <div data-v2-pinned>
            <ListHead>{t('v2.sidebar.pinned')}</ListHead>
            {lists.pinned.map((p) => lead(p, { draggable: true }))}
          </div>
        )}
        {loaded && lists.rest.length > 0 && (
          <div data-v2-rest>
            <ListHead action={<SortMenu />}>{t('shell.sidebar.projects')}</ListHead>
            {lists.rest.map((p) => lead(p))}
          </div>
        )}
        {loaded && nothing && <div className="px-2.5 py-1.5 text-[11.5px] text-subtle">{t('v2.sidebar.nothingMatches')}</div>}
        {loaded && lists.idle.length > 0 && (
          <div className={cn(!idleOnly && 'mt-2')} data-v2-idle={idleOnly ? 'only' : idleOpen ? 'open' : 'closed'}>
            {idleOnly ? (
              <ListHead action={<SortMenu />}>
                {t('v2.sidebar.idle')}
                <span className="ml-1.5 font-normal tabular-nums normal-case tracking-normal text-faint">{lists.idle.length}</span>
              </ListHead>
            ) : (
              <button
                type="button"
                aria-expanded={idleOpen}
                onClick={() => setIdleOpen(!kept.idleOpen)}
                className="flex w-full items-center gap-1.5 rounded-md px-2.5 py-1.5 text-left text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle hover:bg-surface hover:text-secondary"
                data-v2-idle-toggle
              >
                <ChevronRight className={cn('size-3 transition-transform', idleOpen && 'rotate-90')} />
                {t('v2.sidebar.idle')}
                <span className="font-normal tabular-nums normal-case tracking-normal text-faint">{lists.idle.length}</span>
              </button>
            )}
            {idleOpen && <div className={cn(!idleOnly && 'opacity-80')}>{lists.idle.map((p) => lead(p))}</div>}
          </div>
        )}
      </nav>

      {/* The foot is two icons: Settings, whose popover has the environment,
          Settings and Jobs (Overview, Media and Past agents are sections of
          Settings in v2), and the update check. The leads keep the room. */}
      <div className="flex items-center gap-2 border-t border-line p-2">
        <Popover open={menuOpen} onOpenChange={setMenuOpen}>
          <PopoverTrigger asChild>
            <button
              type="button"
              aria-label={t('common.settings')}
              className={cn(
                'relative flex size-8 shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface hover:text-primary data-[state=open]:bg-surface data-[state=open]:text-title',
                menuViews.has(view.kind) && 'bg-surface-raised text-title',
              )}
              data-v2-settings-menu
            >
              <Settings className="size-4" />
              {((setup.data && !setup.data.ready) || running > 0) && (
                <span
                  className={cn('absolute right-1 top-1 size-1.5 rounded-full', setup.data && !setup.data.ready ? 'bg-amber-400' : 'bg-secondary')}
                  aria-label={setup.data && !setup.data.ready ? t('shell.sidebar.needsAttention').toLowerCase() : undefined}
                />
              )}
            </button>
          </PopoverTrigger>
          <PopoverContent side="top" align="start" className="grid w-[264px] gap-0.5 p-1.5" data-v2-settings-popover>
            {/* Which AgentBox this window drives: this machine or a hub's. */}
            <div className="px-1 pb-1.5 pt-0.5">
              <EnvironmentSwitcher />
            </div>
            {(
              [
                { kind: 'settings', icon: Settings, label: t('common.settings'), hint: setup.data && !setup.data.ready ? t('shell.sidebar.needsAttention') : undefined },
                { kind: 'jobs', icon: ListChecks, label: t('shell.nav.jobs'), hint: running > 0 ? t('shell.sidebar.running', { count: running }) : undefined },
              ] as const
            ).map(({ kind, icon: Icon, label, hint }) => (
              <button
                key={kind}
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  onSelect({ kind });
                }}
                className={cn('flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] text-secondary transition-colors hover:bg-surface-raised', view.kind === kind && 'bg-surface text-title')}
                data-v2-menu={kind}
              >
                <Icon className="size-4 text-muted" />
                {label}
                {hint && <span className="ml-auto text-[11px] tabular-nums text-subtle">{hint}</span>}
              </button>
            ))}
          </PopoverContent>
        </Popover>
        <UpdateCheck />
      </div>
    </aside>
  );
}

function ListHead({ action, children }: { action?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-7 items-center px-2.5 pt-2">
      <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{children}</span>
      {action && <span className="ml-auto">{action}</span>}
    </div>
  );
}

const sortLabel: Record<SortBy, MessageKey> = {
  recent: 'v2.sort.recent',
  name: 'v2.sort.name',
  working: 'v2.sort.working',
};

// SortMenu is the switch for how the projects below the pinned ones sort.
function SortMenu() {
  const t = useT();
  const { sort } = useV2Projects();
  return (
    <Menu>
      <Tip label={t('v2.sort.label', { sort: t(sortLabel[sort]) })}>
        <MenuTrigger asChild>
          <button type="button" aria-label={t('v2.sort.label', { sort: t(sortLabel[sort]) })} className="flex size-5 items-center justify-center rounded text-faint hover:bg-surface-strong hover:text-title data-[state=open]:text-title" data-v2-sort={sort}>
            <ArrowUpDown className="size-3.5" />
          </button>
        </MenuTrigger>
      </Tip>
      <MenuContent align="end" className="w-52">
        <MenuLabel>{t('v2.sort.title')}</MenuLabel>
        {sortOptions.map((o) => (
          <MenuCheckboxItem key={o} checked={sort === o} onCheckedChange={() => setSort(o)} data-v2-sort-option={o}>
            {t(sortLabel[o])}
          </MenuCheckboxItem>
        ))}
      </MenuContent>
    </Menu>
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
      onClick={(e) => {
        if (e.button !== 0 || e.ctrlKey) return;
        onClick();
      }}
      onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onClick())}
      className={cn('group relative flex min-w-0 cursor-pointer items-start gap-2 rounded-lg px-2 py-2 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400/50', active ? 'bg-surface-raised' : 'hover:bg-surface')}
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
// and a right-click on it opens its menu (what the project header's ⋯ has:
// New agent, Talk to another project, its icon, folder, settings and pin).
// A pinned one drags among the pins.
function ProjectLead({
  project,
  all,
  shown,
  approvals,
  open,
  pinned,
  view,
  onToggle,
  onPin,
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
  view: View;
  onToggle: () => void;
  onPin: () => void;
  onSelect: (view: View) => void;
  onNewAgent: () => void;
  drag?: Drag;
}) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', project.name], queryFn: () => api.projectChat(project.name), refetchInterval: 60_000 });
  const now = useNow(60_000);
  const [iconOpen, setIconOpen] = useState(false);
  const [talk, setTalk] = useState(false);
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
  return (
    <div
      className={cn('relative mb-0.5', drag?.over === 'before' && 'before:absolute before:inset-x-2 before:-top-px before:h-0.5 before:rounded-full before:bg-brand-400', drag?.over === 'after' && 'after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-brand-400')}
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
            <ProjectIcon project={project} className="mt-px" />
            <span className="min-w-0 flex-1">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[13px] font-medium text-title">{name}</span>
                {chat.data?.chat === 'running' && <LoaderCircle className="size-3 shrink-0 animate-spin text-subtle" />}
                {chat.data?.chat === 'waiting' && <span className="size-1.5 shrink-0 rounded-full bg-amber-400" aria-label={t('shell.sidebar.needsYou')} />}
                {chat.data?.last && <span className="ml-auto shrink-0 text-[11px] text-faint">{timeAgo(chat.data.last.at, now)}</span>}
              </span>
              <span className="block truncate text-[11.5px] text-subtle" data-v2-lead-line>
                {line ? (line.mine ? t('v2.sidebar.you', { text: line.text }) : line.text) : t('v2.sidebar.leadHint')}
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
            <ReviewCount project={project.name} variant="button" />
          </Lead>
        </ContextMenuTrigger>
        <ContextMenuContent data-v2-project-menu={project.name}>
          <ContextMenuItem icon={Plus} onSelect={onNewAgent} data-v2-new-agent={project.name}>
            {t('project.view.newAgent')}
          </ContextMenuItem>
          {others && (
            <ContextMenuItem icon={ArrowLeftRight} onSelect={() => setTimeout(() => setTalk(true), 0)} data-v2-talk-to={project.name}>
              {t('v2.talk.button')}
            </ContextMenuItem>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem icon={pinned ? PinOff : Pin} onSelect={onPin} data-v2-pin={project.name}>
            {pinned ? t('v2.sidebar.unpin') : t('v2.sidebar.pin')}
          </ContextMenuItem>
          <ContextMenuItem icon={Smile} onSelect={() => setIconOpen(true)} data-v2-change-icon={project.name}>
            {t('projectIcon.change')}
          </ContextMenuItem>
          <ContextMenuItem icon={FolderOpen} onSelect={() => void window.agentbox.openPath(project.root)} data-v2-open-folder={project.name}>
            {t('project.view.openFolder')}
          </ContextMenuItem>
          <ContextMenuItem
            icon={Settings}
            onSelect={() => {
              onSelect({ kind: 'project', project: project.name });
              openPanelOn(leadRef, 'settings');
            }}
            data-v2-project-settings={project.name}
          >
            {t('v2.sidebar.projectSettings')}
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      {/* Talk to another project's picker hangs under the row. */}
      <TalkToProject project={project.name} leadRef={leadRef} open={talk} onOpenChange={setTalk} anchor={<span aria-hidden className="pointer-events-none absolute right-2 top-11 size-0" />} />
      <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />
      {open && (
        <div className="relative ml-[17px] border-l border-line-faint pl-1.5" data-v2-agents={project.name}>
          {shown.map((r) => (
            <AgentRow key={r.agent.ref} row={r} active={view.kind === 'agent' && view.ref === r.agent.ref} onSelect={onSelect} />
          ))}
          {shown.length === 0 && <div className="px-2 py-1 text-[11.5px] text-faint">{t(all.length === 0 ? 'v2.sidebar.noAgents' : 'v2.sidebar.noneMatch')}</div>}
        </div>
      )}
    </div>
  );
}

// AgentRow is one agent under its lead: its state mark, its title in
// neutral text, a word only when it needs you, and when it last changed.
// A right-click opens its menu (AgentContextMenu) and nothing else; while it
// is being destroyed it reads "removing…".
function AgentRow({ row, active, onSelect }: { row: Row; active: boolean; onSelect: (view: View) => void }) {
  const t = useT();
  const { agent, st } = row;
  const removing = useRemoving().has(agent.ref);
  return (
    <AgentContextMenu agent={agent} active={active} onSelect={onSelect}>
      <button
        type="button"
        onClick={(e) => {
          if (e.button !== 0 || e.ctrlKey) return;
          onSelect({ kind: 'agent', ref: agent.ref });
        }}
        onContextMenu={(e) => e.stopPropagation()}
        className={cn('flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left', active ? 'bg-surface-raised' : 'hover:bg-surface', removing && 'opacity-60')}
        data-v2-agent={agent.ref}
        data-v2-agent-st={st}
        data-v2-removing={removing || undefined}
      >
        {removing ? <LoaderCircle className="size-[9px] shrink-0 animate-spin text-subtle" /> : <StMark st={st} className="size-[7px]" />}
        <span className={cn('min-w-0 flex-1 truncate text-[12px]', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-secondary', !agent.title && 'font-mono text-[11.5px]')}>{agent.title || agent.name}</span>
        {removing ? (
          <span className="shrink-0 text-[10.5px] text-subtle" data-v2-tag>
            {t('v2.tag.removing')}
          </span>
        ) : (
          attention(st) && (
            <span className="shrink-0 text-[10.5px] text-subtle" data-v2-tag>
              {t(st === 'error' ? 'v2.tag.failed' : awaitsApproval(agent) ? 'v2.tag.approval' : 'v2.tag.asks')}
            </span>
          )
        )}
        {!removing && agent.changesRequested && st === 'working' && <span className="shrink-0 text-[10.5px] text-subtle">{t('v2.tag.changes')}</span>}
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
