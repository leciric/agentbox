// A prototype of a horizontal-tabs layout, against the same fixtures as the
// rest of the preview harness (dev/preview.tsx's ?layout=tabs). The idea:
// vertical UI is navigation and history, horizontal UI is the live working
// set. The icon rail stays; the agent/chat sidebar collapses into a drawer
// (History, toggled by the rail or ⌘⇧H) that lists what isn't a tab right
// now; a row of tabs across the top holds one per live thread — the lead
// chat, running or queued agents, the main chat — each closable without
// stopping its agent, reorderable by drag, and scrollable on overflow. Below
// it, the chat at a fixed width beside a resizable right panel.
//
// Not part of the app; nothing here ships (see the comment atop preview.tsx).
import { useQuery, type QueryClient } from '@tanstack/react-query';
import {
  Check,
  CircleDashed,
  CircleDot,
  Clock,
  Globe,
  History,
  LoaderCircle,
  MessagesSquare,
  MousePointerClick,
  OctagonX,
  RefreshCw,
  Sparkles,
  X,
} from 'lucide-react';
import { type ComponentType, type PointerEvent as ReactPointerEvent, useEffect, useMemo, useRef, useState } from 'react';
import * as T from '../../shared/api';
import { MediaTab } from '../components/MediaTab';
import { OverviewTab } from '../components/OverviewTab';
import { ChatTab } from '../components/chat/ChatTab';
import { homeAgentFrom } from '../components/HomeChatPanel';
import { leadAgentFrom } from '../components/ProjectChatPanel';
import { AIIcon, LiveAgentAvatar } from '../components/state';
import { Tip } from '../components/ui/tooltip';
import { api, isProjectChat } from '../lib/api';
import { chatLabel } from '../lib/agentStatus';
import { cn } from '../lib/utils';
import { agent, agent12Chat, leadChat, mediaItems, organicProject, PROJECT, type FixtureData } from './fixtures';

const AB_LEAD_REF = `${PROJECT}/lead`;
const ORGANIC_LEAD_REF = 'organic/lead';
const HOME_LEAD_REF = `${T.HomeProject}/lead`;
const EMPTY_PROJECTS: T.Project[] = [];

// The tabs open by default: every status the task calls for (working,
// needing input, done, failed, queued, stopped) across three projects, so
// the strip also overflows and scrolls.
const INITIAL_OPEN = [
  AB_LEAD_REF,
  HOME_LEAD_REF,
  `${PROJECT}/agent-12`,
  `${PROJECT}/agent-98`,
  `${PROJECT}/agent-97`,
  `${PROJECT}/agent-87`,
  `${PROJECT}/agent-93`,
  ORGANIC_LEAD_REF,
  'organic/agent-1',
  'organic/agent-q1',
];
const DEFAULT_ACTIVE = `${PROJECT}/agent-12`;

// A small built-in thread for an agent none of fixtures.ts's richer ones
// (leadChat, agent12Chat, agent99Chat) cover, so its tab opens on a real
// conversation rather than the empty-chat hero.
function thread(ref: string, tool: string, state: string, exchanges: [string, string]): T.ChatThread {
  const at = (ago: number) => new Date(Date.now() - ago).toISOString();
  return {
    agent: ref,
    seq: 1,
    session: { state, tool, options: [], commands: [] },
    items: [
      { id: 't1', turn: 't1', kind: 'user', text: exchanges[0], createdAt: at(90_000), updatedAt: at(90_000) },
      { id: 't2', turn: 't1', kind: 'assistant', text: exchanges[1], createdAt: at(60_000), updatedAt: at(60_000) },
    ],
  };
}

// Project colours: a tab and its drawer section wear their project's, so
// tabs from several projects visibly group even scrolled apart. Home has no
// project of its own, so it gets a neutral one rather than joining a group.
const PALETTE = ['#6d5dfc', '#10b981', '#0ea5e9', '#f59e0b', '#f43f5e', '#a855f7'];
function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}
function projectColor(project: string, homeProject: string): string {
  if (project === homeProject) return '#64748b';
  if (project === PROJECT) return PALETTE[0];
  if (project === 'organic') return PALETTE[1];
  return PALETTE[2 + (hash(project) % (PALETTE.length - 2))];
}

// seedTabsLayout seeds everything this scenario's tabs and drawer read that
// buildFixtures()/seedQueryClient don't already: a second project (organic)
// with a few of its own agents in states agentbox's fixtures don't have
// (queued, a second stopped one), their conversations, and each shown
// agent's diff and media — never the dev bridge's generic {} (fixtures.ts).
export function seedTabsLayout(queryClient: QueryClient, fixtures: FixtureData): void {
  const abProject = fixtures.projects.find((p) => p.name === PROJECT)!;
  const org = organicProject();
  queryClient.setQueryData(['projects'], [abProject, org]);
  queryClient.setQueryData(['sections'], fixtures.sections);

  const orgAgent1 = agent({ ref: 'organic/agent-1', project: 'organic', title: 'Rebuild the checkout flow', ai: 'claude', chat: 'running', state: 'running' });
  const orgAgentQ1 = agent({ ref: 'organic/agent-q1', project: 'organic', title: 'Speed up the search index', ai: 'claude', state: 'queued', queuePosition: 1, chat: undefined });
  const orgAgent2 = agent({ ref: 'organic/agent-2', project: 'organic', title: 'Add the loyalty points banner', ai: 'codex', state: 'stopped', chat: undefined });
  queryClient.setQueryData(['agents'], [...fixtures.agents, orgAgent1, orgAgentQ1, orgAgent2]);

  queryClient.setQueryData(['questions', 'organic'], []);
  queryClient.setQueryData(['questions', T.HomeProject], []);
  queryClient.setQueryData(['agentEvents', 'organic'], []);

  queryClient.setQueryData(['chat', AB_LEAD_REF], leadChat());
  queryClient.setQueryData(['chat', `${PROJECT}/agent-12`], agent12Chat());
  queryClient.setQueryData(
    ['chat', HOME_LEAD_REF],
    thread(HOME_LEAD_REF, 'claude', 'ready', [
      'What are the other two projects up to?',
      'organic has one agent rebuilding the checkout flow and one queued; agentbox has a few finished and one that failed migrating the reminders table.',
    ]),
  );
  queryClient.setQueryData(
    ['chat', ORGANIC_LEAD_REF],
    thread(ORGANIC_LEAD_REF, 'claude', 'ready', ['Kick off the checkout rebuild and queue the search index work behind it.', "Started agent-1 on the checkout flow; agent-q1 is queued behind it."]),
  );
  queryClient.setQueryData(
    ['chat', `${PROJECT}/agent-98`],
    thread(`${PROJECT}/agent-98`, 'codex', 'waiting', [
      'Handle the daemon reporting a JSON payload inline, same as the rest of the summary.',
      'Should this truncate a long one, or always show it in full? Asking before I pick.',
    ]),
  );
  queryClient.setQueryData(
    ['chat', `${PROJECT}/agent-97`],
    thread(`${PROJECT}/agent-97`, 'opencode', 'ready', ['The agent rail column widens with a long path in it.', 'Found it: overflow-y-auto with no overflow-x handling. Fixed, and opened the pull request.']),
  );
  queryClient.setQueryData(
    ['chat', `${PROJECT}/agent-87`],
    thread(`${PROJECT}/agent-87`, 'codex', 'error', ['Migrate the reminders table to the new schema.', 'The migration ran, but the AI tool exited mid-turn before confirming it; see the stack trace in Overview.']),
  );
  queryClient.setQueryData(
    ['chat', `${PROJECT}/agent-93`],
    thread(`${PROJECT}/agent-93`, 'opencode', 'off', ['Anything left before you stop for the night?', 'Nothing pending; stopped and freed its Docker space.']),
  );
  queryClient.setQueryData(
    ['chat', 'organic/agent-1'],
    thread('organic/agent-1', 'claude', 'running', ['Rebuild the checkout flow around the new cart API.', 'Working through the cart and payment steps now; the old flow stays up until this is done.']),
  );

  const media = mediaItems(32);
  const diffs: [string, string, string][] = [
    [`${PROJECT}/agent-12`, 'agent-12', ' desktop/src/renderer/components/Sidebar.tsx | 9 +++++++--\n 1 file changed, 7 insertions(+), 2 deletions(-)'],
    [`${PROJECT}/agent-98`, 'agent-98', ''],
    [`${PROJECT}/agent-97`, 'agent-97', ' desktop/src/renderer/components/chat/Markdown.tsx | 18 +++++++++---\n 1 file changed, 14 insertions(+), 4 deletions(-)'],
    [`${PROJECT}/agent-87`, 'agent-87', ''],
    [`${PROJECT}/agent-93`, 'agent-93', ' internal/daemon/settings.go | 6 +++---\n 1 file changed, 3 insertions(+), 3 deletions(-)'],
    ['organic/agent-1', 'agent-1', ' web/checkout/Cart.tsx | 64 ++++++++++++++++------\n 1 file changed, 48 insertions(+), 16 deletions(-)'],
    ['organic/agent-q1', 'agent-q1', ''],
  ];
  for (const [ref, name, diff] of diffs) {
    queryClient.setQueryData(['diff', ref], diff);
    queryClient.setQueryData(
      ['media', ref],
      media.filter((m) => m.agentName === name),
    );
  }
}

// tabStatus reads chatLabel's own tone (the project's one vocabulary for
// what an agent is up to) and adds a glyph the task's six states tell apart
// at a glance: chatLabel's "muted" alone doesn't say queued from stopped
// from settled-and-fine, so state splits it further.
function tabStatus(a: T.Agent): { Icon: ComponentType<{ className?: string }>; className: string; label: string } {
  const { text, tone } = chatLabel(a);
  if (tone === 'urgent') return { Icon: CircleDot, className: 'text-amber-400 animate-pulse', label: text };
  if (tone === 'error') return { Icon: OctagonX, className: 'text-rose-400', label: text };
  if (tone === 'live') return { Icon: LoaderCircle, className: 'text-sky-400 animate-spin', label: text };
  if (a.state === 'queued') return { Icon: Clock, className: 'text-subtle', label: text };
  if (a.state === 'stopped' || a.state === 'paused') return { Icon: CircleDashed, className: 'text-subtle', label: text };
  return { Icon: Check, className: 'text-emerald-400/80', label: text };
}

function PreviewPlaceholder({ agent: a }: { agent: T.Agent }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b border-line-faint px-3 py-2 text-[12px] text-subtle">
        <RefreshCw className="size-3.5" />
        <span className="truncate font-mono">{`http://5173.${a.ref.split('/')[1]}.agentbox.localhost:7777`}</span>
      </div>
      <div className="flex flex-1 items-center justify-center bg-gradient-to-br from-brand-500/10 via-transparent to-transparent">
        <div className="grid justify-items-center gap-2 text-subtle">
          <Globe className="size-6" />
          <p className="text-[13px]">Live preview of {a.title || a.name}</p>
        </div>
      </div>
    </div>
  );
}

function BrowserPlaceholder({ agent: a }: { agent: T.Agent }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-1.5 border-b border-line-faint px-3 py-2 text-[12px] text-subtle">
        <span className="size-2 rounded-full bg-rose-400/60" />
        <span className="size-2 rounded-full bg-amber-400/60" />
        <span className="size-2 rounded-full bg-emerald-400/60" />
        <span className="ml-2 truncate">{a.title || a.name}'s desktop</span>
      </div>
      <div className="flex flex-1 items-center justify-center bg-sunken">
        <div className="grid justify-items-center gap-2 text-subtle">
          <MousePointerClick className="size-6" />
          <p className="text-[13px]">Nobody is driving it right now</p>
        </div>
      </div>
    </div>
  );
}

type PanelKind = 'preview' | 'browser' | 'diff' | 'media';

function Workspace({ agent: a, panel, onPanel, chatWidth, onResizeStart }: { agent: T.Agent; panel: PanelKind; onPanel: (p: PanelKind) => void; chatWidth: number; onResizeStart: (e: ReactPointerEvent) => void }) {
  const hasMachine = !isProjectChat(a.ref);
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-h-0 flex-col" style={{ width: hasMachine ? chatWidth : '100%', flexShrink: 0 }}>
        <ChatTab agent={a} starting={false} onStart={() => {}} autoStart={false} />
      </div>
      {hasMachine && (
        <>
          <div className="w-1 shrink-0 cursor-col-resize bg-line-faint hover:bg-brand-400/50" data-resizer onPointerDown={onResizeStart} />
          <div className="flex min-h-0 flex-1 flex-col border-l border-line-faint">
            <div className="flex shrink-0 items-center gap-1 border-b border-line-faint px-3 py-1.5">
              {(['preview', 'browser', 'diff', 'media'] as const).map((p) => (
                <button
                  key={p}
                  className={cn('rounded-md px-2.5 py-1 text-[12.5px] capitalize', panel === p ? 'bg-surface-faint text-title' : 'text-muted hover:text-secondary')}
                  onClick={() => onPanel(p)}
                >
                  {p}
                </button>
              ))}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              {panel === 'diff' && <OverviewTab agent={a} section="code" />}
              {panel === 'media' && <MediaTab agent={a} />}
              {panel === 'preview' && <PreviewPlaceholder agent={a} />}
              {panel === 'browser' && <BrowserPlaceholder agent={a} />}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function TabStrip({
  refs,
  agents,
  active,
  onActivate,
  onClose,
  onReorder,
  homeProject,
}: {
  refs: string[];
  agents: Map<string, T.Agent>;
  active: string;
  onActivate: (ref: string) => void;
  onClose: (ref: string) => void;
  onReorder: (from: string, to: string) => void;
  homeProject: string;
}) {
  const dragRef = useRef<string | null>(null);
  return (
    <div className="flex h-11 shrink-0 items-stretch overflow-x-auto border-b border-line-faint bg-surface" data-tab-strip>
      {refs.map((ref) => {
        const a = agents.get(ref);
        if (!a) return null;
        const { Icon, className, label } = tabStatus(a);
        const isActive = ref === active;
        return (
          <div
            key={ref}
            draggable
            onDragStart={() => (dragRef.current = ref)}
            onDragOver={(e) => {
              e.preventDefault();
              if (dragRef.current && dragRef.current !== ref) onReorder(dragRef.current, ref);
            }}
            onDragEnd={() => (dragRef.current = null)}
            onMouseDown={(e) => {
              if (e.button === 1) {
                e.preventDefault();
                onClose(ref);
              }
            }}
            onAuxClick={(e) => e.preventDefault()}
            onClick={() => onActivate(ref)}
            className={cn(
              'group relative flex min-w-[150px] max-w-[230px] shrink-0 cursor-pointer items-center gap-2 border-r border-line-faint border-l-[3px] px-3',
              isActive ? 'bg-chat text-title' : 'text-secondary hover:bg-surface-faint',
            )}
            style={{ borderLeftColor: projectColor(a.project, homeProject) }}
            data-tab={ref}
            data-tab-active={isActive}
          >
            <AIIcon ai={a.ai} className="size-3.5 shrink-0 text-muted" />
            <span className="min-w-0 flex-1 truncate text-[12.5px]">{a.title || a.name}</span>
            <Tip label={label}>
              <Icon className={cn('size-3.5 shrink-0', className)} />
            </Tip>
            <button
              className="shrink-0 rounded p-0.5 text-muted opacity-0 hover:bg-surface-raised hover:text-title group-hover:opacity-100"
              onClick={(e) => {
                e.stopPropagation();
                onClose(ref);
              }}
              aria-label={`Close ${a.title || a.name}`}
            >
              <X className="size-3.5" />
            </button>
          </div>
        );
      })}
    </div>
  );
}

function IconRail({
  projects,
  drawerOpen,
  onToggleDrawer,
  onFilterProject,
  onOpenHome,
  homeProject,
}: {
  projects: T.Project[];
  drawerOpen: boolean;
  onToggleDrawer: (project?: string) => void;
  onFilterProject: (project: string) => void;
  onOpenHome: () => void;
  homeProject: string;
}) {
  return (
    <div className="flex w-14 shrink-0 flex-col items-center gap-1 border-r border-line-faint bg-surface py-3" data-icon-rail>
      <div className="mb-2 flex size-8 items-center justify-center rounded-lg bg-brand-500/15 text-brand-300">
        <Sparkles className="size-4" />
      </div>
      <Tip label="History (⌘⇧H)">
        <button
          className={cn('flex size-9 items-center justify-center rounded-lg text-muted hover:bg-surface-faint hover:text-title', drawerOpen && 'bg-surface-faint text-title')}
          onClick={() => onToggleDrawer()}
          data-history-toggle
        >
          <History className="size-[18px]" />
        </button>
      </Tip>
      <div className="my-1.5 h-px w-7 bg-line-faint" />
      {projects.map((p) => (
        <Tip key={p.name} label={p.displayName || p.name}>
          <button
            className="flex size-9 items-center justify-center rounded-lg"
            onClick={() => onFilterProject(p.name)}
            data-project-icon={p.name}
          >
            <span
              className="flex size-6 items-center justify-center rounded-full text-[11px] font-semibold"
              style={{ background: `${projectColor(p.name, homeProject)}26`, color: projectColor(p.name, homeProject) }}
            >
              {(p.displayName || p.name).charAt(0).toUpperCase()}
            </span>
          </button>
        </Tip>
      ))}
      <div className="my-1.5 h-px w-7 bg-line-faint" />
      <Tip label="Main chat">
        <button className="flex size-9 items-center justify-center rounded-lg text-muted hover:bg-surface-faint hover:text-title" onClick={onOpenHome}>
          <MessagesSquare className="size-[18px]" />
        </button>
      </Tip>
    </div>
  );
}

function HistoryDrawer({
  agents,
  openRefs,
  filterProject,
  projects,
  homeProject,
  onClose,
  onOpen,
}: {
  agents: T.Agent[];
  openRefs: string[];
  filterProject?: string;
  projects: T.Project[];
  homeProject: string;
  onClose: () => void;
  onOpen: (ref: string) => void;
}) {
  const grouped = useMemo(() => {
    const rest = agents.filter((a) => !openRefs.includes(a.ref) && (!filterProject || a.project === filterProject));
    const byProject = new Map<string, T.Agent[]>();
    for (const a of rest) {
      const list = byProject.get(a.project) ?? [];
      list.push(a);
      byProject.set(a.project, list);
    }
    return byProject;
  }, [agents, openRefs, filterProject]);
  const projectName = (name: string) => projects.find((p) => p.name === name)?.displayName || name;
  return (
    <div className="fixed inset-0 z-40 flex" data-history-drawer>
      <div className="absolute inset-0 animate-fade-in bg-scrim backdrop-blur-sm" onClick={onClose} />
      <div className="relative flex h-full w-[360px] animate-fade-in flex-col bg-modal shadow-[20px_0_60px_-20px_var(--ab-shadow-deep)]">
        <div className="flex items-center justify-between border-b border-line-faint px-4 py-3">
          <h2 className="text-sm font-medium text-title">History{filterProject ? ` — ${projectName(filterProject)}` : ''}</h2>
          <button onClick={onClose} aria-label="Close history">
            <X className="size-4 text-muted" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {grouped.size === 0 && <p className="px-4 py-6 text-center text-[13px] text-subtle">Nothing else here.</p>}
          {[...grouped.entries()].map(([project, list]) => (
            <div key={project}>
              <div className="flex items-center gap-2 px-4 pb-1 pt-3 text-[11px] uppercase tracking-wider text-subtle">
                <span className="size-2 rounded-full" style={{ background: projectColor(project, homeProject) }} />
                {projectName(project)}
              </div>
              {list.map((a) => (
                <button key={a.ref} className="flex w-full items-center gap-2.5 px-4 py-2 text-left hover:bg-surface-faint" onClick={() => onOpen(a.ref)}>
                  <LiveAgentAvatar agent={a} className="size-7 rounded-lg" />
                  <span className="min-w-0 flex-1 truncate text-[13px] text-secondary">{a.title || a.name}</span>
                </button>
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// TabsLayoutPreview reads ['agents']/['projects'] like the rest of the app
// (seedTabsLayout has put the organic project and its agents there), and
// keeps which are open, which is active, the drawer and the right panel's
// width and tab as its own state — the same way a person would get there, by
// closing and reopening tabs, not through the URL.
export function TabsLayoutPreview() {
  const agentsQuery = useQuery<T.Agent[]>({ queryKey: ['agents'], queryFn: api.agents });
  const projectsQuery = useQuery<T.Project[]>({ queryKey: ['projects'], queryFn: api.projects });
  const projects = projectsQuery.data ?? EMPTY_PROJECTS;

  const agentsByRef = useMemo(() => {
    const map = new Map<string, T.Agent>();
    for (const a of agentsQuery.data ?? []) map.set(a.ref, a);
    const ab = projects.find((p) => p.name === PROJECT);
    const org = projects.find((p) => p.name === 'organic');
    if (ab) map.set(AB_LEAD_REF, leadAgentFrom(ab, { project: PROJECT, ref: AB_LEAD_REF, started: true, chat: 'running' }));
    if (org) map.set(ORGANIC_LEAD_REF, leadAgentFrom(org, { project: 'organic', ref: ORGANIC_LEAD_REF, started: true, chat: 'running' }));
    map.set(HOME_LEAD_REF, homeAgentFrom({ project: T.HomeProject, ref: HOME_LEAD_REF, started: true, chat: 'ready' }));
    return map;
  }, [agentsQuery.data, projects]);

  const [open, setOpen] = useState<string[]>(INITIAL_OPEN);
  const [active, setActive] = useState(DEFAULT_ACTIVE);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerProject, setDrawerProject] = useState<string | undefined>(undefined);
  const [panel, setPanel] = useState<PanelKind>('diff');
  const [chatWidth, setChatWidth] = useState(480);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'h') {
        e.preventDefault();
        setDrawerOpen((v) => !v);
        setDrawerProject(undefined);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  function closeTab(ref: string) {
    setOpen((prev) => {
      const next = prev.filter((r) => r !== ref);
      if (active === ref) {
        const i = prev.indexOf(ref);
        setActive(next[Math.min(i, next.length - 1)] ?? next[0] ?? '');
      }
      return next;
    });
  }

  function reorder(from: string, to: string) {
    setOpen((prev) => {
      const fromIndex = prev.indexOf(from);
      const toIndex = prev.indexOf(to);
      if (fromIndex === -1 || toIndex === -1) return prev;
      const next = [...prev];
      next.splice(fromIndex, 1);
      next.splice(toIndex, 0, from);
      return next;
    });
  }

  function openTab(ref: string) {
    setOpen((prev) => (prev.includes(ref) ? prev : [...prev, ref]));
    setActive(ref);
    setDrawerOpen(false);
  }

  function startResize(e: ReactPointerEvent) {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = chatWidth;
    function move(ev: PointerEvent) {
      setChatWidth(Math.min(760, Math.max(360, startWidth + (ev.clientX - startX))));
    }
    function up() {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    }
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  const activeAgent = agentsByRef.get(active);

  return (
    <div className="flex h-screen w-screen" data-preview-layout="tabs">
      <IconRail
        projects={projects}
        drawerOpen={drawerOpen}
        onToggleDrawer={() => {
          setDrawerOpen((v) => !v);
          setDrawerProject(undefined);
        }}
        onFilterProject={(name) => {
          setDrawerProject(name);
          setDrawerOpen(true);
        }}
        onOpenHome={() => openTab(HOME_LEAD_REF)}
        homeProject={T.HomeProject}
      />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <TabStrip refs={open} agents={agentsByRef} active={active} onActivate={setActive} onClose={closeTab} onReorder={reorder} homeProject={T.HomeProject} />
        {activeAgent ? (
          <Workspace agent={activeAgent} panel={panel} onPanel={setPanel} chatWidth={chatWidth} onResizeStart={startResize} />
        ) : (
          <div className="flex flex-1 items-center justify-center text-[13px] text-subtle">No tabs open — ⌘⇧H for history.</div>
        )}
      </div>
      {drawerOpen && (
        <HistoryDrawer
          agents={[...agentsByRef.values()].filter((a) => a.ref !== HOME_LEAD_REF)}
          openRefs={open}
          filterProject={drawerProject}
          projects={projects}
          homeProject={T.HomeProject}
          onClose={() => setDrawerOpen(false)}
          onOpen={openTab}
        />
      )}
    </div>
  );
}
