// UI v2's right-hand panel: closed by default, opened from one button in the
// chat's header (PanelButton) or ⌘D, it gathers what an agent has besides
// its chat: Files (#306), its Desktop, Terminal and Browser, the Hatch pages
// it published and its Media, and the project's Pull requests and Settings.
// It works as T3Code's right panel (apps/web/src/components/RightPanelTabs.tsx,
// apps/web/src/rightPanelStore.ts): tabs along its top, each chat its own,
// dragged to reorder and closed with their ×; with none open, or a new tab
// (+), a launcher of cards, one per kind. A tab of a kind that shows an
// agent's asks whose first, again as cards: the repository or any of the
// project's agents for Files, Pages and Media, the agents with a machine for
// Desktop, Terminal and Browser; the picker in the tab's header changes it.
// One width for every tab (PreviewPanelShell.tsx, RightPanelResizeHandle.tsx).
// lib/rightPanel.ts has its rules and state.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronDown, Crown, FolderTree, GanttChart, GitBranch, GitPullRequest, Globe, SlidersHorizontal, HardDrive, Images, Monitor, Moon, Play, Plus, Search, SquareTerminal, Unplug, X, type LucideIcon } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
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
  activateTab,
  addTab,
  chatPanel,
  chooseKind,
  launcherTarget,
  chooseTarget,
  closeTab,
  hasTab,
  machineOf,
  moveTab,
  needsMachine,
  needsTarget,
  projectWide,
  openPanelOn,
  panelBox,
  panelDefault,
  panelMin,
  panelState,
  pickable,
  fitTabs,
  resolveWhose,
  setPanel,
  setPanelOpen,
  targets,
  usePanel,
  whoseRef,
  type Machine,
  type PanelKind,
  type PanelState,
  type PanelTab,
  type Whose,
} from '../../lib/rightPanel';
import { cn, errorMessage, shortcut } from '../../lib/utils';
import { BrowserTab } from '../BrowserTab';
import { ErrorBoundary } from '../ErrorBoundary';
import { FileBrowser } from '../files/FilePanel';
import { MediaTab } from '../MediaTab';
import { PageIcon } from '../pages/PageThumb';
import { PagesPanel } from '../pages/PagesPanel';
import { useProjectPages } from '../pages/usePages';
import { ProjectIcon } from '../ProjectIcon';
import { ProjectMediaPanel } from '../ProjectMediaPanel';
import { AIIcon, LiveAgentAvatar } from '../state';
import { TerminalTab } from '../TerminalTab';
import { TimelinePanel } from '../timeline/Timeline';
import { ProjectSettingsBody } from '../ProjectView';
import { PullRequestsPanel } from '../PullRequestsPanel';
import { Button } from '../ui/button';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';
import { PanelBrowser } from './PanelBrowser';
import { chatOf } from './PanelButton';

const kindIcon: Record<PanelKind, LucideIcon | typeof PageIcon> = {
  timeline: GanttChart,
  files: FolderTree,
  desktop: Monitor,
  terminal: SquareTerminal,
  browser: Globe,
  pages: PageIcon,
  media: Images,
  pulls: GitPullRequest,
  settings: SlidersHorizontal,
};
const kindLabel: Record<PanelKind, MessageKey> = {
  timeline: 'panel.tab.timeline',
  files: 'panel.tab.files',
  desktop: 'panel.tab.desktop',
  terminal: 'panel.tab.terminal',
  browser: 'panel.tab.browser',
  pages: 'panel.tab.pages',
  media: 'panel.tab.media',
  pulls: 'panel.tab.pulls',
  settings: 'panel.tab.settings',
};
const kindDescription: Record<PanelKind, MessageKey> = {
  timeline: 'panel.desc.timeline',
  files: 'panel.desc.files',
  desktop: 'panel.desc.desktop',
  terminal: 'panel.desc.terminal',
  browser: 'panel.desc.browser',
  pages: 'panel.desc.pages',
  media: 'panel.desc.media',
  pulls: 'panel.desc.pulls',
  settings: 'panel.desc.settings',
};
const kindAsk: Record<PanelKind, MessageKey> = {
  timeline: 'panel.target.timeline',
  files: 'panel.target.files',
  desktop: 'panel.target.desktop',
  terminal: 'panel.target.terminal',
  browser: 'panel.target.browser',
  pages: 'panel.target.pages',
  media: 'panel.target.media',
  pulls: 'panel.target.files',
  settings: 'panel.target.files',
};
// A launcher's letter for each kind, as T3Code's launcher has (B, T, F…).
const kindKey: Record<PanelKind, string> = { timeline: 'L', files: 'F', desktop: 'D', terminal: 'T', browser: 'B', pages: 'P', media: 'M', pulls: 'R', settings: 'S' };
// The launcher's cards, six: Browser (the Desktop has a browser too) and the
// project's Settings (also in the project's ⋯) are links under them, under
// More, and keep their letters.
const agentKinds: PanelKind[] = ['timeline', 'files', 'desktop', 'terminal', 'pages', 'media'];
const projectKinds: PanelKind[] = ['pulls'];
const moreKinds: PanelKind[] = ['browser', 'settings'];

// RightPanelHost sits beside the main column of App, for a view with a chat:
// it lays the chat's panel out beside the chat or as a drawer, and listens
// for ⌘D, Escape and a file a chat opens.
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
        setPanelOpen(chat, !chatPanel(panelState(), chat).open);
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

  // A window narrowed past where the panel fits beside the chat closes it,
  // rather than turning it into a drawer over the chat you were reading;
  // opened again there, it is a drawer (UX test, finding 8).
  const box = panelBox(state.width, room, windowWidth);
  const measured = room > 0;
  const wasDrawer = useRef<boolean | null>(null);
  useEffect(() => {
    if (!measured) return;
    const was = wasDrawer.current;
    wasDrawer.current = box.drawer;
    if (was === false && box.drawer && chat && chatPanel(panelState(), chat).open) setPanelOpen(chat, false);
  }, [box.drawer, measured, chat]);

  if (!at) return <span ref={probe} hidden />;
  return (
    <>
      <span ref={probe} hidden />
      {chatPanel(state, chat).open && room > 0 && (
        <>
          {box.drawer && <div className="fixed inset-0 z-40 animate-fade-in bg-scrim backdrop-blur-sm" onClick={() => setPanelOpen(chat, false)} data-panel-scrim />}
          <aside
            style={{ width: box.width }}
            aria-label={t('panel.label')}
            className={cn('relative flex h-full shrink-0 border-l border-line bg-sunken', box.drawer && 'fixed inset-y-0 right-0 z-50 animate-fade-in bg-modal shadow-[-20px_0_60px_-20px_var(--ab-shadow-deep)]')}
            onKeyDown={(event) => {
              if (event.key === 'Escape' && box.drawer) setPanelOpen(chat, false);
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

// WidthHandle drags the panel's left edge, the one width of all its tabs; a
// double click puts it back.
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

// The project's agents and past agents, for the tabs to find whose they show.
function useAgents(project: string) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const mine = useMemo(() => (agents.data ?? []).filter((a) => a.project === project), [agents.data, project]);
  return { agents: mine, past: past.data ?? [], loaded: !agents.isPending && !past.isPending };
}

function RightPanel({ project, chat, width, onSelect }: { project: string; chat: string; width: number; onSelect: (view: View) => void }) {
  const t = useT();
  const state = usePanel();
  const panel = chatPanel(state, chat);
  const { agents, past, loaded } = useAgents(project);
  const tab = panel.tabs.find((x) => x.id === panel.active) ?? null;
  const whose = (x: PanelTab) => (x.target ? resolveWhose(x.target, project, agents, past) : undefined);

  return (
    <div className="flex min-w-0 flex-1 flex-col" data-panel-tab={tab?.kind ?? 'launcher'} data-panel-whose={tab?.target}>
      <div className="flex h-10 shrink-0 items-stretch border-b border-line-faint bg-rail pr-1.5">
        <TabStrip project={project} chat={chat} tabs={panel.tabs} active={panel.active} whose={whose} />
        <span className="flex shrink-0 items-center gap-0.5 pl-1">
          <Tip label={t('panel.closeTip', { keys: shortcut('D') })}>
            <Button size="icon-sm" variant="ghost" aria-label={t('panel.close')} onClick={() => setPanelOpen(chat, false)} data-panel-close>
              <X />
            </Button>
          </Tip>
        </span>
      </div>
      {tab ? (
        <TabBody key={tab.id} project={project} chat={chat} tab={tab} whose={whose(tab)} agents={agents} past={past} loaded={loaded} width={width} section={state.section} onSelect={onSelect} />
      ) : (
        <Launcher project={project} chat={chat} id={null} />
      )}
    </div>
  );
}

// ——— The tab strip ———

// TabStrip is the panel's tabs along its top, then + right after the last
// (mock C of the v2 UX audit). A tab is its kind's icon and name and whose it
// shows ("Desktop · agent-41"), cut short with an ellipsis, its full name in
// a tooltip; the ones that don't fit go to a "+N" menu (fitTabs), which + then
// follows. Tabs stay where they are: picking one from the menu takes the last
// shown one's place. A tab is dragged along the strip to reorder (T3Code's
// SortableTabList), and closed with its ×, which shows on hover and on the
// tab shown (PanelTabCloseButton), or with a middle click.
function TabStrip({ project, chat, tabs, active, whose }: { project: string; chat: string; tabs: PanelTab[]; active: string | null; whose: (tab: PanelTab) => Whose | undefined }) {
  const t = useT();
  const dragged = useRef<string | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const add = useRef<HTMLSpanElement>(null);
  const measure = useRef<HTMLDivElement>(null);
  const [room, setRoom] = useState(0);
  const [widths, setWidths] = useState<ReadonlyMap<string, number>>(new Map());
  const [kept, setKept] = useState<readonly string[]>([]);
  const pages = useProjectPages(project);
  const fresh = livePages(pages.data).filter(isNew);
  const faces = tabs.map((tab) => {
    const w = whose(tab);
    const label = tab.kind ? t(kindLabel[tab.kind]) : t('panel.newTab');
    const wide = !!tab.kind && projectWide(tab.kind);
    const name = wide || !w || !tab.kind ? '' : whoseName(t, w, tab.kind);
    const dot = tab.kind === 'pages' && fresh.some((p) => !w || w.kind === 'lead' || p.agents.includes(whoseRef(w).replace(/^past:/, '')));
    return { tab, label, name, dot, full: name ? t('panel.tabName', { kind: label, name }) : label };
  });
  const faceKey = faces.map((f) => `${f.tab.id}:${f.full}`).join('|');
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    // The room is the strip's less the + that follows the tabs.
    const measureRoom = () => setRoom(Math.max(0, el.clientWidth - (add.current?.offsetWidth ?? 0)));
    const observer = new ResizeObserver(measureRoom);
    observer.observe(el);
    measureRoom();
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    const next = new Map<string, number>();
    for (const el of measure.current?.querySelectorAll<HTMLElement>('[data-measure-tab]') ?? []) next.set(el.dataset.measureTab!, el.offsetWidth);
    setWidths((prev) => ([...next].every(([k, v]) => prev.get(k) === v) && prev.size === next.size ? prev : next));
  }, [faceKey]);
  const fit = fitTabs(
    tabs.map((tab) => ({ id: tab.id, width: widths.get(tab.id) ?? 120 })),
    active,
    room,
    56,
    kept,
  );
  const shownKey = fit.shown.join('|');
  useLayoutEffect(() => setKept((prev) => (prev.join('|') === shownKey ? prev : shownKey.split('|').filter(Boolean))), [shownKey]);
  const shown = new Set(fit.shown);
  const drop = (index: number) => {
    if (dragged.current) moveTab(chat, dragged.current, index);
    dragged.current = null;
    setOver(null);
  };
  return (
    <div ref={box} className="relative flex min-w-0 flex-1 items-stretch">
      {/* Every tab laid out once, unseen, for its width. */}
      <div aria-hidden className="pointer-events-none invisible absolute left-0 top-0 size-0 overflow-hidden">
        <div ref={measure} className="flex w-max">
        {faces.map((f) => (
          <span key={f.tab.id} data-measure-tab={f.tab.id} className="flex shrink-0 border-r text-[12px]">
            <TabFace face={f} on />
          </span>
        ))}
        </div>
      </div>
      <div role="tablist" aria-label={t('panel.tabs')} className="flex min-w-0 items-stretch overflow-hidden" data-panel-tabs>
        {faces.map((f, index) => {
          const { tab } = f;
          if (!shown.has(tab.id)) return null;
          const on = tab.id === active;
          return (
            <Tip key={tab.id} label={f.full}>
              <div
                role="tab"
                tabIndex={on ? 0 : -1}
                aria-selected={on}
                aria-label={f.full}
                draggable
                onDragStart={(event) => {
                  dragged.current = tab.id;
                  event.dataTransfer.effectAllowed = 'move';
                  event.dataTransfer.setData('text/plain', tab.id);
                }}
                onDragOver={(event) => {
                  if (!dragged.current) return;
                  event.preventDefault();
                  setOver(index);
                }}
                onDragLeave={() => setOver((o) => (o === index ? null : o))}
                onDrop={(event) => {
                  event.preventDefault();
                  drop(index);
                }}
                onDragEnd={() => {
                  dragged.current = null;
                  setOver(null);
                }}
                onClick={() => activateTab(chat, tab.id)}
                onAuxClick={(event) => {
                  if (event.button === 1) closeTab(chat, tab.id);
                }}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') activateTab(chat, tab.id);
                  if (event.key === 'Delete') closeTab(chat, tab.id);
                }}
                className={cn(
                  'group/tab relative flex shrink-0 cursor-default select-none items-stretch border-r border-line-faint text-[12px]',
                  on ? 'bg-sunken text-title' : 'text-subtle hover:bg-surface hover:text-title',
                  over === index && dragged.current !== tab.id && 'shadow-[inset_2px_0_0_var(--color-brand-500)]',
                )}
                data-panel-tab-id={tab.id}
                data-panel-tab-kind={tab.kind ?? 'new'}
              >
                {on && <span className="absolute inset-x-0 bottom-[-1px] h-px bg-sunken" />}
                {on && <span className="absolute inset-x-0 top-0 h-0.5 bg-brand-500" />}
                <TabFace
                  face={f}
                  on={on}
                  onClose={() => closeTab(chat, tab.id)}
                  closeLabel={t('panel.closeTab', { tab: f.full })}
                />
              </div>
            </Tip>
          );
        })}
      </div>
      {fit.hidden.length > 0 && (
        <Menu>
          <Tip label={t('panel.moreTabs', { count: fit.hidden.length })}>
            <MenuTrigger asChild>
              <button type="button" aria-label={t('panel.moreTabs', { count: fit.hidden.length })} className="flex shrink-0 items-center gap-0.5 px-2 text-[12px] text-subtle hover:bg-surface hover:text-title data-[state=open]:text-title" data-panel-more-tabs={fit.hidden.length}>
                +{fit.hidden.length}
                <ChevronDown className="size-3.5" />
              </button>
            </MenuTrigger>
          </Tip>
          <MenuContent align="start" className="max-w-[320px]" data-panel-more-list>
            {faces
              .filter((f) => !shown.has(f.tab.id))
              .map((f) => (
                <MenuItem key={f.tab.id} icon={f.tab.kind ? kindIcon[f.tab.kind] : Plus} onSelect={() => activateTab(chat, f.tab.id)} data-panel-more-tab={f.tab.id}>
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="truncate">{f.label}</span>
                    {f.name && <span className="truncate text-faint">{f.name}</span>}
                  </span>
                </MenuItem>
              ))}
          </MenuContent>
        </Menu>
      )}
      <span ref={add} className="flex shrink-0 items-center px-1">
        <Tip label={t('panel.addTab')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('panel.addTab')} onClick={() => addTab(chat)} data-panel-add>
            <Plus />
          </Button>
        </Tip>
      </span>
      {/* The rest of the strip: a tab dropped here goes last. */}
      <div
        className="min-w-0 flex-1"
        onDragOver={(event) => {
          if (!dragged.current) return;
          event.preventDefault();
          setOver(tabs.length);
        }}
        onDrop={(event) => {
          event.preventDefault();
          drop(tabs.length - 1);
        }}
      />
    </div>
  );
}

type Face = { tab: PanelTab; label: string; name: string; dot: boolean; full: string };

// TabFace is what a tab shows: its kind's icon, its name and whose it shows,
// cut short past 200px, and its ×.
function TabFace({ face, on, onClose, closeLabel }: { face: Face; on: boolean; onClose?: () => void; closeLabel?: string }) {
  const { tab, dot, full } = face;
  const Icon = tab.kind ? kindIcon[tab.kind] : Plus;
  return (
    <span className="flex min-w-0 items-center gap-1.5 pl-2.5 pr-1">
      <span className="relative shrink-0">
        <Icon className="size-3.5" />
        {dot && <span className="absolute -right-1 -top-1 size-1.5 rounded-full bg-brand-500" />}
      </span>
      <span className="max-w-[200px] truncate" data-panel-tab-name>
        {full}
      </span>
      <button
        type="button"
        aria-label={closeLabel}
        tabIndex={onClose ? undefined : -1}
        onClick={(event) => {
          event.stopPropagation();
          onClose?.();
        }}
        className={cn('flex size-5 shrink-0 items-center justify-center rounded text-subtle hover:bg-surface-raised hover:text-title', !on && '[@media(hover:hover)]:opacity-0 focus-visible:opacity-100 group-hover/tab:opacity-100')}
        data-panel-tab-close
      >
        <X className="size-3" />
      </button>
    </span>
  );
}

// ——— The launcher ———

// Launcher is what the panel shows with no tab open, and a new tab: a card
// per kind, each with its icon, name and one line of what it shows, as
// T3Code's RightPanelEmptyState lists its surfaces. Its letters open a card
// while it has the focus. id is the new tab's, null for the empty panel's.
function Launcher({ project, chat, id }: { project: string; chat: string; id: string | null }) {
  const t = useT();
  const pages = useProjectPages(project);
  const fresh = livePages(pages.data).some(isNew);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => ref.current?.focus({ preventScroll: true }), []);
  const choose = (kind: PanelKind) => chooseKind(chat, id, kind, launcherTarget(chat, kind));
  const card = (kind: PanelKind) => {
    const Icon = kindIcon[kind];
    return (
      <button
        key={kind}
        type="button"
        onClick={() => choose(kind)}
        className="group flex min-w-0 items-start gap-3 rounded-xl border border-line bg-surface-faint p-3 text-left transition-colors hover:border-brand-500/50 hover:bg-surface focus-visible:border-brand-500 focus-visible:outline-none"
        data-panel-card={kind}
      >
        <span className="relative flex size-9 shrink-0 items-center justify-center rounded-lg bg-surface-raised text-secondary group-hover:text-title">
          <Icon className="size-[18px]" />
          {kind === 'pages' && fresh && <span className="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-brand-500" />}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-title">{t(kindLabel[kind])}</span>
            <kbd className="shrink-0 rounded border border-line px-1 font-mono text-[10px] text-faint">{kindKey[kind]}</kbd>
          </span>
          <span className="mt-0.5 block text-[11.5px] leading-snug text-subtle">{t(kindDescription[kind])}</span>
        </span>
      </button>
    );
  };
  return (
    <div
      ref={ref}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.metaKey || event.ctrlKey || event.altKey || event.target !== event.currentTarget) return;
        const kind = (Object.keys(kindKey) as PanelKind[]).find((k) => kindKey[k] === event.key.toUpperCase());
        if (!kind) return;
        event.preventDefault();
        choose(kind);
      }}
      className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4 py-6 outline-none"
      aria-label={t('panel.launcher.title')}
      data-panel-launcher={id ?? 'empty'}
    >
      <div className="mx-auto w-full max-w-[640px]">
        <h3 className="mb-3 text-[13px] font-semibold text-title">{t('panel.launcher.title')}</h3>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2">{agentKinds.map(card)}</div>
        <h4 className="mb-2 mt-5 text-[10.5px] font-semibold uppercase tracking-wide text-faint">{t('panel.launcher.project')}</h4>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2">{projectKinds.map(card)}</div>
        <div className="mt-4 flex flex-wrap items-center gap-x-1 gap-y-1 text-[12px]" data-panel-launcher-more>
          <span className="pr-1 text-subtle">{t('common.more')}:</span>
          {moreKinds.map((kind) => {
            const Icon = kindIcon[kind];
            return (
              <button key={kind} type="button" onClick={() => choose(kind)} className="flex items-center gap-1.5 rounded-md px-2 py-1 text-secondary hover:bg-surface hover:text-title" data-panel-card={kind}>
                <Icon className="size-3.5 text-subtle" />
                {t(kindLabel[kind])}
                <kbd className="rounded border border-line px-1 font-mono text-[10px] text-faint">{kindKey[kind]}</kbd>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// ——— Whose: the picker ———

function Picker({ project, kind, whose, agents, past, onPick }: { project: string; kind: PanelKind; whose: Whose; agents: T.Agent[]; past: T.ArchivedAgent[]; onPick: (w: Whose) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className={cn('flex min-w-0 items-center gap-1.5 rounded-md px-1.5 py-1 text-left hover:bg-surface', open && 'bg-surface')} aria-label={t('panel.pickerLabel')} data-panel-picker>
          <WhoseIcon project={project} whose={whose} className="size-5" />
          <span className="min-w-0 truncate text-[12.5px] font-semibold text-title">{whoseName(t, whose, kind)}</span>
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
          withLead={!needsMachine(kind)}
          forTab={kind}
          onPick={(w) => {
            setOpen(false);
            onPick(w);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

// WhoseList is the header picker's agents, with a search and the "With a
// machine" filter; for a kind that needs a machine (forTab), only an agent
// with one can be picked.
function WhoseList({ project, whose, agents, past, withLead, forTab, onPick }: { project: string; whose?: Whose; agents: T.Agent[]; past: T.ArchivedAgent[]; withLead?: boolean; forTab?: PanelKind; onPick: (w: Whose) => void }) {
  const t = useT();
  const { withMachine } = usePanel();
  const [query, setQuery] = useState('');
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const all = pickable(project, agents, past, false);
  const shown = pickable(project, agents, past, withMachine).filter((w) => matches(w, query));
  const current = whose && whoseRef(whose);
  const projectName = projectLabel(project, projects.data);
  const leadName = t(forTab === 'pages' ? 'panel.all' : 'panel.repository');
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
        <MachineFilter />
      </div>
      <div className={cn('min-h-0 overflow-y-auto p-1', withLead ? 'max-h-[min(420px,calc(var(--radix-popover-content-available-height,100vh)-96px))]' : 'max-h-[420px]')}>
        {withLead && matchesText(leadName, query) && (
          <>
            <Row
              active={whose?.kind === 'lead'}
              onClick={() => onPick({ kind: 'lead', ref: `${project}/${A.LeadName}` })}
              icon={<WhoseIcon project={project} whose={{ kind: 'lead', ref: `${project}/${A.LeadName}` }} className="size-7 text-[11px]" />}
              title={
                <>
                  {leadName} <Crown className="size-3 text-amber-300" />
                </>
              }
              sub={t(forTab === 'pages' ? 'panel.allSub' : 'panel.repositorySub', { project: projectName })}
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
                  {w.kind === 'agent' ? w.agent.name : w.past.name}
                  {(w.kind === 'agent' ? w.agent.title : w.past.title) ? ` · ${w.kind === 'agent' ? w.agent.title : w.past.title}` : ''} <WhoseMark whose={w} />
                </>
              }
              sub={w.kind === 'agent' ? w.agent.branch : w.past.branch}
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

// whoseName is whose a tab shows, by name. The lead's is the repository,
// but under Pages it's every page the project has: All.
function whoseName(t: ReturnType<typeof useT>, w: Whose, kind?: PanelKind): string {
  return w.kind === 'lead' ? t(kind === 'pages' ? 'panel.all' : 'panel.repository') : w.kind === 'agent' ? w.agent.name : w.past.name;
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

// TabBody is a tab: the launcher while it is new, whose it shows to choose
// while that is unset (or gone), and then what it shows, under a header
// whose picker changes whose.
function TabBody({ project, chat, tab, whose, agents, past, loaded, width, section, onSelect }: { project: string; chat: string; tab: PanelTab; whose: Whose | undefined; agents: T.Agent[]; past: T.ArchivedAgent[]; loaded: boolean; width: number; section: PanelState['section']; onSelect: (view: View) => void }) {
  if (!tab.kind) return <Launcher project={project} chat={chat} id={tab.id} />;
  const kind = tab.kind;
  if (projectWide(kind)) {
    return (
      <>
        <div className="flex h-9 shrink-0 items-center border-b border-line-faint px-1.5">
          <ProjectName project={project} />
        </div>
        <ProjectTab project={project} chat={chat} tab={kind} section={section} onSelect={onSelect} />
      </>
    );
  }
  const pick = (w: Whose) => chooseTarget(chat, tab.id, whoseRef(w));
  if (needsTarget(tab) || (!whose && loaded)) return <ChooseTarget project={project} chat={chat} kind={kind} agents={agents} past={past} onPick={pick} />;
  if (!whose) return null;
  return (
    <>
      <div className="flex h-9 shrink-0 items-center border-b border-line-faint px-1.5">
        <Picker project={project} kind={kind} whose={whose} agents={agents} past={past} onPick={pick} />
      </div>
      <Body project={project} chat={chat} whose={whose} tab={kind} agents={agents} past={past} width={width} onPick={pick} />
    </>
  );
}

function Body({ project, chat, whose, tab, agents, past, width, onPick }: { project: string; chat: string; whose: Whose; tab: PanelKind; agents: T.Agent[]; past: T.ArchivedAgent[]; width: number; onPick: (w: Whose) => void }) {
  const t = useT();
  if (!hasTab(whose, tab)) return <ChooseTarget project={project} chat={chat} kind={tab} whose={whose} agents={agents} past={past} onPick={onPick} />;
  if (whose.kind === 'agent' && needsMachine(tab) && machineOf(whose.agent) === 'asleep') return <Asleep agent={whose.agent} tab={tab} />;
  return (
    <ErrorBoundary key={`${whoseRef(whose)}:${tab}`} fallback={<p className="p-4 text-xs text-subtle">{t('panel.failed')}</p>}>
    <div className="flex min-h-0 flex-1 flex-col">
      {tab === 'files' && <Files project={project} whose={whose} width={width} />}
      {tab === 'desktop' && whose.kind === 'agent' && <BrowserTab agent={whose.agent} onOpenMedia={() => openPanelOn(chat, 'media', whoseRef(whose))} />}
      {tab === 'terminal' && whose.kind === 'agent' && <AgentTerminal agent={whose.agent} />}
      {tab === 'browser' && whose.kind === 'agent' && <PanelBrowser agent={whose.agent} />}
      {tab === 'pages' && <Pages project={project} whose={whose} agents={agents} />}
      {tab === 'media' && <Media project={project} whose={whose} />}
      {tab === 'timeline' && whose.kind !== 'lead' && <TimelinePanel project={project} agentRef={whose.kind === 'agent' ? whose.agent.ref : whose.past.ref} />}
    </div>
    </ErrorBoundary>
  );
}

// ChooseTarget is a tab asking whose it shows, as cards: targets() for its
// kind, with a search and, where past agents may be picked, the "With a
// machine" filter. whose is set when what was picked can't show this kind
// any more: an agent retired since, whose machine was freed.
function ChooseTarget({ project, chat, kind, whose, agents, past, onPick }: { project: string; chat: string; kind: PanelKind; whose?: Whose; agents: T.Agent[]; past: T.ArchivedAgent[]; onPick: (w: Whose) => void }) {
  const t = useT();
  const { withMachine } = usePanel();
  const [query, setQuery] = useState('');
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const Icon = kindIcon[kind];
  const machine = needsMachine(kind);
  const all = targets(kind, project, chat, agents, past, false);
  const shown = targets(kind, project, chat, agents, past, withMachine).filter((w) => (w.kind === 'lead' ? matchesText(whoseName(t, w, kind), query) : matches(w, query)));
  const agentsShown = shown.filter((w) => w.kind !== 'lead').length;
  const hidden = all.length - targets(kind, project, chat, agents, past, true).length;
  const projectName = projectLabel(project, projects.data);
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4 py-5" data-panel-choose={kind}>
      <div className="mx-auto w-full max-w-[640px]">
        <div className="mb-3 flex items-start gap-3">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-surface-raised text-secondary">
            <Icon className="size-[18px]" />
          </span>
          <span className="min-w-0">
            <span className="block text-[13px] font-semibold text-title">{t(kindAsk[kind])}</span>
            <span className="block text-[11.5px] text-subtle">
              {whose && whose.kind !== 'agent'
                ? t(whose.kind === 'lead' ? 'panel.choose.lead' : 'panel.choose.freed', { tab: t(kindLabel[kind]).toLowerCase(), name: whose.kind === 'past' ? whose.past.name : '' })
                : t(machine ? 'panel.target.machineSub' : 'panel.target.sub')}
            </span>
          </span>
        </div>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <span className="flex min-w-[160px] flex-1 items-center gap-2 rounded-lg border border-line bg-surface-faint px-2.5 py-1.5">
            <Search className="size-3.5 shrink-0 text-faint" />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t('panel.find')}
              aria-label={t('panel.find')}
              className="min-w-0 flex-1 bg-transparent text-[12.5px] text-primary outline-none placeholder:text-faint"
              data-whose-find
            />
          </span>
          {!machine && <MachineFilter />}
        </div>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2">
          {shown.map((w) => {
            const ref = whoseRef(w);
            const sub = w.kind === 'lead' ? t(kind === 'pages' ? 'panel.allSub' : 'panel.repositorySub', { project: projectName }) : (w.kind === 'agent' ? w.agent.title : w.past.title) || (w.kind === 'agent' ? w.agent.branch : w.past.branch);
            const own = ref === chat;
            return (
              <button
                key={ref}
                type="button"
                onClick={() => onPick(w)}
                className={cn('flex min-w-0 flex-col gap-2 rounded-xl border bg-surface-faint p-3 text-left transition-colors hover:border-brand-500/50 hover:bg-surface focus-visible:border-brand-500 focus-visible:outline-none', own ? 'border-brand-500/40' : 'border-line')}
                data-whose-card={ref}
              >
                <span className="flex min-w-0 items-center gap-2">
                  <WhoseIcon project={project} whose={w} className="size-7 text-[11px]" />
                  <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-title">{whoseName(t, w, kind)}</span>
                  <WhoseMark whose={w} />
                </span>
                <span className="line-clamp-2 min-h-[2lh] text-[11.5px] leading-snug text-subtle">{sub}</span>
                <span className="flex items-center justify-between gap-2 text-[10.5px] text-faint">
                  <span>{t(machineLabel[machineOfWhose(w)])}</span>
                  {own && <span className="rounded-full bg-brand-500/15 px-1.5 py-px font-medium text-brand-300">{t('panel.thisChat')}</span>}
                </span>
              </button>
            );
          })}
        </div>
        {agentsShown === 0 && <p className="px-3 py-4 text-center text-[12px] text-subtle">{query ? t('panel.noMatch') : machine || withMachine ? t('panel.noMachines') : t('panel.noAgents')}</p>}
        {!machine && withMachine && hidden > 0 && !query && <p className="pt-3 text-[11px] text-faint">{t('panel.hidden', { count: hidden })}</p>}
      </div>
    </div>
  );
}

// MachineFilter is the "With a machine" chip, remembered for every picker.
function MachineFilter() {
  const t = useT();
  const { withMachine } = usePanel();
  return (
    <button
      type="button"
      aria-pressed={withMachine}
      onClick={() => setPanel({ withMachine: !withMachine })}
      className={cn('flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px]', withMachine ? 'border-brand-500/60 bg-brand-500/15 text-title' : 'border-line text-subtle hover:text-title')}
      title={t('panel.withMachineTip')}
      data-whose-machine-filter
    >
      <HardDrive className="size-3" />
      {t('panel.withMachine')}
      {withMachine && <X className="size-3" />}
    </button>
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

function Asleep({ agent, tab }: { agent: T.Agent; tab: PanelKind }) {
  const t = useT();
  const wake = useWake(agent);
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center" data-panel-asleep={agent.ref}>
      <Moon className="size-8 text-faint" />
      <p className="max-w-[300px] text-[12.5px] text-subtle">{t('panel.asleep', { name: agent.name, tab: t(kindLabel[tab]).toLowerCase() })}</p>
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
function ProjectTab({ project, chat, tab, section, onSelect }: { project: string; chat: string; tab: PanelKind; section: PanelState['section']; onSelect: (view: View) => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  if (tab === 'pulls') {
    return (
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
    );
  }
  if (!p) return null;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-panel-settings={section}>
      <ProjectSettingsBody project={p} section={section} onSection={(next) => setPanel({ section: next })} onSelect={onSelect} onOpenMedia={() => openPanelOn(chat, 'media', `${project}/${A.LeadName}`)} />
    </div>
  );
}
