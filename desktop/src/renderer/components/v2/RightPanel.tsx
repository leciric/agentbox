// UI v2's right-hand panel: closed by default, opened from one button in the
// chat's header (PanelButton) or ⌘D, it gathers what an agent has besides
// its chat: Files (#306), its Desktop, Terminal and Browser, the Hatch pages
// it published and its Media, in a rail of icons on its outer edge. The
// picker in its header chooses whose they are, for every tab: the chat in
// the middle's by default, the project's repository (the lead's), or any of
// its agents, past ones too. A tab that needs a machine where there is none
// lists the agents that have one, to pick from. It holds the chat's ⋯ menu
// too (usePanelMenu). lib/rightPanel.ts has its rules and state; the mock
// was concept E's &rp=2 (dev/concepts/rightPanel.tsx on
// agentbox/mock-right-panel).
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronDown, Crown, Ellipsis, FolderTree, GitBranch, GitPullRequest, Globe, SlidersHorizontal, HardDrive, Images, Monitor, Moon, Play, Search, SquareTerminal, Unplug, X, type LucideIcon } from 'lucide-react';
import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as A from '../../../shared/api';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { onShowFiles } from '../../lib/files';
import { useT, type MessageKey } from '../../lib/i18n';
import { isNew, livePages } from '../../lib/pages';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import {
  hasTab,
  machineOf,
  needsMachine,
  projectWide,
  openPanelOn,
  panelBox,
  panelDefault,
  panelMin,
  panelState,
  panelWant,
  panelTabs,
  pickable,
  pickedFor,
  resolveWhose,
  setPanel,
  usePanel,
  usePanelMenuItems,
  whoseRef,
  type Machine,
  type PanelState,
  type PanelTab,
  type Whose,
} from '../../lib/rightPanel';
import { cn, errorMessage, shortcut } from '../../lib/utils';
import { BrowserTab } from '../BrowserTab';
import { FileBrowser } from '../files/FilePanel';
import { MediaTab } from '../MediaTab';
import { PageIcon } from '../pages/PageThumb';
import { PagesPanel } from '../pages/PagesPanel';
import { useProjectPages } from '../pages/usePages';
import { ProjectIcon } from '../ProjectIcon';
import { ProjectMediaPanel } from '../ProjectMediaPanel';
import { AIIcon, LiveAgentAvatar } from '../state';
import { TerminalTab } from '../TerminalTab';
import { ProjectSettingsBody } from '../ProjectView';
import { PullRequestsPanel } from '../PullRequestsPanel';
import { Button } from '../ui/button';
import { Menu, MenuContent, MenuTrigger } from '../ui/menu';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';
import { PanelBrowser } from './PanelBrowser';
import { chatOf } from './PanelButton';

const tabIcon: Record<PanelTab, LucideIcon | typeof PageIcon> = {
  files: FolderTree,
  desktop: Monitor,
  terminal: SquareTerminal,
  browser: Globe,
  pages: PageIcon,
  media: Images,
  pulls: GitPullRequest,
  settings: SlidersHorizontal,
};
const tabLabel: Record<PanelTab, MessageKey> = {
  files: 'panel.tab.files',
  desktop: 'panel.tab.desktop',
  terminal: 'panel.tab.terminal',
  browser: 'panel.tab.browser',
  pages: 'panel.tab.pages',
  media: 'panel.tab.media',
  pulls: 'panel.tab.pulls',
  settings: 'panel.tab.settings',
};

const railWidth = 44;

// RightPanelHost sits beside the main column of App, for a view with a chat:
// it lays the panel out beside the chat or as a drawer, and listens for ⌘D,
// Escape and a file a chat opens.
export function RightPanelHost({ view, onSelect }: { view: View; onSelect: (view: View) => void }) {
  const t = useT();
  const at = chatOf(view);
  const state = usePanel();
  const probe = useRef<HTMLSpanElement>(null);
  const [room, setRoom] = useState(0);
  const [windowWidth, setWindowWidth] = useState(() => window.innerWidth);
  useLayoutEffect(() => {
    const row = probe.current?.parentElement;
    if (!row) return;
    const measure = () => {
      setRoom(row.clientWidth);
      setWindowWidth(window.innerWidth);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(row);
    measure();
    return () => observer.disconnect();
  }, []);

  const chat = at?.chat ?? '';
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!chat) return;
      // A terminal's Ctrl+D is its end of input, and a desktop you control
      // gets its keys: neither toggles the panel.
      if ((event.target as Element | null)?.closest?.('.xterm, canvas')) return;
      if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'd') {
        event.preventDefault();
        setPanel({ open: !panelState().open });
      }
    };
    addEventListener('keydown', onKey);
    const off = onShowFiles((ref) => {
      if (chat) openPanelOn(chat, 'files', ref);
    });
    return () => {
      removeEventListener('keydown', onKey);
      off();
    };
  }, [chat]);

  if (!at) return <span ref={probe} hidden />;
  const box = panelBox(panelWant(state), room, windowWidth);
  return (
    <>
      <span ref={probe} hidden />
      {state.open && room > 0 && (
        <>
          {box.drawer && <div className="fixed inset-0 z-40 animate-fade-in bg-scrim backdrop-blur-sm" onClick={() => setPanel({ open: false })} data-panel-scrim />}
          <aside
            style={{ width: box.width }}
            aria-label={t('panel.label')}
            className={cn('relative flex h-full shrink-0 border-l border-line bg-sunken', box.drawer && 'fixed inset-y-0 right-0 z-50 animate-fade-in bg-modal shadow-[-20px_0_60px_-20px_var(--ab-shadow-deep)]')}
            onKeyDown={(event) => {
              if (event.key === 'Escape' && box.drawer) setPanel({ open: false });
            }}
            data-right-panel={box.drawer ? 'drawer' : 'beside'}
          >
            {!box.drawer && <WidthHandle width={box.width} />}
            <RightPanel project={at.project} chat={at.chat} width={box.width} onSelect={onSelect} />
          </aside>
        </>
      )}
    </>
  );
}

// WidthHandle drags the panel's left edge; a double click puts it back.
function WidthHandle({ width }: { width: number }) {
  const last = useRef<number | null>(null);
  const w = useRef(width);
  w.current = width;
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      className="absolute inset-y-0 left-0 z-10 w-1.5 cursor-col-resize transition-colors hover:bg-brand-400/30 active:bg-brand-400/40"
      onPointerDown={(event) => {
        event.preventDefault();
        event.currentTarget.setPointerCapture(event.pointerId);
        last.current = event.clientX;
      }}
      onPointerMove={(event) => {
        if (last.current === null) return;
        const dx = event.clientX - last.current;
        if (!dx) return;
        last.current = event.clientX;
        setPanel({ width: Math.max(panelMin, w.current - dx) });
      }}
      onPointerUp={() => (last.current = null)}
      onPointerCancel={() => (last.current = null)}
      onDoubleClick={() => setPanel({ width: panelDefault })}
      data-panel-splitter
    />
  );
}

// The project's agents and past agents, and whose the panel shows.
function useWhose(project: string, chat: string) {
  const state = usePanel();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const mine = useMemo(() => (agents.data ?? []).filter((a) => a.project === project), [agents.data, project]);
  const gone = past.data ?? [];
  const ref = pickedFor(state, chat);
  const whose = resolveWhose(ref, project, mine, gone) ?? resolveWhose(chat, project, mine, gone) ?? { kind: 'lead', ref: `${project}/${A.LeadName}` };
  return { state, whose, agents: mine, past: gone, loading: agents.isPending };
}

function RightPanel({ project, chat, width, onSelect }: { project: string; chat: string; width: number; onSelect: (view: View) => void }) {
  const t = useT();
  const { state, whose, agents, past } = useWhose(project, chat);
  const pick = (w: Whose) => setPanel({ picked: whoseRef(w) === chat ? null : { for: chat, ref: whoseRef(w) } });
  const Icon = tabIcon[state.tab];
  const pages = useProjectPages(project);
  const fresh = livePages(pages.data).filter((p) => isNew(p) && (whose.kind === 'lead' || p.agents.includes(whoseRef(whose).replace(/^past:/, '')))).length;
  const menu = usePanelMenuItems<ReactNode>();

  return (
    <div className="flex min-w-0 flex-1" data-panel-tab={state.tab} data-panel-whose={whoseRef(whose)}>
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center gap-1.5 border-b border-line-faint pl-3 pr-1.5">
          <span className="flex shrink-0 items-center gap-1.5 text-[12.5px] font-semibold text-title">
            <Icon className="size-4 text-subtle" />
            {t(tabLabel[state.tab])}
          </span>
          <span className="text-faint">·</span>
          {projectWide(state.tab) ? (
            <ProjectName project={project} />
          ) : (
            <Picker project={project} whose={whose} agents={agents} past={past} onPick={pick} />
          )}
          <span className="ml-auto" />
          {menu && (
            <Menu>
              <MenuTrigger asChild>
                <Button size="icon-sm" variant="ghost" aria-label={t('panel.more')} data-panel-menu>
                  <Ellipsis />
                </Button>
              </MenuTrigger>
              <MenuContent>{menu}</MenuContent>
            </Menu>
          )}
        </div>
        {projectWide(state.tab) ? (
          <ProjectTab project={project} tab={state.tab} section={state.section} onSelect={onSelect} />
        ) : (
          <Body project={project} whose={whose} tab={state.tab} agents={agents} past={past} width={width - railWidth} onPick={pick} />
        )}
      </div>
      <nav className="flex shrink-0 flex-col items-center gap-1 border-l border-line bg-rail py-1.5" style={{ width: railWidth }} aria-label={t('panel.tabs')}>
        <Tip label={t('panel.closeTip', { keys: shortcut('D') })} side="left">
          <button type="button" aria-label={t('panel.close')} onClick={() => setPanel({ open: false })} className="flex size-8 items-center justify-center rounded-md text-subtle hover:bg-surface-raised hover:text-primary" data-panel-close>
            <X className="size-4" />
          </button>
        </Tip>
        <span className="my-1 h-px w-6 bg-line" />
        {panelTabs.map((tab) => {
          const TabIcon = tabIcon[tab];
          const on = state.tab === tab;
          const ok = hasTab(whose, tab);
          return (
            <Fragment key={tab}>
            {tab === 'pulls' && <span className="my-1 h-px w-6 bg-line" />}
            <Tip label={ok ? t(tabLabel[tab]) : t('panel.needsMachineTip', { tab: t(tabLabel[tab]) })} side="left">
              <button
                type="button"
                aria-label={t(tabLabel[tab])}
                aria-current={on ? 'page' : undefined}
                onClick={() => setPanel({ tab })}
                className={cn('relative flex size-8 items-center justify-center rounded-md', on ? 'bg-surface-raised text-title' : 'text-subtle hover:bg-surface hover:text-title', !ok && !on && 'opacity-50')}
                data-panel-rail-tab={tab}
              >
                {on && <span className="absolute -right-1.5 top-1.5 h-5 w-0.5 rounded-full bg-brand-500" />}
                <TabIcon className="size-4" />
                {tab === 'pages' && fresh > 0 && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-brand-500" />}
              </button>
            </Tip>
            </Fragment>
          );
        })}
      </nav>
    </div>
  );
}

// ——— Whose: the picker ———

function Picker({ project, whose, agents, past, onPick }: { project: string; whose: Whose; agents: T.Agent[]; past: T.ArchivedAgent[]; onPick: (w: Whose) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className={cn('flex min-w-0 items-center gap-1.5 rounded-md px-1.5 py-1 text-left hover:bg-surface', open && 'bg-surface')} aria-label={t('panel.pickerLabel')} data-panel-picker>
          <WhoseIcon project={project} whose={whose} className="size-5" />
          <span className="min-w-0 truncate text-[12.5px] font-semibold text-title">{whoseName(t, whose)}</span>
          <WhoseMark whose={whose} />
          <ChevronDown className="size-3.5 shrink-0 text-subtle" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(360px,calc(100vw-32px))] p-0" data-panel-picker-list>
        <WhoseList
          project={project}
          whose={whose}
          agents={agents}
          past={past}
          withLead
          onPick={(w) => {
            setOpen(false);
            onPick(w);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

// WhoseList is the agents to pick from, with a search and the "With a
// machine" filter: the picker's, and what a tab that needs a machine shows
// when there is none (forTab), where only an agent with one can be picked.
function WhoseList({ project, whose, agents, past, withLead, forTab, onPick }: { project: string; whose?: Whose; agents: T.Agent[]; past: T.ArchivedAgent[]; withLead?: boolean; forTab?: PanelTab; onPick: (w: Whose) => void }) {
  const t = useT();
  const { withMachine } = usePanel();
  const [query, setQuery] = useState('');
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const all = pickable(project, agents, past, false);
  const shown = pickable(project, agents, past, withMachine).filter((w) => matches(w, query));
  const current = whose && whoseRef(whose);
  const projectName = projectLabel(project, projects.data);
  return (
    <div className="flex min-h-0 flex-col" data-whose-list>
      <div className="flex items-center gap-2 border-b border-line-faint px-3 py-2">
        <Search className="size-3.5 shrink-0 text-faint" />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t('panel.find')}
          aria-label={t('panel.find')}
          className="min-w-0 flex-1 bg-transparent text-[12.5px] text-primary outline-none placeholder:text-faint"
          data-whose-find
        />
      </div>
      <div className="flex items-center justify-between gap-2 border-b border-line-faint px-3 py-2">
        <span className="text-[11.5px] text-subtle">{withMachine ? t('panel.countOf', { shown: shown.length, count: all.length }) : t('panel.count', { count: all.length })}</span>
        <button
          type="button"
          aria-pressed={withMachine}
          onClick={() => setPanel({ withMachine: !withMachine })}
          className={cn('flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px]', withMachine ? 'border-brand-500/60 bg-brand-500/15 text-title' : 'border-line text-subtle hover:text-title')}
          title={t('panel.withMachineTip')}
          data-whose-machine-filter
        >
          <HardDrive className="size-3" />
          {t('panel.withMachine')}
          {withMachine && <X className="size-3" />}
        </button>
      </div>
      <div className={cn('min-h-0 overflow-y-auto p-1', withLead ? 'max-h-[min(420px,calc(var(--radix-popover-content-available-height,100vh)-96px))]' : 'max-h-[420px]')}>
        {withLead && matchesText(t('panel.repository'), query) && (
          <>
            <Row
              active={whose?.kind === 'lead'}
              onClick={() => onPick({ kind: 'lead', ref: `${project}/${A.LeadName}` })}
              icon={<WhoseIcon project={project} whose={{ kind: 'lead', ref: `${project}/${A.LeadName}` }} className="size-7 text-[11px]" />}
              title={
                <>
                  {t('panel.repository')} <Crown className="size-3 text-amber-300" />
                </>
              }
              sub={t('panel.repositorySub', { project: projectName })}
              right={<span className="text-[10.5px] text-faint">{t('panel.host')}</span>}
              data-whose-row="lead"
            />
            <div className="px-2 pb-1 pt-2 text-[10.5px] font-semibold uppercase tracking-wide text-faint">{t('panel.agents')}</div>
          </>
        )}
        {shown.map((w) => {
          const ref = whoseRef(w);
          const disabled = !!forTab && !hasTab(w, forTab);
          return (
            <Row
              key={ref}
              active={ref === current}
              disabled={disabled}
              onClick={() => onPick(w)}
              icon={<WhoseIcon project={project} whose={w} className="size-7" />}
              title={
                <>
                  {w.kind === 'agent' ? w.agent.name : w.past.name} <WhoseMark whose={w} />
                </>
              }
              sub={(w.kind === 'agent' ? w.agent.title : w.past.title) || (w.kind === 'agent' ? w.agent.branch : w.past.branch)}
              right={<span className="text-[10.5px] text-faint">{t(machineLabel[machineOfWhose(w)])}</span>}
              data-whose-row={ref}
            />
          );
        })}
        {shown.length === 0 && <p className="px-3 py-4 text-center text-[12px] text-subtle">{query ? t('panel.noMatch') : withMachine ? t('panel.noMachines') : t('panel.noAgents')}</p>}
        {withMachine && all.length > shown.length && !query && <p className="px-2 pb-1 pt-2 text-[11px] text-faint">{t('panel.hidden', { count: all.length - pickable(project, agents, past, true).length })}</p>}
      </div>
    </div>
  );
}

function Row({ active, disabled, onClick, icon, title, sub, right, ...rest }: { active: boolean; disabled?: boolean; onClick: () => void; icon: ReactNode; title: ReactNode; sub: string; right: ReactNode; 'data-whose-row': string }) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn('flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left', active ? 'bg-surface-raised' : 'hover:bg-surface', disabled && 'cursor-not-allowed opacity-45 hover:bg-transparent')}
      aria-current={active || undefined}
      {...rest}
    >
      {icon}
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-[12.5px] font-medium text-title">{title}</span>
        <span className="block truncate text-[11px] text-subtle">{sub}</span>
      </span>
      <span className="flex shrink-0 items-center gap-1.5">
        {right}
        {active && <Check className="size-3.5 text-brand-300" />}
      </span>
    </button>
  );
}

const machineLabel: Record<Machine | 'host', MessageKey> = {
  running: 'panel.machine.running',
  asleep: 'panel.machine.asleep',
  freed: 'panel.machine.freed',
  host: 'panel.host',
};
const machineOfWhose = (w: Whose): Machine | 'host' => (w.kind === 'lead' ? 'host' : w.kind === 'past' ? 'freed' : machineOf(w.agent));

function matches(w: Whose, query: string): boolean {
  if (w.kind === 'lead') return true;
  const a = w.kind === 'agent' ? w.agent : w.past;
  return matchesText(`${a.name} ${a.title} ${a.branch}`, query);
}
const matchesText = (text: string, query: string) => !query.trim() || query.toLowerCase().split(/\s+/).every((word) => text.toLowerCase().includes(word));

function whoseName(t: ReturnType<typeof useT>, w: Whose): string {
  return w.kind === 'lead' ? t('panel.repository') : w.kind === 'agent' ? w.agent.name : w.past.name;
}

function WhoseIcon({ project, whose, className }: { project: string; whose: Whose; className?: string }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  if (whose.kind === 'lead') return p ? <ProjectIcon project={p} className={cn('rounded-md text-[10px]', className)} /> : <span className={cn('shrink-0 rounded-md bg-surface-strong', className)} />;
  if (whose.kind === 'agent') return <LiveAgentAvatar agent={whose.agent} className={className} />;
  return (
    <span className={cn('flex shrink-0 items-center justify-center rounded-full border border-line bg-surface text-subtle opacity-70', className)}>
      <AIIcon ai={whose.past.ai} className="size-3" />
    </span>
  );
}

function WhoseMark({ whose }: { whose: Whose }) {
  const t = useT();
  const m = machineOfWhose(whose);
  if (m === 'host') return <Crown className="size-3 shrink-0 text-amber-300" aria-label={t('panel.repository')} />;
  if (m === 'running') return <span title={t('panel.machine.running')} className="block size-1.5 shrink-0 rounded-full bg-emerald-400" />;
  if (m === 'asleep') return <Moon className="size-3 shrink-0 text-subtle" aria-label={t('panel.machine.asleep')} />;
  return <Unplug className="size-3 shrink-0 text-faint" aria-label={t('panel.machine.freed')} />;
}

// ——— The tabs ———

function Body({ project, whose, tab, agents, past, width, onPick }: { project: string; whose: Whose; tab: PanelTab; agents: T.Agent[]; past: T.ArchivedAgent[]; width: number; onPick: (w: Whose) => void }) {
  if (!hasTab(whose, tab)) return <ChooseMachine project={project} whose={whose} tab={tab} agents={agents} past={past} onPick={onPick} />;
  if (whose.kind === 'agent' && needsMachine(tab) && machineOf(whose.agent) === 'asleep') return <Asleep agent={whose.agent} tab={tab} />;
  return (
    <div className="flex min-h-0 flex-1 flex-col" key={`${whoseRef(whose)}:${tab}`}>
      {tab === 'files' && <Files project={project} whose={whose} width={width} />}
      {tab === 'desktop' && whose.kind === 'agent' && <BrowserTab agent={whose.agent} onOpenMedia={() => setPanel({ tab: 'media' })} />}
      {tab === 'terminal' && whose.kind === 'agent' && <AgentTerminal agent={whose.agent} />}
      {tab === 'browser' && whose.kind === 'agent' && <PanelBrowser agent={whose.agent} />}
      {tab === 'pages' && <Pages project={project} whose={whose} agents={agents} />}
      {tab === 'media' && <Media project={project} whose={whose} />}
    </div>
  );
}

// ChooseMachine is a tab that needs a machine, for whose has none: the lead,
// on this computer, or a past agent, whose machine was freed. It lists the
// agents that have one.
function ChooseMachine({ project, whose, tab, agents, past, onPick }: { project: string; whose: Whose; tab: PanelTab; agents: T.Agent[]; past: T.ArchivedAgent[]; onPick: (w: Whose) => void }) {
  const t = useT();
  const Icon = tabIcon[tab];
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto" data-panel-choose={tab}>
      <div className="flex flex-col items-center gap-2 px-6 pb-3 pt-8 text-center">
        <Icon className="size-7 text-faint" />
        <p className="max-w-[320px] text-[12.5px] text-subtle">
          {t(whose.kind === 'lead' ? 'panel.choose.lead' : 'panel.choose.freed', { tab: t(tabLabel[tab]).toLowerCase(), name: whose.kind === 'past' ? whose.past.name : '' })}
        </p>
        <p className="text-[12px] font-medium text-secondary">{t('panel.choose.pick')}</p>
      </div>
      <div className="mx-3 mb-3 overflow-hidden rounded-xl border border-line bg-surface-faint">
        <WhoseList project={project} agents={agents} past={past} forTab={tab} onPick={onPick} />
      </div>
    </div>
  );
}

// useWake starts a stopped agent or resumes a paused one.
function useWake(agent: T.Agent) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.agentAction(agent.ref, agent.state === 'paused' ? 'resume' : 'start'),
    onSuccess: (updated) => queryClient.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === updated.ref ? updated : a))),
    onError: (err) => toast.error(errorMessage(err)),
  });
}

function Asleep({ agent, tab }: { agent: T.Agent; tab: PanelTab }) {
  const t = useT();
  const wake = useWake(agent);
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center" data-panel-asleep={agent.ref}>
      <Moon className="size-8 text-faint" />
      <p className="max-w-[300px] text-[12.5px] text-subtle">{t('panel.asleep', { name: agent.name, tab: t(tabLabel[tab]).toLowerCase() })}</p>
      <Button size="sm" variant="primary" disabled={wake.isPending} onClick={() => wake.mutate()} data-panel-wake>
        <Play />
        {t('panel.wake', { name: agent.name })}
      </Button>
    </div>
  );
}

function AgentTerminal({ agent }: { agent: T.Agent }) {
  const wake = useWake(agent);
  return <TerminalTab agent={agent} starting={wake.isPending} onStart={() => wake.mutate()} />;
}

function Files({ project, whose, width }: { project: string; whose: Whose; width: number }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: whose.kind === 'lead' });
  const lead = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project), enabled: whose.kind === 'lead' });
  if (whose.kind === 'past') {
    return (
      <>
        <div className="flex h-7 shrink-0 items-center gap-1.5 border-b border-line-faint bg-surface-faint px-3 text-[11.5px] text-subtle" data-panel-readonly>
          <GitBranch className="size-3 shrink-0" />
          <span className="min-w-0 truncate">{t('panel.files.readOnly', { branch: whose.past.branch })}</span>
        </div>
        <FileBrowser fileRef={whoseRef(whose)} root="" width={width} split />
      </>
    );
  }
  const root = whose.kind === 'agent' ? whose.agent.worktree : lead.data?.worktree || projects.data?.find((p) => p.name === project)?.root || '';
  return <FileBrowser fileRef={whoseRef(whose)} root={root} width={width} split />;
}

function Pages({ project, whose, agents }: { project: string; whose: Whose; agents: T.Agent[] }) {
  const t = useT();
  const query = useProjectPages(project);
  const own = whose.kind === 'lead' ? '' : whoseRef(whose).replace(/^past:/, '');
  const [agent, setAgent] = useState(own);
  useEffect(() => setAgent(own), [own]);
  const pages = livePages(query.data);
  if (!query.data?.connector) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center text-[12.5px] text-subtle" data-panel-pages-off>
        <PageIcon className="size-7 text-faint" />
        {query.isPending ? t('common.loading') : t('panel.pages.off')}
      </div>
    );
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col pt-2">
      <PagesPanel project={project} pages={pages} agents={agents} agent={agent} onAgent={setAgent} />
    </div>
  );
}

function Media({ project, whose }: { project: string; whose: Whose }) {
  if (whose.kind === 'agent') return <MediaTab agent={whose.agent} />;
  if (whose.kind === 'past') return <MediaTab agent={pastAsAgent(whose.past)} />;
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
      <ProjectMediaPanel project={project} />
    </div>
  );
}

// pastAsAgent is a past agent in the shape its Media reads: no machine.
function pastAsAgent(p: T.ArchivedAgent): T.Agent {
  return {
    ref: p.ref,
    project: p.project,
    name: p.name,
    title: p.title,
    instance: '',
    ai: p.ai,
    autonomous: false,
    branch: p.branch,
    baseRef: p.baseRef,
    baseCommit: p.baseCommit,
    worktree: '',
    source: '',
    claudeAccount: '',
    githubAccount: '',
    interface: p.interface,
    state: 'archived',
    ip: '',
    createdAt: p.createdAt,
  };
}

// ——— The project's own tabs ———

function ProjectName({ project }: { project: string }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  return (
    <span className="flex min-w-0 items-center gap-1.5 px-1.5" data-panel-project>
      <WhoseIcon project={project} whose={{ kind: 'lead', ref: `${project}/${A.LeadName}` }} className="size-5" />
      <span className="min-w-0 truncate text-[12.5px] font-semibold text-title">{projectLabel(project, projects.data)}</span>
    </span>
  );
}

// ProjectTab is the project's pull requests or its settings, what the
// lead's page had as tabs over its chat.
function ProjectTab({ project, tab, section, onSelect }: { project: string; tab: PanelTab; section: PanelState['section']; onSelect: (view: View) => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  if (tab === 'pulls') {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" data-panel-pulls>
        <PullRequestsPanel project={project} onSelect={onSelect} onOpenAccount={() => setPanel({ tab: 'settings', section: 'general' })} />
      </div>
    );
  }
  if (!p) return null;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-panel-settings={section}>
      <ProjectSettingsBody project={p} section={section} onSection={(next) => setPanel({ section: next })} onSelect={onSelect} onOpenMedia={() => setPanel({ tab: 'media', picked: null })} />
    </div>
  );
}
