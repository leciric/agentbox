// UI v2's right-hand panel (components/v2/RightPanel.tsx): what is pure about
// it, and its state.
//
// Modelled on T3Code's right panel: one header button opens it, closed by
// default, ⌘D (apps/web/src/components/chat/PanelLayoutControls.tsx, mod+d in
// packages/shared/src/keybindings.ts); beside the chat while the chat keeps
// 360px, a sheet over it below 1024px (hooks/usePreviewPanelInlineSize.ts,
// rightPanelLayout.ts). Its tabs sit in a rail of icons on its outer edge,
// and the agent whose they are is picked in its header: the chat in the
// middle's by default.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api.ts';
import type { ProjectSection } from './tabs.ts';

export type PanelTab = 'files' | 'desktop' | 'terminal' | 'browser' | 'pages' | 'media' | 'pulls' | 'settings';
export const panelTabs: PanelTab[] = ['files', 'desktop', 'terminal', 'browser', 'pages', 'media', 'pulls', 'settings'];

// The project's own tabs, the lead's page's tabs before: its pull requests
// and its settings, the same whoever is picked.
export const projectWide = (tab: PanelTab) => tab === 'pulls' || tab === 'settings';

// The tabs that show a machine: an agent's display, its shell, its dev server.
export const needsMachine = (tab: PanelTab) => tab === 'desktop' || tab === 'terminal' || tab === 'browser';

// A past agent (its machine was removed) is picked as "past:<ref>": what it
// left is read from its branch, its pages and its media.
const pastPrefix = 'past:';
export const pastRef = (ref: string) => `${pastPrefix}${ref}`;
export const isPastRef = (ref: string) => ref.startsWith(pastPrefix);
export const unPast = (ref: string) => (isPastRef(ref) ? ref.slice(pastPrefix.length) : ref);

// Machine is what an agent's machine is: up (or on its way), asleep (stopped
// or paused, its disk kept), or freed (a past agent's, given back).
export type Machine = 'running' | 'asleep' | 'freed';
export function machineOf(agent: Pick<T.Agent, 'state'> | undefined): Machine {
  if (!agent) return 'freed';
  return agent.state === 'stopped' || agent.state === 'paused' ? 'asleep' : 'running';
}

// Who the panel shows: the project's lead (its repository, on this computer,
// with no machine), a live agent, or a past one.
export type Whose = { kind: 'lead'; ref: string } | { kind: 'agent'; agent: T.Agent } | { kind: 'past'; past: T.ArchivedAgent };

// An agent the picker lists, live or past.
export type AgentWhose = Exclude<Whose, { kind: 'lead' }>;

export const whoseRef = (w: Whose) => (w.kind === 'lead' ? w.ref : w.kind === 'agent' ? w.agent.ref : pastRef(w.past.ref));

// hasTab says whether whose has the tab: the lead and a past agent have no
// machine, so no desktop, terminal or browser; everyone has files, pages and
// media.
export function hasTab(w: Whose, tab: PanelTab): boolean {
  return w.kind === 'agent' || !needsMachine(tab);
}

// resolveWhose finds ref among the project's agents and past agents; the
// lead for its own ref, and undefined when it is gone.
export function resolveWhose(ref: string, project: string, agents: T.Agent[], past: T.ArchivedAgent[]): Whose | undefined {
  if (ref === `${project}/lead` || ref === project) return { kind: 'lead', ref: `${project}/lead` };
  if (isPastRef(ref)) {
    const p = past.find((a) => a.ref === unPast(ref));
    return p && { kind: 'past', past: p };
  }
  const a = agents.find((x) => x.ref === ref);
  if (a) return { kind: 'agent', agent: a };
  // An agent retired while it was picked is a past agent now.
  const p = past.find((x) => x.ref === ref);
  return p && { kind: 'past', past: p };
}

// pickable is who the picker lists for a project: its live agents, newest
// first, then its past ones unless only those with a machine are wanted.
export function pickable(project: string, agents: T.Agent[], past: T.ArchivedAgent[], withMachine: boolean): AgentWhose[] {
  const live: AgentWhose[] = agents
    .filter((a) => a.project === project && a.name !== 'lead')
    .toSorted((a, b) => b.createdAt.localeCompare(a.createdAt))
    .map((agent) => ({ kind: 'agent', agent }));
  if (withMachine) return live;
  const gone: AgentWhose[] = past
    .filter((a) => a.project === project && !agents.some((x) => x.ref === a.ref))
    .toSorted((a, b) => b.archivedAt.localeCompare(a.archivedAt))
    .map((p) => ({ kind: 'past', past: p }));
  return [...live, ...gone];
}

// panelBox is how wide the panel is, and whether it sits beside the chat or
// over it as a drawer: beside from 1024px of window up, while the chat keeps
// chatMin; a drawer otherwise.
export const chatMin = 360;
export const panelMin = 340;
export const panelDefault = 540;
// The project's settings and pull requests are pages laid out for a wide
// column: they open the panel at least this wide, room allowing.
export const panelWide = 860;
export const panelWant = (state: Pick<PanelState, 'tab' | 'width'>) => (projectWide(state.tab) ? Math.max(state.width, panelWide) : state.width);
export function panelBox(want: number, room: number, windowWidth: number): { width: number; drawer: boolean } {
  if (windowWidth < 1024 || room - chatMin < panelMin) return { width: Math.round(Math.min(windowWidth * 0.92, Math.max(want, 420), windowWidth - 24)), drawer: true };
  return { width: Math.round(Math.max(panelMin, Math.min(want, room - chatMin))), drawer: false };
}

// The panel's state. It opens closed; the tab, its width and the machine
// filter are remembered. picked is whose the panel shows, chosen for the chat
// in the middle (for): opening another chat follows that chat again.
export interface PanelState {
  open: boolean;
  tab: PanelTab;
  width: number;
  withMachine: boolean;
  picked: { for: string; ref: string } | null;
  section: ProjectSection; // the Settings tab's
}

const storageKey = 'agentbox.ui.v2.panel';
const listeners = new Set<() => void>();
let current: PanelState | undefined;

function load(): PanelState {
  const base: PanelState = { open: false, tab: 'files', width: panelDefault, withMachine: true, picked: null, section: 'repository' };
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<PanelState>;
    return {
      ...base,
      ...(saved.tab && panelTabs.includes(saved.tab) ? { tab: saved.tab } : {}),
      ...(typeof saved.width === 'number' ? { width: saved.width } : {}),
      ...(typeof saved.withMachine === 'boolean' ? { withMachine: saved.withMachine } : {}),
    };
  } catch {
    return base;
  }
}

export function panelState(): PanelState {
  current ??= load();
  return current;
}

export function setPanel(change: Partial<PanelState>): void {
  current = { ...panelState(), ...change };
  try {
    const { tab, width, withMachine } = current;
    localStorage.setItem(storageKey, JSON.stringify({ tab, width, withMachine }));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function usePanel(): PanelState {
  return useSyncExternalStore(subscribe, panelState);
}

// pickedFor is whose the panel shows beside the chat chat: what was picked
// for it, or the chat itself.
export function pickedFor(state: PanelState, chat: string): string {
  return state.picked?.for === chat ? state.picked.ref : chat;
}

// openPanelOn opens the panel on a tab, showing ref, beside chat.
export function openPanelOn(chat: string, tab: PanelTab, ref?: string): void {
  setPanel({ open: true, tab, ...(ref ? { picked: ref === chat ? null : { for: chat, ref } } : {}) });
}

// The ⋯ menu the chat in the middle had, given to the panel: its view sets
// it while it's on screen (usePanelMenu in components/v2/RightPanel.tsx).
let menu: unknown = null;
const menuListeners = new Set<() => void>();
export function setPanelMenu(items: unknown): void {
  menu = items;
  for (const l of menuListeners) l();
}
export function usePanelMenuItems<T>(): T | null {
  return useSyncExternalStore(
    (l) => {
      menuListeners.add(l);
      return () => menuListeners.delete(l);
    },
    () => menu as T | null,
  );
}

// browserPorts is the ports of the agent's own machine its Chromium has pages
// open on (localhost:5173/…), one each, for the Browser tab to offer.
export interface BrowserPort {
  port: string;
  path: string;
  title: string;
  url: string;
}
export function browserPorts(pages: Pick<T.BrowserPage, 'url' | 'title'>[]): BrowserPort[] {
  const out: BrowserPort[] = [];
  for (const page of pages) {
    let u: URL;
    try {
      u = new URL(page.url);
    } catch {
      continue;
    }
    if (!/^https?:$/.test(u.protocol) || !['localhost', '127.0.0.1', '0.0.0.0', '[::1]'].includes(u.hostname)) continue;
    const port = u.port || (u.protocol === 'https:' ? '443' : '80');
    if (out.some((p) => p.port === port)) continue;
    out.push({ port, path: `${u.pathname}${u.search}${u.hash}`, title: page.title, url: page.url });
  }
  return out;
}

// previewUrl is a port of an agent's machine through the daemon's preview
// proxy at addr (host:port), as OverviewTab's PreviewLink makes it.
export function previewUrl(agent: Pick<T.Agent, 'name' | 'project'>, port: string, addr: string, path = '/'): string {
  return `http://${port}.${agent.name}.${agent.project}.localhost:${addr.split(':').pop()}${path.startsWith('/') ? path : `/${path}`}`;
}
