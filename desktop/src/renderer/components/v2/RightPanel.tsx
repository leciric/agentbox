// UI v2's right-hand panel, always beside a chat, as v1's rail was. Its home
// is the project's agents (AgentList), which fold to a strip of their
// avatars (») and back from the chat header's button (PanelButton) or ⌘D;
// it never closes. Where the list doesn't fit beside the chat, the strip
// stays there and the rest opens over the chat as a drawer.
// Beside the lead's chat its top is a switch of five tabs, Agents (the list),
// Pull requests (the project's), Files (the repository's tree, the lead's
// checkout), Evidence and Schedules, the middle ones kept per project
// (LeadSwitch). An agent, or the project's Settings (opened from the panel's
// header), show in it with ✕ back to where it was; for an agent, the header
// is, left to right: a back arrow, whose it is (a click opens its chat), and
// a switch in LeadSwitch's look of every view it has (ViewSwitch: Desktop,
// Terminal, Browser, Android for an Android project, Files, Evidence,
// Timeline, Schedules), icons only where the lit one's name doesn't fit.
// Opening an agent's chat (a row of the list does)
// shows that agent's Desktop, and its back arrow goes back to the lead's
// chat; the lead's panel keeps what it showed, its list unless something
// was picked there. Dragged at its left edge, the list and an agent each
// keep their own width. lib/rightPanel.ts has its rules and state.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlarmClock, ArrowLeft, ChevronsRight, Camera, Briefcase, Crown, Film, FileText, FolderTree, CircleDot, GanttChart, GitBranch, GitPullRequest, Globe, Image as ImageIcon, Images, LoaderCircle, Maximize, Minimize, Monitor, MousePointer2, Play, Plus, Power, Settings, SlidersHorizontal, Smartphone, SquareTerminal, Unplug, Users, X, type LucideIcon } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api, browserViewPath } from '../../lib/api';
import { onShowFiles } from '../../lib/files';
import { useT, type MessageKey } from '../../lib/i18n';
import { clock, kindInfo, mediaUrl } from '../../lib/media';
import { loadedItems, useMediaPages } from '../../lib/mediaPages';
import { useSeeMedia } from '../../lib/notifications';
import { isLeadRef, livePages, openPage } from '../../lib/pages';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import {
  chaptersOf,
  chatPanel,
  defaultView,
  hasAndroid,
  isPastRef,
  leadOf,
  leadTab,
  leadTabs,
  listTarget,
  listBox,
  listMin,
  listStrip,
  machineOf,
  needsMachine,
  openPanelOn,
  panelBack,
  panelClose,
  panelToggle,
  onList,
  panelBox,
  panelDefault,
  panelMin,
  panelTarget,
  panelWant,
  projectWide,
  resolveWhose,
  setFitsBeside,
  setPanel,
  setLeadTab,
  setPanelOpen,
  setView,
  unPast,
  usePanel,
  viewOf,
  viewsOf,
  whoseRef,
  type PanelState,
  type LeadTab,
  type PanelView,
  type WideView,
  type Whose,
} from '../../lib/rightPanel';
import { useFullscreen } from '../../lib/useFullscreen';
import { useVncView } from '../../lib/useVncView';
import { cn, errorMessage, timeAgo } from '../../lib/utils';
import { AndroidTab } from '../AndroidTab';
import { ConfirmDialog } from '../ConfirmDialog';
import { ErrorBoundary } from '../ErrorBoundary';
import { FileBrowser } from '../files/FilePanel';
import { MediaViewer, MoreMedia } from '../MediaTab';
import { PageThumb } from '../pages/PageThumb';
import { useProjectPages } from '../pages/usePages';
import { standIn } from '../pip/standIn';
import { SchedulesPanel } from '../SchedulesPanel';
import { ProjectSettingsBody } from '../ProjectView';
import { PullRequestsPanel } from '../PullRequestsPanel';
import { TerminalTab } from '../TerminalTab';
import { TimelinePanel, useTimeline } from '../timeline/Timeline';
import { ShareButton } from '../ShareButton';
import { Button } from '../ui/button';
import { Switch } from '../ui/switch';
import { Tip } from '../ui/tooltip';
import { PanelBrowser } from './PanelBrowser';
import { AgentList, AgentStrip } from './AgentList';
import { chatOf } from './PanelButton';
import { useOpenReview, useProjectReview } from './ReviewV2';
import { PickButton, usePicker } from '../PickElement';
import { RichBeside } from '../chat/RichBlock';
import { titleOf } from '../../lib/rich';
import { IssuesPanel } from '../IssuesPanel';

const viewIcon: Record<PanelView, LucideIcon> = {
  desktop: Monitor,
  terminal: SquareTerminal,
  files: FolderTree,
  evidence: Images,
  browser: Globe,
  android: Smartphone,
  timeline: GanttChart,
  pulls: GitPullRequest,
  issues: CircleDot,
  schedules: AlarmClock,
  settings: SlidersHorizontal,
};
const viewLabel: Record<PanelView, MessageKey> = {
  desktop: 'panel.view.desktop',
  terminal: 'panel.view.terminal',
  files: 'panel.view.files',
  evidence: 'panel.view.evidence',
  browser: 'panel.view.browser',
  android: 'panel.view.android',
  timeline: 'panel.view.timeline',
  pulls: 'panel.view.pulls',
  issues: 'panel.view.issues',
  schedules: 'panel.view.schedules',
  settings: 'panel.view.settings',
};

// RightPanelHost sits beside the main column of App, for a view with a chat:
// it lays the chat's panel out beside the chat or as a drawer, and listens
// for ⌘D, Escape and a file a chat opens.
export function RightPanelHost({ view, onSelect, onNewAgent }: { view: View; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
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
        panelToggle(chat);
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

  // On the list, its own narrow width, or its strip. Where even the list
  // doesn't fit beside the chat, the strip stays beside it and the rest
  // opens over the chat as a drawer, from the strip or the header's button.
  const list = onList(state, chat);
  const lead = isLeadRef(chat);
  const box = list ? listBox(state, room, windowWidth) : panelBox(panelWant(state, chat, room), room, windowWidth);
  const narrow = box.drawer || (list && state.listFolded && listBox({ ...state, listFolded: false }, room, windowWidth).drawer);
  const drawerWidth = list ? listBox({ ...state, listFolded: false }, room, windowWidth).width : box.width;
  const measured = room > 0;
  useEffect(() => {
    if (measured) setFitsBeside(!narrow);
  }, [narrow, measured]);

  if (!at) return <span ref={probe} hidden />;
  const panel = (drawer: boolean, width: number) => <RightPanel project={at.project} chat={at.chat} width={width} drawer={drawer} onSelect={onSelect} onNewAgent={onNewAgent} />;
  return (
    <>
      <span ref={probe} hidden />
      {measured && (
        <aside
          style={{ width: narrow ? listStrip : box.width }}
          aria-label={t('panel.label')}
          className="relative flex h-full shrink-0 border-l border-line bg-sunken"
          data-right-panel={narrow ? 'strip' : 'beside'}
        >
          {!narrow && !(list && state.listFolded) && <WidthHandle width={box.width} which={list ? 'list' : lead ? 'lead' : 'agent'} />}
          {narrow ? <AgentStrip project={at.project} chat={at.chat} onSelect={onSelect} onUnfold={() => setPanelOpen(chat, true)} /> : panel(false, box.width)}
        </aside>
      )}
      {measured && narrow && chatPanel(state, chat).open && (
        <>
          <div className="fixed inset-0 z-40 animate-fade-in bg-scrim backdrop-blur-sm" onClick={() => setPanelOpen(chat, false)} data-panel-scrim />
          <aside
            style={{ width: drawerWidth }}
            aria-label={t('panel.label')}
            className="fixed inset-y-0 right-0 z-50 flex h-full animate-fade-in border-l border-line bg-modal shadow-[-20px_0_60px_-20px_var(--ab-shadow-deep)]"
            onKeyDown={(event) => {
              if (event.key === 'Escape') setPanelOpen(chat, false);
            }}
            data-right-panel="drawer"
          >
            {panel(true, drawerWidth)}
          </aside>
        </>
      )}
    </>
  );
}

// WidthHandle drags the panel's left edge, the list's, a lead's or an
// agent's width; a double click puts it back.
function WidthHandle({ width, which }: { width: number; which: 'list' | 'lead' | 'agent' }) {
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
        if (which === 'list') return setPanel({ listWidth: Math.max(listMin, w.current - dx) });
        const next = Math.max(panelMin, w.current - dx);
        setPanel(which === 'lead' ? { width: next } : { agentWidth: next });
      }}
      onPointerUp={() => (last.current = null)}
      onPointerCancel={() => (last.current = null)}
      onDoubleClick={() => setPanel(which === 'list' ? { listWidth: undefined } : which === 'lead' ? { width: panelDefault } : { agentWidth: undefined })}
      data-panel-splitter
    />
  );
}

// The project's agents and past agents: whose the panel shows.
function useAgents(project: string) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const mine = useMemo(() => (agents.data ?? []).filter((a) => a.project === project), [agents.data, project]);
  return { agents: mine, past: past.data ?? [] };
}

function RightPanel({ project, chat, width, drawer, onSelect, onNewAgent }: { project: string; chat: string; width: number; drawer: boolean; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const state = usePanel();
  const panel = chatPanel(state, chat);
  const { agents, past } = useAgents(project);
  const stored = panelTarget(state, chat);
  const lead = isLeadRef(chat);
  let whose = stored ? resolveWhose(stored, project, agents, past) : undefined;
  // The lead's panel doesn't show an agent retired since it was last shown,
  // unless it was a past agent when it was picked: it shows the list.
  if (lead && whose?.kind === 'past' && stored && !isPastRef(stored)) whose = undefined;
  const ref = whose && whoseRef(whose);
  // A rich block opened beside the chat goes over all of it, as the
  // project's own views do (RichBeside).
  const preview = panel.preview;
  const over = !!(panel.wide || preview);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const projectName = projectLabel(project, projects.data);
  // An agent of an Android project, or with an emulator running, has Android
  // on its switch, as in v1's tabs (AgentView); booted, with a green dot.
  const live = whose?.kind === 'agent' ? whose.agent : undefined;
  const androidStatus = useQuery({ queryKey: ['android', live?.ref], queryFn: () => api.android(live!.ref), enabled: live?.state === 'running', refetchInterval: 15_000 });
  const android = hasAndroid(projects.data?.find((p) => p.name === project), androidStatus.data);
  let view: PanelView | undefined = panel.wide ?? (whose && viewOf(state, whose));
  // Android, last shown for an agent of a project no longer one: its default.
  if (view === 'android' && whose && projects.isSuccess && !android) view = defaultView(whose);
  // Beside the lead's chat, the list, the pull requests, the repository and
  // the schedules are its tabs: no back arrow, title or ✕ for them, the switch instead.
  // Issues, beside it, is named by the switch the same way.
  const pulls = lead && (panel.wide === 'pulls' || panel.wide === 'issues');
  // Schedules, picked on an agent's switch, keeps the agent's header.
  const home = lead && !preview && (pulls || (panel.wide === 'schedules' && (!whose || whose.kind === 'lead')) || (!over && (!whose || whose.kind === 'lead')));
  // An agent's header, or whoever else's the panel shows but the lead's home.
  const switcher = !!whose && !home && !preview;
  const fit = useNamedFit(`${ref}:${view}:${android}`);

  if (!whose && !over && state.listFolded && !drawer) return <AgentStrip project={project} chat={chat} onSelect={onSelect} onUnfold={() => setPanel({ listFolded: false })} />;
  return (
    <div className="flex min-w-0 flex-1 flex-col" data-panel-view={preview ? 'preview' : (view ?? listTarget)} data-panel-whose={ref}>
      <div ref={fit.row} className={cn('flex h-11 shrink-0 items-center overflow-hidden border-b border-line-faint bg-rail pl-2 pr-1.5', fit.named ? 'gap-1.5' : 'gap-1')}>
        {(whose || over) && !home && (
          <Tip label={t('panel.back')}>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t('panel.back')}
              onClick={() => {
                // In an agent's chat, opened from the lead's list, back is to
                // the lead's chat and its list, in one click.
                if (!lead && !over) {
                  panelBack(leadOf(project));
                  setPanelOpen(leadOf(project), true);
                  onSelect({ kind: 'project', project });
                } else panelBack(chat);
              }}
              data-panel-back
            >
              <ArrowLeft />
            </Button>
          </Tip>
        )}
        {preview ? (
          <span className="min-w-0 truncate pl-1 text-[12.5px] font-semibold text-title" data-panel-preview-title>
            {titleOf(preview.kind, preview.code, t(`chat.rich.${preview.kind}`))}
          </span>
        ) : home ? (
          <LeadSwitch project={project} tab={leadTab(state, project)} files={view === 'files'} count={agents.length} />
        ) : whose ? (
          <WhoseTitle project={project} chat={chat} whose={whose} roomy={fit.named} onSelect={onSelect} />
        ) : (
          !panel.wide && (
            <span className="flex min-w-0 items-center gap-2 pl-1.5">
              <span className="text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">{t('agent.rail.agents')}</span>
              {agents.length > 0 && <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{agents.length}</span>}
            </span>
          )
        )}
        {whose && switcher && <ViewSwitch chat={chat} whose={whose} views={viewsOf(whose, android)} view={view} named={fit.named} booted={androidStatus.data?.booted === true} />}
        <span className="min-w-0 flex-1" />
        {!whose && !over && (
          <Tip label={t('agent.rail.newAgentIn', { project: projectName })}>
            <Button size="icon-sm" variant="ghost" aria-label={t('agent.rail.newAgentIn', { project: projectName })} onClick={() => onNewAgent(project)} data-panel-new-agent>
              <Plus />
            </Button>
          </Tip>
        )}
        {!preview && lead && !switcher && <ProjectButtons project={project} chat={chat} wide={panel.wide} onSelect={onSelect} />}
        {!whose && !over && (
          <Tip label={t('panel.list.fold')}>
            <Button size="icon-sm" variant="ghost" aria-label={t('panel.list.fold')} onClick={() => (drawer ? setPanelOpen(chat, false) : setPanel({ listFolded: true }))} data-panel-fold>
              <ChevronsRight />
            </Button>
          </Tip>
        )}
        {(whose || over) && !home && (
          <Tip label={t('panel.back')}>
            <Button size="icon-sm" variant="ghost" aria-label={t('panel.back')} onClick={() => panelClose(chat)} data-panel-close>
              <X />
            </Button>
          </Tip>
        )}
      </div>
      <ErrorBoundary key={`${ref ?? listTarget}:${preview ? 'preview' : (view ?? '')}`} fallback={<p className="p-4 text-xs text-subtle">{t('panel.failed')}</p>}>
        {preview ? (
          <RichBeside key={preview.code} chat={chat} preview={preview} />
        ) : panel.wide ? (
          <div className="flex min-h-0 flex-1 flex-col">
            <ProjectView project={project} chat={chat} view={panel.wide} section={state.section} titled={!pulls} onSelect={onSelect} />
          </div>
        ) : whose && view ? (
          <div className="flex min-h-0 flex-1 flex-col">
            <Body project={project} chat={chat} whose={whose} view={view} width={width} focus={panel.focus} onSelect={onSelect} />
          </div>
        ) : (
          <AgentList project={project} chat={chat} onSelect={onSelect} onNewAgent={onNewAgent} />
        )}
      </ErrorBoundary>
    </div>
  );
}

// ——— The header ———

// WhoseTitle is whose the panel shows, "● Invoice due dates": its status dot
// and title; a click opens its chat, unless that's the one beside it.
// Beside a switch with a name lit (roomy), it keeps room for a few letters of
// its title, and the switch goes to icons first.
function WhoseTitle({ project, chat, whose, roomy, onSelect }: { project: string; chat: string; whose: Whose; roomy: boolean; onSelect: (view: View) => void }) {
  const t = useT();
  const target: View = whose.kind === 'lead' ? { kind: 'project', project } : whose.kind === 'agent' ? { kind: 'agent', ref: whose.agent.ref } : { kind: 'pastAgents', ref: whose.past.ref };
  const here = (whose.kind === 'lead' && isLeadRef(chat)) || (whose.kind === 'agent' && whose.agent.ref === chat);
  const title = whoseTitle(t, whose);
  return (
    <Tip label={here ? title : t('panel.openChat', { name: title })}>
      <button
        type="button"
        disabled={here}
        onClick={() => onSelect(target)}
        className={cn('flex max-w-[45%] shrink items-center gap-1.5 rounded-md px-1.5 py-1 text-left enabled:hover:bg-surface', roomy ? 'min-w-20' : 'min-w-0')}
        data-panel-whose-title
      >
        <StatusDot whose={whose} />
        <span className="min-w-0 truncate text-[12.5px] font-semibold text-title">{title}</span>
      </button>
    </Tip>
  );
}

// LeadSwitch is the lead's panel's five tabs, Agents (with how many), Pull
// requests (how many are open, and how many of the project's agents are
// ready to review), Files, Evidence and Schedules, in ViewSwitch's look.
// Files is lit only while the repository shows its files: its Browser or
// Timeline, from the ⋯, lights none.
// Only the lit one is named, at any width, the others by their icon and count
// (Pull requests by those ready only, when some are: how many are open is
// in its tooltip).
function LeadSwitch({ project, tab, files, count }: { project: string; tab: LeadTab; files: boolean; count: number }) {
  const t = useT();
  const pulls = useQuery({ queryKey: ['pulls', project], queryFn: () => api.projectPullRequests(project), refetchInterval: 60_000 });
  const open = (pulls.data?.pullRequests ?? []).filter((pr) => pr.state === 'open').length;
  const ready = useProjectReview(project, !!useOpenReview()).data?.items?.length ?? 0;
  const tabs: Record<LeadTab, { icon: LucideIcon; label: string; on: boolean; tip?: string }> = {
    agents: { icon: Users, label: t('panel.lead.agents'), on: tab === 'agents' },
    pulls: {
      icon: GitPullRequest,
      label: t('panel.view.pulls'),
      on: tab === 'pulls',
      tip: [t('panel.view.pulls'), open > 0 && t('v2.header.openPulls', { count: open }), ready > 0 && t('v2.header.readyPulls', { count: ready })].filter(Boolean).join(' · '),
    },
    issues: { icon: CircleDot, label: t('panel.view.issues'), on: tab === 'issues' },
    files: { icon: FolderTree, label: t('panel.view.files'), on: tab === 'files' && files },
    evidence: { icon: Images, label: t('panel.view.evidence'), on: tab === 'evidence' },
    schedules: { icon: AlarmClock, label: t('panel.view.schedules'), on: tab === 'schedules' },
  };
  return (
    <div role="radiogroup" aria-label={t('panel.lead.switch')} className="flex min-w-0 shrink items-center rounded-lg border border-line bg-surface-faint p-0.5" data-panel-lead-switch={tab}>
      {leadTabs.map((v) => {
        const { icon: Icon, label, on, tip } = tabs[v];
        const named = on;
        const button = (
          <button
            key={v}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={tip ?? label}
            onClick={() => setLeadTab(project, v)}
            className={cn('flex h-7 min-w-0 items-center gap-1.5 rounded-md text-[11.5px] font-medium transition-colors', named ? 'px-2.5' : 'shrink-0 px-2', on ? 'bg-surface-raised text-title shadow-sm' : 'text-subtle hover:text-title')}
            data-panel-lead-tab={v}
          >
            <Icon className="size-3.5 shrink-0" />
            {named && <span className="truncate">{label}</span>}
            {v === 'agents' && count > 0 && <span className="shrink-0 rounded-full bg-surface-strong px-1.5 text-[10.5px] tabular-nums text-subtle">{count}</span>}
            {v === 'pulls' && open > 0 && (named || !ready) && <span className="shrink-0 rounded-full bg-surface-strong px-1.5 text-[10.5px] tabular-nums text-subtle" data-panel-pulls-open={open}>{open}</span>}
            {v === 'pulls' && ready > 0 && (
              <span className="shrink-0 rounded-full bg-brand-500/15 px-1.5 text-[10.5px] font-medium tabular-nums text-brand-300 [:root[data-appearance=light]_&]:text-brand-600" data-panel-pulls-ready={ready}>
                {named ? t('v2.header.ready', { count: ready }) : ready}
              </span>
            )}
          </button>
        );
        return named && !tip ? (
          button
        ) : (
          <Tip key={v} label={tip ?? label}>
            <span className={cn('flex', named ? 'min-w-0' : 'shrink-0')}>{button}</span>
          </Tip>
        );
      })}
    </div>
  );
}

// ProjectButtons are the lead's panel's project-wide places, beside its ⋯:
// Work (every agent's work, which takes the main column) and Settings (over
// whatever the panel shows; clicked again, back to it).
function ProjectButtons({ project, chat, wide, onSelect }: { project: string; chat: string; wide: WideView | undefined; onSelect: (view: View) => void }) {
  const t = useT();
  const settings = wide === 'settings';
  return (
    <>
      <Tip label={t('work.open')}>
        <Button size="icon-sm" variant="ghost" aria-label={t('work.open')} onClick={() => onSelect({ kind: 'work', project })} data-open-work>
          <Briefcase />
        </Button>
      </Tip>
      <Tip label={t('v2.sidebar.projectSettings')}>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={t('v2.sidebar.projectSettings')}
          aria-pressed={settings}
          className={cn(settings && 'bg-surface-raised text-primary')}
          onClick={() => {
            if (settings) return panelBack(chat);
            setPanel({ section: 'general' });
            openPanelOn(chat, 'settings');
          }}
          data-project-settings
        >
          <Settings />
        </Button>
      </Tip>
    </>
  );
}

// ViewSwitch is an agent's views (viewsOf), in LeadSwitch's look: the lit
// one named, the others by their icon, with their name in a tooltip; all by
// their icon where the header has no room for the name (useNamedFit).
// Android has a green dot once its emulator has booted.
function ViewSwitch({ chat, whose, views, view, named, booted }: { chat: string; whose: Whose; views: PanelView[]; view: PanelView | undefined; named: boolean; booted: boolean }) {
  const t = useT();
  return (
    <div role="radiogroup" aria-label={t('panel.switch')} className="flex shrink-0 items-center rounded-lg border border-line bg-surface-faint p-0.5" data-panel-switch={named ? 'named' : 'icons'}>
      {views.map((v) => {
        const Icon = viewIcon[v];
        const on = view === v;
        const label = t(viewLabel[v]);
        const shown = named && on;
        const button = (
          <button
            key={v}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={label}
            onClick={() => setView(chat, whoseRef(whose), v)}
            className={cn('relative flex h-7 shrink-0 items-center gap-1.5 rounded-md text-[11.5px] font-medium transition-colors', shown ? 'px-2.5' : 'w-7 justify-center', on ? 'bg-surface-raised text-title shadow-sm' : 'text-subtle hover:text-title')}
            data-panel-switch-view={v}
          >
            <Icon className="size-3.5 shrink-0" />
            {shown && <span className="whitespace-nowrap">{label}</span>}
            {v === 'android' && booted && <span className={cn('size-1.5 shrink-0 rounded-full bg-emerald-400', !shown && 'absolute right-1 top-1')} aria-label={t('agent.view.running')} data-panel-android-booted />}
          </button>
        );
        return shown ? (
          button
        ) : (
          <Tip key={v} label={label}>
            <span className="flex shrink-0">{button}</span>
          </Tip>
        );
      })}
    </div>
  );
}

// useNamedFit is whether the panel's header (row) fits with the switch's lit
// view named. Overflowing, it goes to icons only, until the row is as wide
// again as it needed; a new key (whose, view) measures again.
function useNamedFit(key: string) {
  const row = useRef<HTMLDivElement>(null);
  const [need, setNeed] = useState<{ key: string; width: number } | null>(null);
  const named = need?.key !== key;
  const width = need?.width ?? 0;
  useLayoutEffect(() => {
    const el = row.current;
    if (!el) return;
    const check = () => {
      if (named && el.scrollWidth > el.clientWidth) setNeed({ key, width: el.scrollWidth });
      else if (!named && el.clientWidth >= width) setNeed(null);
    };
    check();
    const observer = new ResizeObserver(check);
    observer.observe(el);
    const sw = el.querySelector('[data-panel-switch]');
    if (sw) observer.observe(sw);
    return () => observer.disconnect();
  }, [key, named, width]);
  return { row, named };
}

// whoseTitle is whose the panel shows, by what it's for: an agent's title
// (its name until it has one), or the repository.
function whoseTitle(t: ReturnType<typeof useT>, w: Whose): string {
  if (w.kind === 'lead') return t('panel.repository');
  const a = w.kind === 'agent' ? w.agent : w.past;
  return a.title || a.name;
}

const whoseName = (w: Whose) => (w.kind === 'agent' ? w.agent.name : w.kind === 'past' ? w.past.name : '');

function StatusDot({ whose, busy }: { whose: Whose; busy?: boolean }) {
  const t = useT();
  if (whose.kind === 'lead') return <Crown className="size-3 shrink-0 text-amber-300" aria-label={t('panel.repository')} />;
  if (whose.kind === 'past') return <Unplug className="size-3 shrink-0 text-faint" aria-label={t('panel.machine.freed')} />;
  const asleep = machineOf(whose.agent) === 'asleep';
  return <span title={t(asleep ? 'panel.machine.asleep' : 'panel.machine.running')} className={cn('block size-2 shrink-0 rounded-full', asleep ? 'bg-faint' : 'bg-emerald-400', busy && 'animate-pulse')} data-panel-dot={asleep ? 'asleep' : 'running'} />;
}

// ——— The views ———

function Body({ project, chat, whose, view, width, focus, onSelect }: { project: string; chat: string; whose: Whose; view: PanelView; width: number; focus?: string; onSelect: (view: View) => void }) {
  if (projectWide(view)) return null;
  const titled = (body: ReactNode) => (
    <>
      <ViewTitle view={view} />
      {body}
    </>
  );
  if (whose.kind === 'agent' && needsMachine(view) && machineOf(whose.agent) === 'asleep') return <Stopped agent={whose.agent} view={view} />;
  switch (view) {
    case 'desktop':
      return whose.kind === 'agent' ? <PanelDesktop chat={chat} agent={whose.agent} /> : null;
    case 'terminal':
      return whose.kind === 'agent' ? <AgentTerminal agent={whose.agent} /> : null;
    case 'files':
      return <Files project={project} whose={whose} width={width} />;
    case 'evidence':
      return <Evidence project={project} whose={whose} focus={focus} />;
    case 'browser':
      return whose.kind === 'agent' ? titled(<PanelBrowser agent={whose.agent} />) : null;
    case 'android':
      return whose.kind === 'agent' ? (
        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto" data-panel-android={whose.agent.ref}>
          <AndroidTab agent={whose.agent} compact={width < 520} onOpenMedia={() => setView(chat, whose.agent.ref, 'evidence')} onSetup={() => onSelect({ kind: 'settings' })} />
        </div>
      ) : null;
    case 'timeline':
      if (whose.kind === 'lead') return null;
      return (
        <>
          <ViewTitle view={view} after={<ShareButton compact agentRef={unPast(whoseRef(whose))} className="ml-auto" />} />
          <TimelinePanel project={project} agentRef={unPast(whoseRef(whose))} />
        </>
      );
  }
}

// ViewTitle names a view off the switch, which then has none lit.
function ViewTitle({ view, after }: { view: PanelView; after?: ReactNode }) {
  const t = useT();
  const Icon = viewIcon[view];
  return (
    <div className="flex h-8 shrink-0 items-center gap-1.5 border-b border-line-faint px-3 text-[12px] font-medium text-secondary" data-panel-view-title={view}>
      <Icon className="size-3.5 text-subtle" />
      {t(viewLabel[view])}
      {after}
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

// Stopped is a view that needs the machine of an agent whose machine is off.
function Stopped({ agent, view }: { agent: T.Agent; view: PanelView }) {
  const t = useT();
  const wake = useWake(agent);
  const paused = agent.state === 'paused';
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center" data-panel-stopped={agent.ref}>
      <span className="flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-surface">
        <Power className="size-5 text-muted" />
      </span>
      <p className="text-[13.5px] font-semibold text-title">{t(paused ? 'panel.paused.title' : 'panel.stopped.title', { name: agent.title || agent.name })}</p>
      <p className="max-w-[320px] text-[12px] leading-relaxed text-subtle">{t('panel.stopped.body', { view: t(viewLabel[view]).toLowerCase() })}</p>
      <Button variant="primary" disabled={wake.isPending} onClick={() => wake.mutate()} data-panel-wake>
        {wake.isPending ? <LoaderCircle className="animate-spin" /> : <Play />}
        {t(paused ? 'panel.stopped.resume' : 'panel.stopped.start')}
      </Button>
    </div>
  );
}

function AgentTerminal({ agent }: { agent: T.Agent }) {
  const wake = useWake(agent);
  return <TerminalTab agent={agent} starting={wake.isPending} onStart={() => wake.mutate()} />;
}

// PanelDesktop is an agent's desktop, live, at its 16:10 with a LIVE badge,
// to watch or take over; under it, the agent's latest screenshots and
// recordings, each opened in Evidence.
function PanelDesktop({ chat, agent }: { chat: string; agent: T.Agent }) {
  const t = useT();
  const queryClient = useQueryClient();
  const running = agent.state === 'running';
  const status = useQuery({ queryKey: ['browser', agent.ref], queryFn: () => api.browser(agent.ref), enabled: running && !standIn, refetchInterval: 3_000 });
  const recording = useQuery({
    queryKey: ['recording', agent.ref],
    queryFn: () => api.recording(agent.ref),
    enabled: running && !standIn,
    refetchInterval: (query) => (query.state.data?.recording ? 2_000 : 10_000),
  });
  const [control, setControl] = useState(false);
  const [now, setNow] = useState(Date.now());
  const desktopUp = !!standIn || (running && status.data?.display === true);
  const picker = usePicker(agent, chat);
  // Picking takes the pointer, as control does: the overlay is on the page.
  const pointer = control || picker.picking;
  const { view, connected } = useVncView(browserViewPath(agent.ref), desktopUp && !standIn, pointer);
  const live = connected || !!standIn;
  const screen = useFullscreen<HTMLDivElement>();
  const isRecording = recording.data?.recording === true;
  const page = status.data?.pages?.[0];
  const openEvidence = (id?: string) => setView(chat, agent.ref, 'evidence', id);
  const saved = (item: T.MediaItem) => toast(t('agent.media.saved', { name: item.name }), { action: { label: t('agent.media.view'), onClick: () => openEvidence(item.id) } });
  const start = useMutation({ mutationFn: () => api.browserAction(agent.ref, 'start'), onSuccess: (next) => queryClient.setQueryData(['browser', agent.ref], next) });
  const shot = useMutation({ mutationFn: () => api.screenshot(agent.ref, { target: 'display', name: 'screen' }), onSuccess: saved });
  const record = useMutation({
    mutationFn: (): Promise<T.MediaItem | T.RecordingStatus> => (isRecording ? api.stopRecording(agent.ref) : api.startRecording(agent.ref, { name: 'screen recording' })),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ['recording', agent.ref] });
      if ('id' in result) saved(result);
    },
  });
  useEffect(() => {
    if (!isRecording) return;
    const timer = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(timer);
  }, [isRecording]);
  const elapsed = recording.data?.startedAt ? (now - new Date(recording.data.startedAt).getTime()) / 1000 : 0;
  const error = start.error ?? shot.error ?? record.error;

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto" data-panel-desktop={agent.ref}>
      <div className="flex h-9 shrink-0 items-center gap-1.5 px-3">
        <span className={cn('flex shrink-0 items-center gap-1 rounded px-1.5 py-px text-[10px] font-bold tracking-wider', live ? 'bg-rose-500/90 text-white' : 'bg-surface-raised text-faint')} data-panel-live={live}>
          <span className={cn('size-1.5 rounded-full', live ? 'animate-pulse bg-white' : 'bg-faint')} />
          {t('panel.live')}
        </span>
        <span className="min-w-0 flex-1 truncate text-[11.5px] text-subtle">{picker.picking ? t('pick.footer') : page?.title || page?.url || ''}</span>
        <PickButton compact picking={picker.picking} disabled={!live || (!standIn && status.data?.running !== true)} onToggle={picker.toggle} />
        <Tip label={t('agent.browser.screenshotTip')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('agent.browser.screenshot')} disabled={!connected || shot.isPending} onClick={() => shot.mutate()}>
            {shot.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
          </Button>
        </Tip>
        <Tip label={isRecording ? t('agent.browser.stopRecording') : t('agent.browser.recordTip')}>
          <Button size="sm" variant="ghost" className={cn('h-7 px-2', isRecording && 'bg-rose-500/15 text-rose-100')} aria-label={isRecording ? t('agent.browser.stopRecording') : t('agent.browser.record')} disabled={!connected || record.isPending} onClick={() => record.mutate()}>
            {isRecording ? <span className="size-2 animate-pulse rounded-[3px] bg-rose-400" /> : <span className="size-2.5 rounded-full bg-rose-500" />}
            {isRecording && <span className="font-mono text-[11px] tabular-nums">{clock(elapsed)}</span>}
          </Button>
        </Tip>
        <Tip label={t(screen.full ? 'agent.browser.exitFullscreen' : 'agent.browser.fullscreen')}>
          <Button size="icon-sm" variant="ghost" disabled={!connected} aria-label={t(screen.full ? 'agent.browser.exitFullscreen' : 'agent.browser.fullscreen')} onClick={screen.toggle}>
            {screen.full ? <Minimize /> : <Maximize />}
          </Button>
        </Tip>
        <Tip label={t('agent.browser.takeControl')}>
          <label className={cn('flex shrink-0 items-center gap-1.5 rounded-md px-1 text-subtle', control && 'text-brand-200')}>
            <Switch checked={control} disabled={!connected} onCheckedChange={setControl} aria-label={t('agent.browser.takeControl')} data-panel-control />
            <MousePointer2 className="size-3.5" />
          </label>
        </Tip>
      </div>
      {error && <p className="px-3 pb-2 text-[11.5px] text-rose-300">{errorMessage(error)}</p>}
      <div className="px-3">
        <div ref={screen.ref} className={cn('relative aspect-[16/10] w-full overflow-hidden rounded-lg border border-line bg-stage', pointer && connected && 'ring-2 ring-brand-400/50', picker.picking && 'ring-brand-400/90')} data-panel-screen>
          {standIn ? (
            <div className="absolute inset-0 flex items-center">
              <div className="w-full">{standIn(agent)}</div>
            </div>
          ) : (
            <>
              <div ref={view} className="absolute inset-0" data-browser-view={agent.ref} data-connected={connected} />
              {connected && !pointer && <div className="absolute inset-0" data-view-guard />}
              {!desktopUp && (
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-2.5 px-4 text-center">
                  <Monitor className="size-6 text-faint" />
                  <p className="text-[12px] text-subtle">{t('panel.desktop.off', { name: agent.name })}</p>
                  <Button size="sm" variant="primary" disabled={start.isPending || status.isPending} onClick={() => start.mutate()} data-panel-start-desktop>
                    {start.isPending ? <LoaderCircle className="animate-spin" /> : <Play />}
                    {t('agent.browser.startDesktop')}
                  </Button>
                </div>
              )}
              {desktopUp && !connected && (
                <div className="absolute inset-0 flex items-center justify-center text-[12px] text-subtle">
                  <LoaderCircle className="mr-2 size-4 animate-spin" />
                  {t('agent.browser.connecting')}
                </div>
              )}
            </>
          )}
          {screen.full && (
            <Button size="icon-sm" className="absolute right-3 top-3 z-10 opacity-60 hover:opacity-100" aria-label={t('agent.browser.exitFullscreen')} onClick={screen.toggle}>
              <Minimize />
            </Button>
          )}
        </div>
      </div>
      <CaptureStrip project={agent.project} name={agent.name} onOpen={openEvidence} />
    </div>
  );
}

const captureKinds = ['screenshot', 'recording'];

// CaptureStrip is an agent's latest screenshots and recordings, newest first,
// under its desktop.
function CaptureStrip({ project, name, onOpen }: { project: string; name: string; onOpen: (id?: string) => void }) {
  const t = useT();
  const media = useMediaPages({ project, agent: name, kinds: captureKinds });
  const items = loadedItems(media.data).slice(0, 8);
  return (
    <div className="px-3 pb-3 pt-3" data-panel-captures>
      <div className="mb-1.5 flex items-center gap-2">
        <span className="text-[10.5px] font-semibold uppercase tracking-wide text-faint">{t('panel.desktop.latest')}</span>
        <button type="button" onClick={() => onOpen()} className="ml-auto text-[11.5px] text-subtle hover:text-title" data-panel-all-evidence>
          {t('panel.desktop.all')} →
        </button>
      </div>
      {items.length === 0 ? (
        <p className="rounded-lg border border-dashed border-line px-3 py-3 text-center text-[11.5px] text-faint">{media.isPending ? t('common.loading') : t('panel.desktop.none')}</p>
      ) : (
        // One row, of as many as fit: the rest are in Evidence.
        <div className="grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] grid-rows-[auto] auto-rows-[0] gap-x-2 overflow-hidden">
          {items.map((item) => (
            <button key={item.id} type="button" onClick={() => onOpen(item.id)} className="group min-w-0 text-left" title={item.name} data-panel-capture={item.id}>
              <Thumb item={item} className="aspect-[16/10] w-full rounded-md border border-line transition group-hover:border-brand-500/60" />
              <span className="mt-1 block truncate text-[10.5px] text-subtle">
                {item.kind === 'recording' && item.meta?.duration ? `${clock(item.meta.duration)} · ` : ''}
                {timeAgo(item.createdAt)}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// Thumb is a screenshot, or a recording's first frame, with its kind's mark.
function Thumb({ item, className }: { item: T.MediaItem; className?: string }) {
  return (
    <span className={cn('relative block overflow-hidden bg-stage', className)}>
      {item.kind === 'recording' ? (
        <video src={`${mediaUrl(item)}#t=0.1`} preload="metadata" muted playsInline className="size-full object-cover object-top" />
      ) : (
        <img src={mediaUrl(item)} alt="" loading="lazy" className="size-full object-cover object-top" />
      )}
      <span className="absolute bottom-1 left-1 flex size-4 items-center justify-center rounded bg-black/60 text-white">{item.kind === 'recording' ? <Film className="size-2.5" /> : <ImageIcon className="size-2.5" />}</span>
    </span>
  );
}

// Evidence is what whose left to show, Media and Hatch pages in one, newest
// first: a recording large (the one opened from the Desktop's strip, else
// the latest), with its chapters; the screenshots and other recordings; then
// pages and reports. The repository's is the whole project's.
function Evidence({ project, whose, focus }: { project: string; whose: Whose; focus?: string }) {
  const t = useT();
  const name = whoseName(whose);
  const media = useMediaPages({ project, ...(name ? { agent: name } : {}) });
  const items = useMemo(() => loadedItems(media.data), [media.data]);
  const pages = useProjectPages(project);
  const own = unPast(whoseRef(whose));
  const [chosen, setChosen] = useState<string | undefined>(focus);
  useEffect(() => setChosen(focus), [focus]);
  const featured = items.find((i) => i.id === chosen && captureKinds.includes(i.kind)) ?? items.find((i) => i.kind === 'recording');
  const captures = items.filter((i) => captureKinds.includes(i.kind) && i.id !== featured?.id);
  const reports = items.filter((i) => !captureKinds.includes(i.kind));
  const myPages = livePages(pages.data).filter((p) => whose.kind === 'lead' || p.agents.includes(own));
  const rest = [...myPages.map((p) => ({ at: p.updatedAt, page: p })), ...reports.map((r) => ({ at: r.createdAt, item: r }))].toSorted((a, b) => b.at.localeCompare(a.at));
  const [openId, setOpenIdState] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);
  const seeMedia = useSeeMedia();
  const setOpenId = (id: string | null) => {
    setOpenIdState(id);
    seeMedia(id);
  };
  const viewing = items.filter((i) => i.id !== featured?.id || i.kind !== 'recording');
  const index = viewing.findIndex((i) => i.id === openId);
  const label = (item: T.MediaItem) => (whose.kind === 'lead' ? (item.agentName ?? '') : '');

  if (media.isPending) return <p className="p-4 text-[12px] text-subtle">{t('common.loading')}</p>;
  if (items.length === 0 && myPages.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-6 text-center" data-panel-evidence-empty>
        <Images className="size-7 text-faint" />
        <p className="max-w-[300px] text-[12.5px] text-subtle">{whose.kind === 'lead' ? t('panel.evidence.emptyRepo') : t('panel.evidence.empty', { name })}</p>
      </div>
    );
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 py-3" data-panel-evidence>
      {featured && <Featured item={featured} agentRef={whose.kind === 'lead' ? undefined : own} label={label(featured)} />}
      {captures.length > 0 && (
        <section className="mt-4" data-panel-evidence-captures>
          <h4 className="mb-1.5 text-[10.5px] font-semibold uppercase tracking-wide text-faint">{t('panel.evidence.captures')}</h4>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-2">
            {captures.map((item) => (
              <button key={item.id} type="button" onClick={() => (item.kind === 'recording' ? setChosen(item.id) : setOpenId(item.id))} className="group min-w-0 text-left" title={item.name} data-panel-evidence-item={item.id}>
                <Thumb item={item} className="aspect-[16/10] w-full rounded-md border border-line transition group-hover:border-brand-500/60" />
                <span className="mt-1 block truncate text-[11px] text-secondary">{item.name}</span>
                <span className="block truncate text-[10.5px] text-faint">{[label(item), timeAgo(item.createdAt)].filter(Boolean).join(' · ')}</span>
              </button>
            ))}
          </div>
        </section>
      )}
      {rest.length > 0 && (
        <section className="mt-4" data-panel-evidence-pages>
          <h4 className="mb-1.5 text-[10.5px] font-semibold uppercase tracking-wide text-faint">{t('panel.evidence.pagesReports')}</h4>
          <div className="grid gap-1">
            {rest.map((r) =>
              'page' in r && r.page ? (
                <button key={`p:${r.page.id}`} type="button" onClick={() => openPage(project, r.page)} className="flex min-w-0 items-center gap-2.5 rounded-lg px-1.5 py-1.5 text-left hover:bg-surface" data-panel-evidence-page={r.page.id}>
                  <PageThumb project={project} page={r.page} className="h-10 w-16 shrink-0 rounded border border-line" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[12.5px] text-title">{r.page.title}</span>
                    <span className="block truncate text-[11px] text-subtle">
                      {t('panel.evidence.page')} · {timeAgo(r.at)}
                    </span>
                  </span>
                </button>
              ) : 'item' in r && r.item ? (
                <ReportRow key={r.item.id} item={r.item} label={label(r.item)} onOpen={() => setOpenId(r.item!.id)} />
              ) : null,
            )}
          </div>
        </section>
      )}
      <MoreMedia pages={media} />
      <MediaViewer items={viewing} index={index} onIndex={(i) => setOpenId(viewing[i]?.id ?? null)} onClose={() => setOpenId(null)} onDelete={(item) => setDeleting(item)} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('project.media.deleteTitle', { name: deleting?.name ?? '' })}
        description={deleting?.agentName ? t('project.media.deleteDescription', { agent: deleting.agentName }) : t('project.media.deleteDescriptionNone')}
        confirmLabel={t('common.delete')}
        destructive
        onConfirm={async () => {
          await api.deleteMedia(deleting!.id);
          setOpenId(null);
        }}
      />
    </div>
  );
}

function ReportRow({ item, label, onOpen }: { item: T.MediaItem; label: string; onOpen: () => void }) {
  const info = kindInfo(item.kind);
  const Icon = info.icon ?? FileText;
  return (
    <button type="button" onClick={onOpen} className="flex min-w-0 items-center gap-2.5 rounded-lg px-1.5 py-1.5 text-left hover:bg-surface" data-panel-evidence-report={item.id}>
      <span className="flex h-10 w-16 shrink-0 items-center justify-center rounded border border-line bg-surface-faint">
        <Icon className="size-4 text-subtle" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[12.5px] text-title">{item.name}</span>
        <span className="block truncate text-[11px] text-subtle">{[info.one, label, timeAgo(item.createdAt)].filter(Boolean).join(' · ')}</span>
      </span>
    </button>
  );
}

// Featured is Evidence's large item: a recording, with its chapters when its
// agent's timeline has steps in it, or a screenshot.
function Featured({ item, agentRef, label }: { item: T.MediaItem; agentRef?: string; label: string }) {
  const t = useT();
  const video = useRef<HTMLVideoElement>(null);
  const timeline = useTimeline(item.kind === 'recording' ? agentRef : undefined);
  const chapters = timeline.data ? chaptersOf(item, timeline.data.steps) : [];
  return (
    <section data-panel-featured={item.id}>
      <div className="mb-1.5 flex min-w-0 items-center gap-1.5 text-[11.5px]">
        {item.kind === 'recording' ? <Film className="size-3.5 shrink-0 text-subtle" /> : <ImageIcon className="size-3.5 shrink-0 text-subtle" />}
        <span className="min-w-0 truncate font-medium text-secondary">{item.name}</span>
        <span className="shrink-0 text-faint">{[label, item.meta?.duration ? clock(item.meta.duration) : '', timeAgo(item.createdAt)].filter(Boolean).join(' · ')}</span>
      </div>
      <div className="overflow-hidden rounded-lg border border-line bg-stage">
        {item.kind === 'recording' ? (
          <video key={item.id} ref={video} src={mediaUrl(item)} controls muted playsInline className="aspect-[16/10] w-full bg-stage" />
        ) : (
          <img src={mediaUrl(item)} alt={item.name} className="aspect-[16/10] w-full object-contain object-top" />
        )}
      </div>
      {chapters.length > 0 && (
        <div className="mt-2" data-panel-chapters>
          <h4 className="mb-1 text-[10.5px] font-semibold uppercase tracking-wide text-faint">{t('panel.evidence.chapters')}</h4>
          <ol className="grid gap-px">
            {chapters.map((c, i) => (
              <li key={i}>
                <button
                  type="button"
                  onClick={() => {
                    if (!video.current) return;
                    video.current.currentTime = c.at;
                    void video.current.play();
                  }}
                  className="flex w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left text-[12px] hover:bg-surface"
                >
                  <span className="shrink-0 font-mono text-[11px] tabular-nums text-brand-300">{clock(c.at)}</span>
                  <span className="min-w-0 truncate text-secondary">{c.title}</span>
                </button>
              </li>
            ))}
          </ol>
        </div>
      )}
    </section>
  );
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

// ——— The project's own views ———

// ProjectView is the project's pull requests or its settings, over whoever
// the panel shows until a view of the switch is picked. The lead's Pull
// requests tab is named by its switch: no title of its own then.
function ProjectView({ project, chat, view, section, titled, onSelect }: { project: string; chat: string; view: WideView; section: PanelState['section']; titled: boolean; onSelect: (view: View) => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const title = <span className="ml-1 min-w-0 truncate font-normal text-subtle">· {projectLabel(project, projects.data)}</span>;
  if (view === 'pulls') {
    return (
      <>
        {titled && <ViewTitle view={view} after={title} />}
        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" data-panel-pulls>
          <PullRequestsPanel
            project={project}
            onSelect={onSelect}
            onOpenAccount={() => {
              setPanel({ section: 'general' });
              openPanelOn(chat, 'settings');
            }}
          />
        </div>
      </>
    );
  }
  if (view === 'issues') {
    return (
      <>
        {titled && <ViewTitle view={view} after={title} />}
        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" data-panel-issues>
          <IssuesPanel
            project={project}
            onSelect={onSelect}
            onOpenAccount={() => {
              setPanel({ section: 'general' });
              openPanelOn(chat, 'settings');
            }}
          />
        </div>
      </>
    );
  }
  if (view === 'schedules') {
    return (
      <>
        <ViewTitle view={view} after={title} />
        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" data-panel-schedules>
          <SchedulesPanel project={project} onSelect={onSelect} />
        </div>
      </>
    );
  }
  if (!p) return null;
  return (
    <>
      <ViewTitle view={view} after={title} />
      <div className="flex min-h-0 flex-1 flex-col" data-panel-settings={section}>
        <ProjectSettingsBody project={p} section={section} onSection={(next) => setPanel({ section: next })} onSelect={onSelect} onOpenMedia={() => openPanelOn(chat, 'evidence', leadOf(project))} />
      </div>
    </>
  );
}
