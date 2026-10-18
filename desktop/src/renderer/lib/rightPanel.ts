// UI v2's right-hand panel (components/v2/RightPanel.tsx): what is pure about
// it, and its state.
//
// One header button opens it, closed by default, or ⌘D; beside the chat while
// the chat keeps 440px, a drawer over it otherwise. It always shows one of the
// project's agents (or its repository, the lead's checkout), picked at its
// top, in one of four views on a fixed switch: Desktop, Terminal, Files and
// Evidence (its media and Hatch pages, newest first). The rest, Browser,
// Timeline, and the project's Pull requests and Settings, open from its ⋯.
// There are no tabs and nothing to choose first: it remembers the view each
// agent was last shown in, and the agent each project's lead last looked at.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api.ts';
import { isLeadRef } from './pages.ts';
import type { ProjectSection } from './tabs.ts';

export type PanelView = 'desktop' | 'terminal' | 'files' | 'evidence' | 'browser' | 'timeline' | 'pulls' | 'schedules' | 'settings';
export const panelViews: PanelView[] = ['desktop', 'terminal', 'files', 'evidence', 'browser', 'timeline', 'pulls', 'schedules', 'settings'];
// The switch's four, always there, in this order; the rest are in the ⋯.
export const switchViews: PanelView[] = ['desktop', 'terminal', 'files', 'evidence'];
export const moreViews: PanelView[] = ['browser', 'timeline', 'pulls', 'schedules', 'settings'];

// The project's own views: its pull requests, its scheduled tasks and its
// settings, the same whoever is picked.
export type WideView = 'pulls' | 'schedules' | 'settings';
export const projectWide = (view: PanelView): view is WideView => view === 'pulls' || view === 'schedules' || view === 'settings';

// The views that show a machine: an agent's display, its shell, its dev server.
export const needsMachine = (view: PanelView) => view === 'desktop' || view === 'terminal' || view === 'browser';

// The views only an agent has: the lead does no work of its own to show as a
// timeline (its project's day is the Today page).
export const agentsOnly = (view: PanelView) => view === 'timeline';

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

export const whoseRef = (w: Whose) => (w.kind === 'lead' ? w.ref : w.kind === 'agent' ? w.agent.ref : pastRef(w.past.ref));
export const leadOf = (project: string) => `${project}/lead`;
export const projectOf = (ref: string) => unPast(ref).split('/')[0];

// hasView says whether whose has the view: the lead and a past agent have no
// machine, so no desktop, terminal or browser, and the lead no timeline;
// everyone has files and evidence, and the project's own views.
export function hasView(w: Whose, view: PanelView): boolean {
  if (projectWide(view)) return true;
  if (w.kind === 'lead') return !needsMachine(view) && !agentsOnly(view);
  return w.kind === 'agent' || !needsMachine(view);
}

// defaultView is what whose opens on before it was shown in any: an agent's
// desktop, the repository's files, and what a past agent left.
export const defaultView = (w: Whose): PanelView => (w.kind === 'agent' ? 'desktop' : w.kind === 'lead' ? 'files' : 'evidence');

// Desk is what an agent's computer is doing, in one word, for the glyph on
// its sidebar row, its page and its hover card: live (its desktop driven in
// the last minutes), on, or asleep (stopped or paused: it finished, and its
// machine sleeps until it is reviewed).
export type Desk = 'live' | 'on' | 'asleep';
export const deskOf = (agent: Pick<T.Agent, 'state'>, inUse: boolean): Desk => (machineOf(agent) === 'asleep' ? 'asleep' : inUse ? 'live' : 'on');

// resolveWhose finds ref among the project's agents and past agents; the
// lead for its own ref, and undefined when it is gone.
export function resolveWhose(ref: string, project: string, agents: T.Agent[], past: T.ArchivedAgent[]): Whose | undefined {
  if (ref === leadOf(project) || ref === project) return { kind: 'lead', ref: leadOf(project) };
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

// pickable is the agents the picker lists for a project, under its
// repository: those with a machine, newest first.
export function pickable(project: string, agents: T.Agent[]): T.Agent[] {
  return agents.filter((a) => a.project === project && a.name !== 'lead').toSorted((a, b) => b.createdAt.localeCompare(a.createdAt));
}

// mostRecent is the project's agent that did something last: one using its
// desktop now (live, refs) first, as the lane of desktops shows it, then one
// at work, then by when it last did anything (the fleet's lastActive), then
// the newest.
export function mostRecent(project: string, agents: T.Agent[], fleet: Pick<T.FleetAgent, 'ref' | 'busy' | 'lastActive'>[] = [], live: readonly string[] = []): T.Agent | undefined {
  const of = new Map(fleet.map((f) => [f.ref, f]));
  const at = (a: T.Agent) => of.get(a.ref)?.lastActive ?? a.createdAt;
  const rank = (a: T.Agent) => (live.includes(a.ref) ? 2 : 0) + (of.get(a.ref)?.busy ? 1 : 0);
  return pickable(project, agents).toSorted((a, b) => rank(b) - rank(a) || at(b).localeCompare(at(a)))[0];
}

// panelBox is how wide the panel is, and whether it sits beside the chat or
// over it as a drawer: beside from besideMin of window up, while the chat
// keeps chatMin; a drawer otherwise. At 1024px beside, the sidebar and the
// panel left the chat about 280px (UX test, finding 8).
export const besideMin = 1100;
export const chatMin = 440;
export const panelMin = 340;
export const panelDefault = 560;
// panelWant is how wide chat's panel would be: the one width beside a lead's
// chat; beside an agent's own, its desktop is what you came for, so half the
// room until you drag it.
export const panelWant = (state: Pick<PanelState, 'width' | 'agentWidth'>, chat: string, room: number): number => (isLeadRef(chat) ? state.width : (state.agentWidth ?? Math.round(room / 2)));
export function panelBox(want: number, room: number, windowWidth: number): { width: number; drawer: boolean } {
  if (windowWidth < besideMin || room - chatMin < panelMin) return { width: Math.round(Math.min(windowWidth * 0.92, Math.max(want, 420), windowWidth - 24)), drawer: true };
  return { width: Math.round(Math.max(panelMin, Math.min(want, room - chatMin))), drawer: false };
}

// A chat's panel: open or not, whose it shows when that was picked in this
// chat (an agent's chat shows its own agent until another is picked; the
// lead's shows the project's last), and the project's own view over it while
// one is open from the ⋯. focus is a media item to show first in Evidence,
// for this session only.
export interface ChatPanel {
  open: boolean;
  target?: string;
  wide?: WideView;
  focus?: string;
}
const closedPanel: ChatPanel = { open: false };

// The panel's state, all remembered but the chats' wide and focus: each chat's own panel, keyed
// by its ref, the view each agent (by ref) was last shown in, the agent each
// project's panel last showed, its width beside a lead's chat and beside an
// agent's (unset: half the room), and the Settings view's section.
export interface PanelState {
  width: number;
  agentWidth?: number;
  section: ProjectSection;
  chats: Record<string, ChatPanel>;
  views: Record<string, PanelView>;
  last: Record<string, string>;
}

const storageKey = 'agentbox.ui.v2.panel';
const listeners = new Set<() => void>();
let current: PanelState | undefined;

const isView = (v: unknown): v is PanelView => panelViews.includes(v as PanelView);
const strings = (x: unknown): Record<string, string> => Object.fromEntries(Object.entries(x && typeof x === 'object' ? x : {}).filter(([, v]) => typeof v === 'string'));

function load(): PanelState {
  const base: PanelState = { width: panelDefault, section: 'repository', chats: {}, views: {}, last: {} };
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<PanelState>;
    const chats: Record<string, ChatPanel> = {};
    for (const [chat, p] of Object.entries(saved.chats ?? {})) {
      // A panel saved with tabs, before the switch, opens again on its own.
      chats[chat] = { open: p?.open === true, ...(typeof p?.target === 'string' ? { target: p.target } : {}) };
    }
    const views = Object.fromEntries(Object.entries(saved.views ?? {}).filter(([, v]) => isView(v)));
    return {
      ...base,
      ...(typeof saved.width === 'number' ? { width: saved.width } : {}),
      ...(typeof saved.agentWidth === 'number' ? { agentWidth: saved.agentWidth } : {}),
      chats,
      views,
      last: strings(saved.last),
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
    const { width, agentWidth, views, last } = current;
    const chats = Object.fromEntries(Object.entries(current.chats).map(([k, p]) => [k, bare(p)]));
    localStorage.setItem(storageKey, JSON.stringify({ width, agentWidth, chats, views, last }));
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

// chatPanel is chat's panel: closed until it is opened.
export const chatPanel = (state: PanelState, chat: string): ChatPanel => state.chats[chat] ?? closedPanel;

export function useChatPanel(chat: string): ChatPanel {
  return chatPanel(usePanel(), chat);
}

// panelTarget is whose chat's panel shows, as a ref, before it is checked
// against the agents there are (RightPanel falls back from one gone): in the
// lead's, the agent the project's panel last showed; in an agent's, the one
// picked there, else the agent itself.
export function panelTarget(state: PanelState, chat: string): string | undefined {
  if (isLeadRef(chat)) return state.last[projectOf(chat)];
  return state.chats[chat]?.target ?? chat;
}

// viewOf is the view whose ref is shown in.
export const viewOf = (state: PanelState, w: Whose): PanelView => {
  const v = state.views[whoseRef(w)];
  return v && hasView(w, v) ? v : defaultView(w);
};

// shown is what chat's panel shows while it is open, for the lane of
// desktops to leave out the one it has: whose ref and in which view, the
// view unset when whose was never shown in one.
export function shown(state: PanelState, chat: string): { target: string; view: PanelView | undefined } | undefined {
  const p = chatPanel(state, chat);
  const target = panelTarget(state, chat);
  if (!p.open || p.wide || !target) return undefined;
  return { target, view: state.views[target] };
}

// bare is a chat's panel without the project's own view or the item Evidence
// was opened on: what picking whose, or a view, leaves of it.
const bare = ({ open, target }: ChatPanel): ChatPanel => ({ open, ...(target ? { target } : {}) });

function update(chat: string, change: (p: ChatPanel) => ChatPanel, more: Partial<PanelState> = {}): void {
  const state = panelState();
  const before = chatPanel(state, chat);
  const after = change(before);
  if (after !== before || Object.keys(more).length) setPanel({ ...more, chats: { ...state.chats, [chat]: after } });
}

// setPanelOpen opens or closes chat's panel.
export function setPanelOpen(chat: string, open: boolean): void {
  update(chat, (p) => (p.open === open ? p : { ...p, open }));
}

// pickTarget shows ref in chat's panel: the picker. It's the project's last,
// for its lead's panel, and leaves the project's own view.
export function pickTarget(chat: string, ref: string): void {
  const state = panelState();
  update(chat, (p) => ({ ...bare(p), open: true, ...(isLeadRef(chat) ? {} : { target: ref }) }), { last: { ...state.last, [projectOf(chat)]: ref } });
}

// setView shows ref in view, remembered for it, in chat's panel: the
// switch, and the ⋯'s Browser and Timeline. A project's own view isn't
// whose: it goes over whoever is shown until another view is picked.
export function setView(chat: string, ref: string, view: PanelView, focus?: string): void {
  const state = panelState();
  if (projectWide(view)) {
    update(chat, (p) => ({ ...p, open: true, wide: view }));
    return;
  }
  update(chat, (p) => ({ ...bare(p), open: true, ...(focus ? { focus } : {}) }), { views: { ...state.views, [ref]: view } });
}

// openPanelOn opens chat's panel on view, of target when given (then picked
// there), else of whoever it shows: the project's own views need none.
export function openPanelOn(chat: string, view: PanelView, target?: string): void {
  if (target) pickTarget(chat, target);
  const ref = target ?? panelTarget(panelState(), chat);
  if (ref || projectWide(view)) setView(chat, ref ?? '', view);
  else setPanelOpen(chat, true);
}

// fitsBeside is whether the panel would sit beside the chat now, kept by
// RightPanelHost: opening an agent's chat opens its panel only then, rather
// than as a drawer over the chat you came to read.
let beside = true;
export const setFitsBeside = (fits: boolean) => void (beside = fits);
export const fitsBeside = () => beside;

// openAgentChat is opening an agent's chat: its panel opens on its Desktop,
// where there is room for it beside the chat.
export function openAgentChat(ref: string): void {
  if (!beside) return;
  const state = panelState();
  update(ref, () => ({ open: true, target: ref }), { views: { ...state.views, [ref]: 'desktop' }, last: { ...state.last, [projectOf(ref)]: ref } });
}

// chaptersOf is a recording's chapters: the titled steps of its agent's
// timeline it shows, the one under way as it started at 0:00, each at its
// second of the recording. One isn't chapters: none then.
export function chaptersOf(item: Pick<T.MediaItem, 'createdAt' | 'meta'>, steps: Pick<T.TimelineStep, 'title' | 'start' | 'end'>[]): { at: number; title: string }[] {
  const from = new Date(item.createdAt).getTime();
  const to = from + (item.meta?.duration ?? 0) * 1000;
  const out = steps.flatMap((s) => {
    const start = new Date(s.start).getTime();
    return s.title && start < to && new Date(s.end).getTime() > from ? [{ at: Math.max(0, start - from) / 1000, title: s.title }] : [];
  });
  return out.length > 1 ? out : [];
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

