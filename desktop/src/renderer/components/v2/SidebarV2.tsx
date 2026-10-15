// UI v2's sidebar, lead-first: most of the time is spent talking to each
// project's lead, so the list is Home and one row per lead, its last line and
// its agents' counts (working, to review, asks, failed). Each project's
// agents fold under it, closed by default, one quiet row each: neutral text
// and one small state mark, with colour only for what needs you (asks,
// failed). Search and a filter popover (T3Code's: state, project, removable
// chips) narrow it; narrowing by state or text opens the projects with a match.
// The reading is lib/leadFirst.ts's.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Archive, Check, ChevronRight, CircleArrowUp, FolderPlus, House, Images, ListChecks, ListFilter, LoaderCircle, Plus, RefreshCw, Search, Settings, X } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';
import * as A from '../../../shared/api';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { pageItems, useQuestions, useReviewHead } from '../../lib/inboxPages';
import { agentSt, attention, countSt, filterCount, foldDone, leadLine, narrowing, noFilters, projectRows, stOrder, toggle, type AgentSt, type Filters, type Row } from '../../lib/leadFirst';
import { projectLabel } from '../../lib/projectName';
import { useReviewing } from '../../lib/reviewAlert';
import { buildLists } from '../../lib/sidebar';
import { useNow } from '../../lib/useNow';
import { updateHint } from '../../lib/appUpdate';
import { isUpgrade } from '../../lib/nightly';
import { useAppUpdate } from '../../lib/useAppUpdate';
import { cn, errorMessage, timeAgo } from '../../lib/utils';
import { EnvironmentSwitcher } from '../EnvironmentSwitcher';
import { shownKinds } from '../AllMediaView';
import { Logo } from '../Sidebar';
import { useMediaCounts } from '../../lib/mediaPages';
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
  const sections = useQuery({ queryKey: ['sections'], queryFn: api.sections });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  const reviewing = useReviewing();
  const head = useReviewHead(reviewing);
  const questions = useQuestions();
  const [filters, setFilters] = useState<Filters>(noFilters);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(new Set());
  const running = jobs.data?.filter((j) => j.status === 'running').length ?? 0;
  const [menuOpen, setMenuOpen] = useState(false);
  const unseen = useMediaCounts({ kinds: shownKinds }).data?.unseen ?? 0;

  // The projects in the sidebar's order, sections or not.
  const ordered = useMemo(() => buildLists(projects.data ?? [], sections.data ?? []).flatMap((l) => l.projects), [projects.data, sections.data]);
  const rows: Row[] = useMemo(() => {
    const ctx = {
      review: new Set((reviewing ? (head.data?.items ?? []) : []).map((i) => i.ref)),
      asking: new Set(pageItems(questions.data).map((n) => n.ref)),
    };
    return (agents.data ?? []).map((agent) => ({ agent, st: agentSt(agent, ctx) }));
  }, [agents.data, head.data, questions.data, reviewing]);
  const label = (p: T.Project) => projectLabel(p);
  const list = projectRows(ordered, rows, filters, expanded, label);
  const hidden = ordered.length - list.length;
  const narrow = narrowing(filters);
  const clear = () => setFilters(noFilters);
  // The agent on screen opens its project, so its row is in view.
  const current = view.kind === 'agent' ? view.ref : undefined;
  const currentProject = current?.split('/')[0];

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
      <FilterBar filters={filters} onFilters={setFilters} projects={ordered} rows={rows} />

      <nav className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 pt-1" aria-label={t('v2.sidebar.chats')}>
        <HomeRow active={view.kind === 'homeChat'} onClick={() => onSelect({ kind: 'homeChat' })} />
        <div className="flex items-center px-2.5 pb-1 pt-3">
          <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{t('shell.sidebar.projects')}</span>
        </div>
        {projects.data?.length === 0 && (
          <button className="mx-2 mt-1 flex w-[calc(100%-1rem)] items-center gap-2 rounded-lg border border-dashed border-line-strong px-3 py-3 text-left text-[13px] text-subtle hover:border-line-heavy hover:text-tertiary" onClick={onAddProject}>
            <FolderPlus className="size-4" />
            {t('shell.sidebar.addFirst')}
          </button>
        )}
        {list.map((p) => (
          <ProjectLead
            key={p.project.name}
            project={p.project}
            all={p.all}
            shown={p.shown}
            open={p.open || (!narrow && currentProject === p.project.name)}
            filters={filters}
            view={view}
            unfolded={unfolded.has(p.project.name)}
            onUnfold={() => setUnfolded((s) => new Set(s).add(p.project.name))}
            onToggle={() =>
              setExpanded((s) => {
                const next = new Set(s);
                if (next.has(p.project.name)) next.delete(p.project.name);
                else next.add(p.project.name);
                return next;
              })
            }
            onSelect={onSelect}
            onNewAgent={onNewAgent}
          />
        ))}
        {(hidden > 0 || (narrow && list.length === 0)) && projects.data && projects.data.length > 0 && (
          <div className="mx-2 mt-2 flex items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-[11.5px] text-subtle" data-v2-hidden={hidden}>
            {list.length === 0 ? t('v2.sidebar.nothingMatches') : t('v2.sidebar.hidden', { count: hidden })}
            <button type="button" onClick={clear} className="ml-auto font-medium text-secondary hover:text-title hover:underline">
              {t('v2.filters.clear')}
            </button>
          </div>
        )}
      </nav>

      {/* The foot is two icons: Settings, whose popover has the environment,
          Overview, Past agents, Media and Jobs, and the update check. The
          leads keep the room. */}
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
              {((setup.data && !setup.data.ready) || running > 0 || unseen > 0) && (
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
                { kind: 'home', icon: House, label: t('v2.sidebar.overview') },
                { kind: 'pastAgents', icon: Archive, label: t('pastAgents.title') },
                { kind: 'media', icon: Images, label: t('shell.nav.media'), hint: unseen > 0 ? String(unseen) : undefined },
                { kind: 'jobs', icon: ListChecks, label: t('shell.nav.jobs'), hint: running > 0 ? t('shell.sidebar.running', { count: running }) : undefined },
              ] as const
            ).map(({ kind, icon: Icon, label, ...rest }) => (
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
                {'hint' in rest && rest.hint && <span className="ml-auto text-[11px] tabular-nums text-subtle">{rest.hint}</span>}
              </button>
            ))}
          </PopoverContent>
        </Popover>
        <UpdateCheck />
      </div>
    </aside>
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
    <div className="shrink-0 px-3 pb-1.5" data-v2-filters>
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
              {stOrder.map((st) => (
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

function Lead({ active, onClick, children, data }: { active: boolean; onClick: () => void; children: ReactNode; data?: Record<string, string> }) {
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onClick())}
      className={cn('group flex min-w-0 cursor-pointer items-start gap-2 rounded-lg px-2 py-2 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400/50', active ? 'bg-surface-raised' : 'hover:bg-surface')}
      {...data}
    >
      {children}
    </div>
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

// ProjectLead is a project's lead, the main chat, with its last line and its
// agents' counts; the chevron opens its agents under it.
function ProjectLead({
  project,
  all,
  shown,
  open,
  filters,
  view,
  unfolded,
  onUnfold,
  onToggle,
  onSelect,
  onNewAgent,
}: {
  project: T.Project;
  all: Row[];
  shown: Row[];
  open: boolean;
  filters: Filters;
  view: View;
  unfolded: boolean;
  onUnfold: () => void;
  onToggle: () => void;
  onSelect: (view: View) => void;
  onNewAgent: (project: string) => void;
}) {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', project.name], queryFn: () => api.projectChat(project.name), refetchInterval: 60_000 });
  const now = useNow(60_000);
  const line = leadLine(chat.data?.last);
  const counts = countSt(all.map((r) => r.st));
  const busy = counts.working + counts.review + counts.needs + counts.error;
  const { rows, folded } = foldDone(shown, filters, unfolded);
  const name = projectLabel(project);
  const active = view.kind === 'project' && view.project === project.name;
  return (
    <div className="mb-0.5" data-v2-project={project.name}>
      <Lead active={active} onClick={() => onSelect({ kind: 'project', project: project.name })} data={{ 'data-v2-lead': project.name }}>
        <button
          type="button"
          aria-label={t(open ? 'v2.sidebar.hideAgents' : 'v2.sidebar.showAgents', { project: name })}
          aria-expanded={open}
          onClick={(e) => {
            e.stopPropagation();
            onToggle();
          }}
          className="mt-1 flex size-4 shrink-0 items-center justify-center rounded text-faint hover:bg-surface-strong hover:text-title"
          data-v2-toggle={project.name}
        >
          <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
        </button>
        <span className="mt-px flex size-6 shrink-0 items-center justify-center rounded-md bg-surface-strong text-[11px] font-semibold uppercase text-secondary" aria-hidden>
          {name.slice(0, 1)}
        </span>
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
          {!open && all.length > 0 && (
            <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10.5px] tabular-nums" data-v2-counts>
              {countKeys.map(({ st, key }) =>
                counts[st] ? (
                  <span key={st} className={cn('inline-flex items-center gap-1', st === 'needs' ? 'text-amber-300' : st === 'error' ? 'text-rose-300' : 'text-subtle')} data-v2-count={st}>
                    <StMark st={st} />
                    {t(key, { count: counts[st] })}
                  </span>
                ) : null,
              )}
              {busy === 0 && <span className="text-faint">{t('v2.count.allDone', { count: all.length })}</span>}
            </span>
          )}
        </span>
        <Tip label={t('shell.sidebar.newAgentIn', { project: name })}>
          <button
            type="button"
            aria-label={t('shell.sidebar.newAgentIn', { project: name })}
            className="mt-0.5 rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover:opacity-100"
            onClick={(e) => {
              e.stopPropagation();
              onNewAgent(project.name);
            }}
          >
            <Plus className="size-3.5" />
          </button>
        </Tip>
      </Lead>
      {open && (
        <div className="relative ml-[17px] border-l border-line-faint pl-1.5" data-v2-agents={project.name}>
          {rows.map((r) => (
            <AgentRow key={r.agent.ref} row={r} active={view.kind === 'agent' && view.ref === r.agent.ref} onClick={() => onSelect({ kind: 'agent', ref: r.agent.ref })} />
          ))}
          {folded > 0 && (
            <button type="button" onClick={onUnfold} className="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-[11.5px] text-subtle hover:bg-surface hover:text-secondary" data-v2-unfold>
              <Check className="size-3 shrink-0 text-faint" />
              {t('v2.sidebar.done', { count: folded })}
            </button>
          )}
          {shown.length === 0 && <div className="px-2 py-1 text-[11.5px] text-faint">{t('v2.sidebar.noAgents')}</div>}
        </div>
      )}
    </div>
  );
}

// AgentRow is one agent under its lead: its state mark, its title in
// neutral text, a word only when it needs you, and when it last changed.
function AgentRow({ row, active, onClick }: { row: Row; active: boolean; onClick: () => void }) {
  const t = useT();
  const { agent, st } = row;
  return (
    <button
      type="button"
      onClick={onClick}
      title={t(stLabel[st])}
      className={cn('flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left', active ? 'bg-surface-raised' : 'hover:bg-surface')}
      data-v2-agent={agent.ref}
      data-v2-agent-st={st}
    >
      <StMark st={st} className="size-[7px]" />
      <span className={cn('min-w-0 flex-1 truncate text-[12px]', active ? 'text-title' : st === 'done' ? 'text-subtle' : 'text-secondary', !agent.title && 'font-mono text-[11.5px]')}>{agent.title || agent.name}</span>
      {attention(st) && <span className="shrink-0 text-[10.5px] text-subtle">{t(st === 'needs' ? 'v2.tag.asks' : 'v2.tag.failed')}</span>}
      {agent.changesRequested && st === 'working' && <span className="shrink-0 text-[10.5px] text-subtle">{t('v2.tag.changes')}</span>}
    </button>
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
