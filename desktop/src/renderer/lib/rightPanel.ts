// UI v2's right-hand panel (components/v2/RightPanel.tsx): what is pure about
// it, and its state.
//
// Modelled on T3Code's right panel: one header button opens it, closed by
// default, ⌘D (apps/web/src/components/chat/PanelLayoutControls.tsx, mod+d in
// packages/shared/src/keybindings.ts); beside the chat while the chat keeps
// 360px, a sheet over it below 1024px (hooks/usePreviewPanelInlineSize.ts,
// rightPanelLayout.ts). Its tabs run along its top, each chat its own
// (rightPanelStore.ts), and + opens a launcher of cards, one per kind
// (components/RightPanelTabs.tsx, RightPanelEmptyState). A tab of a kind that
// needs one asks whose it shows inside it, again as cards.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api.ts';
import { isLeadRef } from './pages.ts';
import type { ProjectSection } from './tabs.ts';

export type PanelKind = 'files' | 'desktop' | 'terminal' | 'browser' | 'pages' | 'media' | 'pulls' | 'settings';
export const panelKinds: PanelKind[] = ['files', 'desktop', 'terminal', 'browser', 'pages', 'media', 'pulls', 'settings'];

// The project's own tabs, the lead's page's tabs before: its pull requests
// and its settings, the same whoever is picked.
export const projectWide = (tab: PanelKind) => tab === 'pulls' || tab === 'settings';

// The tabs that show a machine: an agent's display, its shell, its dev server.
export const needsMachine = (tab: PanelKind) => tab === 'desktop' || tab === 'terminal' || tab === 'browser';

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
export function hasTab(w: Whose, tab: PanelKind): boolean {
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

// targets is whose a tab of kind asks to show, as cards: the repository (the
// lead's) and the agents for files, pages and media, past ones too unless
// only those with a machine are wanted; for the desktop, the terminal and the
// browser, only the agents with a machine. The chat in the middle's own comes
// first.
export function targets(kind: PanelKind, project: string, chat: string, agents: T.Agent[], past: T.ArchivedAgent[], withMachine: boolean): Whose[] {
  const list = pickable(project, agents, past, withMachine || needsMachine(kind));
  const own = list.findIndex((w) => w.kind === 'agent' && w.agent.ref === chat);
  if (own > 0) list.unshift(...list.splice(own, 1));
  return needsMachine(kind) ? list : [{ kind: 'lead', ref: `${project}/lead` }, ...list];
}

// panelBox is how wide the panel is, and whether it sits beside the chat or
// over it as a drawer: beside from 1024px of window up, while the chat keeps
// chatMin; a drawer otherwise. Every tab has the one width, as in T3Code
// (hooks/usePreviewPanelInlineSize.ts: one stored width, the column beside it
// kept at 360px).
export const chatMin = 360;
export const panelMin = 340;
export const panelDefault = 540;
export function panelBox(want: number, room: number, windowWidth: number): { width: number; drawer: boolean } {
  if (windowWidth < 1024 || room - chatMin < panelMin) return { width: Math.round(Math.min(windowWidth * 0.92, Math.max(want, 420), windowWidth - 24)), drawer: true };
  return { width: Math.round(Math.max(panelMin, Math.min(want, room - chatMin))), drawer: false };
}

// A tab of the panel, as T3Code's surfaces (apps/web/src/rightPanelStore.ts):
// kind is what it shows, null while it is a new tab's launcher, and target
// whose it is (a ref, as whoseRef makes it), unset until it is chosen inside
// the tab. The project's own tabs (projectWide) need none.
export interface PanelTab {
  id: string;
  kind: PanelKind | null;
  target?: string;
}

// needsTarget says whether a tab still has to ask whose it shows.
export const needsTarget = (tab: PanelTab) => !!tab.kind && !projectWide(tab.kind) && !tab.target;

// A chat's panel: open or not, its tabs in order, and the one shown.
export interface ChatPanel {
  open: boolean;
  tabs: PanelTab[];
  active: string | null;
}
const closedPanel: ChatPanel = { open: false, tabs: [], active: null };

// The panel's state, all remembered: each chat's own panel, keyed by its ref,
// the one width, the machine filter and the Settings tab's section.
export interface PanelState {
  width: number;
  withMachine: boolean;
  section: ProjectSection;
  chats: Record<string, ChatPanel>;
}

const storageKey = 'agentbox.ui.v2.panel';
const listeners = new Set<() => void>();
let current: PanelState | undefined;

const isTab = (t: unknown): t is PanelTab => {
  const x = t as PanelTab;
  return !!x && typeof x.id === 'string' && (x.kind === null || panelKinds.includes(x.kind)) && (x.target === undefined || typeof x.target === 'string');
};

function load(): PanelState {
  const base: PanelState = { width: panelDefault, withMachine: true, section: 'repository', chats: {} };
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<PanelState>;
    const chats: Record<string, ChatPanel> = {};
    for (const [chat, p] of Object.entries(saved.chats ?? {})) {
      const tabs = Array.isArray(p?.tabs) ? p.tabs.filter(isTab) : [];
      chats[chat] = { open: p.open === true && tabs.length > 0, tabs, active: tabs.some((t) => t.id === p.active) ? p.active : (tabs[0]?.id ?? null) };
    }
    return {
      ...base,
      ...(typeof saved.width === 'number' ? { width: saved.width } : {}),
      ...(typeof saved.withMachine === 'boolean' ? { withMachine: saved.withMachine } : {}),
      chats,
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
    const { width, withMachine, chats } = current;
    localStorage.setItem(storageKey, JSON.stringify({ width, withMachine, chats }));
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

// chatPanel is chat's panel: closed with no tabs until it is opened.
export const chatPanel = (state: PanelState, chat: string): ChatPanel => state.chats[chat] ?? closedPanel;

export function useChatPanel(chat: string): ChatPanel {
  return chatPanel(usePanel(), chat);
}

function update(chat: string, change: (p: ChatPanel) => ChatPanel): void {
  const state = panelState();
  const before = chatPanel(state, chat);
  const after = change(before);
  if (after !== before) setPanel({ chats: { ...state.chats, [chat]: after } });
}

let lastId = 0;
const newId = () => `${Date.now().toString(36)}${(++lastId).toString(36)}`;

// setPanelOpen opens or closes chat's panel; with no tabs it opens on the
// launcher.
export function setPanelOpen(chat: string, open: boolean): void {
  update(chat, (p) => (p.open === open ? p : { ...p, open }));
}

// addTab is the tab strip's +: a new tab, on the launcher.
export function addTab(chat: string): void {
  const tab: PanelTab = { id: newId(), kind: null };
  update(chat, (p) => ({ open: true, tabs: [...p.tabs, tab], active: tab.id }));
}

// chooseKind is a launcher's card: it turns the new tab id into kind, or
// adds a tab of kind when the launcher is the empty panel's (no id).
export function chooseKind(chat: string, id: string | null, kind: PanelKind, target?: string): void {
  update(chat, (p) => {
    if (id && p.tabs.some((t) => t.id === id)) return { ...p, open: true, tabs: p.tabs.map((t) => (t.id === id ? { id, kind, ...(target ? { target } : {}) } : t)), active: id };
    const tab: PanelTab = { id: newId(), kind, ...(target ? { target } : {}) };
    return { open: true, tabs: [...p.tabs, tab], active: tab.id };
  });
}

// launcherTarget is whose a tab the launcher opens shows: in an agent's own
// page the machine kinds and Files open straight on that agent (the tab's
// picker switches), and in the lead's, or for any other kind, it asks.
export function launcherTarget(chat: string, kind: PanelKind): string | undefined {
  if (isLeadRef(chat) || projectWide(kind) || !(kind === 'files' || needsMachine(kind))) return undefined;
  return chat;
}

// chooseTarget is whose tab id shows: picked from its cards, or changed.
export function chooseTarget(chat: string, id: string, target: string): void {
  update(chat, (p) => ({ ...p, tabs: p.tabs.map((t) => (t.id === id ? { ...t, target } : t)) }));
}

export function activateTab(chat: string, id: string): void {
  update(chat, (p) => (p.active === id || !p.tabs.some((t) => t.id === id) ? p : { ...p, active: id }));
}

// closeTab closes a tab, showing its nearest neighbour that shows something
// (right first, then left, as T3Code's closeSurface does): not a new tab's
// launcher or a tab still asking whose it shows, unless only those are left.
// The panel closes with its last tab.
export function closeTab(chat: string, id: string): void {
  update(chat, (p) => {
    const index = p.tabs.findIndex((t) => t.id === id);
    if (index < 0) return p;
    const tabs = p.tabs.filter((t) => t.id !== id);
    const active = p.active === id ? (neighbour(tabs, index)?.id ?? null) : p.active;
    return { open: p.open && tabs.length > 0, tabs, active };
  });
}

// neighbour is the tab nearest index in tabs, once one was taken out there:
// the closest that shows something, right before left, else the right one.
function neighbour(tabs: PanelTab[], index: number): PanelTab | undefined {
  const shows = (t: PanelTab | undefined) => !!t?.kind && !needsTarget(t);
  for (let d = 0; d < tabs.length; d++) {
    if (shows(tabs[index + d])) return tabs[index + d];
    if (shows(tabs[index - 1 - d])) return tabs[index - 1 - d];
  }
  return tabs[Math.min(index, tabs.length - 1)];
}

// moveTab puts tab id at index toIndex, a tab dragged along the strip.
export function moveTab(chat: string, id: string, toIndex: number): void {
  update(chat, (p) => {
    const from = p.tabs.findIndex((t) => t.id === id);
    if (from < 0 || from === toIndex) return p;
    const tabs = [...p.tabs];
    tabs.splice(Math.max(0, Math.min(toIndex, tabs.length - 1)), 0, ...tabs.splice(from, 1));
    return { ...p, tabs };
  });
}

// openPanelOn opens chat's panel on a tab of kind: the one showing target
// already (any of the project's own kind), or a new one, which asks whose it
// is when no target is given.
export function openPanelOn(chat: string, kind: PanelKind, target?: string): void {
  const p = chatPanel(panelState(), chat);
  const there = p.tabs.find((t) => t.kind === kind && (projectWide(kind) || (target ? t.target === target : !t.target)));
  if (there) update(chat, (q) => ({ ...q, open: true, active: there.id }));
  else chooseKind(chat, null, kind, target);
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

// fitTabs is which tabs the strip shows and which go to its +N menu, from
// each tab's width and the room there is, room kept for the menu's button
// once any don't fit. The active tab is always shown, then the ones shown
// before (keep) for as long as they fit, then the rest in order: picking a
// tab from the menu takes the place of the last shown, and the others stay
// where they were. Shown tabs keep their order.
export function fitTabs(tabs: { id: string; width: number }[], active: string | null, room: number, more: number, keep: readonly string[] = []): { shown: string[]; hidden: string[] } {
  const all = tabs.map((t) => t.id);
  if (tabs.reduce((sum, t) => sum + t.width, 0) <= room) return { shown: all, hidden: [] };
  const byId = new Map(tabs.map((t) => [t.id, t]));
  const kept = keep.filter((id) => byId.has(id));
  const order = [...new Set([...(active && byId.has(active) ? [active] : []), ...kept, ...all])];
  const budget = room - more;
  const on = new Set<string>();
  let used = 0;
  for (const id of order) {
    const width = byId.get(id)!.width;
    // The active tab is shown even when it alone overflows; past the kept
    // ones, the first that doesn't fit ends it, so a narrow tab further on
    // doesn't jump the ones before it.
    if (used + width > budget && on.size) {
      if (kept.includes(id) || id === active) continue;
      break;
    }
    on.add(id);
    used += width;
  }
  return { shown: all.filter((id) => on.has(id)), hidden: all.filter((id) => !on.has(id)) };
}
