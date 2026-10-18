// Tours: the first-run tutorial and each big feature's short "What's new"
// tour, played by components/v2/Tour.tsx over the real UI. A tour is steps,
// each a spotlight on what its anchor finds on screen, or a card in the
// middle when nothing is (a narrow window, no projects yet), with a made-up
// example where the real thing may not be there to point at.
//
// Which tours were seen or dismissed, and the one being played and its step,
// are how this app is used on this computer, so they live in localStorage
// beside the layout (uiV2.ts): a tour cut short by quitting picks up where it
// was on the next launch.
//
// A feature declares its tour in `tours` with the version it shipped in.
// After an update, the tours not seen yet play once, one after another; a
// first run plays the welcome tour instead, which covers them all, and they
// count as seen. Help → What's new replays any of them.
import { useSyncExternalStore } from 'react';
import type { MessageKey } from './i18n';
import type { Arrival } from './uiV2.ts';

// Example is the made-up content a step shows beside its words.
export type Example = 'sidebar' | 'prompt' | 'mention' | 'cards' | 'review' | 'panel' | 'layout' | 'pick';

// Go is where a step needs the app to be: a lead's chat, or that chat with its
// panel open. Without a project, the step stays where it is.
export type Go = 'lead' | 'panel';

export type Step = {
  id: string;
  title: MessageKey;
  body: MessageKey;
  // anchor is the CSS selectors of what to spotlight, the first one visible.
  anchor?: string[];
  example?: Example;
  // exampleAlways shows the example even when the anchor is there: a review
  // that can't be opened for the tour, a prompt not yet written.
  exampleAlways?: boolean;
  go?: Go;
  // needsAgents skips the step while no agent exists: it points at what
  // only an agent's work puts on screen.
  needsAgents?: boolean;
};

export type Tour = {
  id: string;
  version: string;
  title: MessageKey;
  steps: Step[];
};

const S = {
  welcome: { id: 'welcome', title: 'tour.welcome.title', body: 'tour.welcome.body' },
  sidebar: {
    id: 'sidebar',
    title: 'tour.sidebar.title',
    body: 'tour.sidebar.body',
    anchor: ['[data-v2-rest] [data-v2-project]', '[data-v2-pinned] [data-v2-project]', '[data-v2-idle] [data-v2-project]', '[data-sidebar-v2] nav'],
    example: 'sidebar',
  },
  groups: {
    id: 'groups',
    title: 'tour.groups.title',
    body: 'tour.groups.body',
    anchor: ['[data-v2-pinned]', '[data-v2-rest]', '[data-sidebar-v2] nav'],
  },
  hide: {
    id: 'hide',
    title: 'tour.hide.title',
    body: 'tour.hide.body',
    anchor: ['[data-v2-hidden]', '[data-v2-idle]', '[data-sidebar-v2] nav'],
  },
  home: {
    id: 'home',
    title: 'tour.home.title',
    body: 'tour.home.body',
    anchor: ['[data-v2-home]'],
  },
  lead: {
    id: 'lead',
    title: 'tour.lead.title',
    body: 'tour.lead.body',
    anchor: ['[data-chat-composer]'],
    example: 'prompt',
    exampleAlways: true,
    go: 'lead',
  },
  mention: {
    id: 'mention',
    title: 'tour.mention.title',
    body: 'tour.mention.body',
    anchor: ['[data-project-menu]', '[data-chat-composer]'],
    example: 'mention',
    exampleAlways: true,
    go: 'lead',
  },
  cards: {
    id: 'cards',
    title: 'tour.cards.title',
    body: 'tour.cards.body',
    anchor: ['[data-chat-composer]'],
    example: 'cards',
    exampleAlways: true,
    go: 'lead',
  },
  reviewButton: {
    id: 'review-button',
    title: 'tour.reviewButton.title',
    body: 'tour.reviewButton.body',
    anchor: ['[data-v2-review-button]', '[data-v2-review-banner]', '[data-v2-review-all]'],
    needsAgents: true,
  },
  review: {
    id: 'review',
    title: 'tour.review.title',
    body: 'tour.review.body',
    example: 'review',
    exampleAlways: true,
  },
  panel: {
    id: 'panel',
    title: 'tour.panel.title',
    body: 'tour.panel.body',
    anchor: ['[data-panel-toggle]'],
    go: 'lead',
  },
  computer: {
    id: 'computer',
    title: 'tour.computer.title',
    body: 'tour.computer.body',
    anchor: ['[data-v2-agent] [data-desk]', '[data-v2-agents]', '[data-sidebar-v2] nav'],
    needsAgents: true,
  },
  switch: {
    id: 'switch',
    title: 'tour.switch.title',
    body: 'tour.switch.body',
    anchor: ['[data-panel-switch]', '[data-right-panel]'],
    example: 'panel',
    go: 'panel',
  },
  pick: {
    id: 'pick',
    title: 'tour.pick.title',
    body: 'tour.pick.body',
    anchor: ['[data-pick-element]', '[data-panel-switch]', '[data-right-panel]'],
    example: 'pick',
    exampleAlways: true,
    go: 'panel',
  },
  places: {
    id: 'places',
    title: 'tour.places.title',
    body: 'tour.places.body',
    anchor: ['[data-v2-foot]'],
  },
  classic: {
    id: 'classic',
    title: 'tour.classic.title',
    body: 'tour.classic.body',
    anchor: ['[data-v2-foot-item="settings"]'],
    example: 'layout',
    exampleAlways: true,
  },
  schedules: {
    id: 'schedules',
    title: 'tour.schedules.title',
    body: 'tour.schedules.body',
    anchor: ['[data-panel-more]', '[data-panel-toggle]'],
    go: 'panel',
  },
  help: {
    id: 'help',
    title: 'tour.help.title',
    body: 'tour.help.body',
    anchor: ['[data-v2-help]'],
  },
} satisfies Record<string, Step>;

// The release these tours came with: the next after 0.12.
const v013 = '0.13.0';
// Home took Overview in, and Media and Jobs moved to the sidebar's foot.
const v014 = '0.14.0';
// The panel became one agent at a time, in four views, with no tabs.
const v015 = '0.15.0';

export const welcomeTour: Tour = {
  id: 'welcome',
  version: v013,
  title: 'tour.welcome.name',
  steps: [S.welcome, S.sidebar, S.computer, S.home, S.groups, S.hide, S.lead, S.mention, S.cards, S.reviewButton, S.review, S.panel, S.switch, S.pick, S.places, S.classic, S.help],
};

// tours is What's new: each big feature's own short tour, oldest first.
export const tours: Tour[] = [
  { id: 'v2', version: v013, title: 'tour.v2.name', steps: [S.welcome, S.sidebar, S.lead, S.classic] },
  { id: 'review', version: v013, title: 'tour.reviewTour.name', steps: [S.reviewButton, S.review] },
  { id: 'panel', version: v013, title: 'tour.panelTour.name', steps: [S.panel, S.switch] },
  { id: 'groups', version: v013, title: 'tour.groupsTour.name', steps: [S.groups, S.hide] },
  { id: 'cross-project', version: v013, title: 'tour.crossTour.name', steps: [S.mention] },
  { id: 'places', version: v014, title: 'tour.placesTour.name', steps: [S.home, S.places] },
  { id: 'panel-switch', version: v015, title: 'tour.switchTour.name', steps: [S.computer, S.switch] },
  { id: 'pick', version: v015, title: 'tour.pickTour.name', steps: [S.pick] },
  { id: 'schedules', version: v015, title: 'tour.schedulesTour.name', steps: [S.schedules] },
];

export const tourById = (id: string): Tour | undefined => (id === welcomeTour.id ? welcomeTour : tours.find((t) => t.id === id));

// ——— What this computer remembers ———

// Playing is a run of one or more tours, played as one, and where it is.
export type Playing = { tours: string[]; step: number };

export type TourState = {
  seen: string[];
  dismissed: string[];
  playing: Playing | null;
};

const storageKey = 'agentbox.tours';
const empty: TourState = { seen: [], dismissed: [], playing: null };
const strings = (x: unknown): string[] => (Array.isArray(x) ? x.filter((s): s is string => typeof s === 'string') : []);

export function parseState(raw: string | null): TourState {
  try {
    const x = JSON.parse(raw ?? '{}') as Partial<TourState>;
    const p = x.playing;
    const playing =
      p && typeof p === 'object' && typeof p.step === 'number'
        ? { tours: strings(p.tours).filter((id) => tourById(id)), step: Math.max(0, Math.floor(p.step)) }
        : null;
    return { seen: strings(x.seen), dismissed: strings(x.dismissed), playing: playing && playing.tours.length > 0 ? playing : null };
  } catch {
    return empty;
  }
}

// stepsOf is a run's steps, in order, each with the tour it belongs to.
export function stepsOf(playing: Playing): { tour: Tour; step: Step }[] {
  return playing.tours.flatMap((id) => {
    const tour = tourById(id);
    return tour ? tour.steps.map((step) => ({ tour, step })) : [];
  });
}

// finish ends the run, its tours put on list.
const finish = (state: TourState, list: 'seen' | 'dismissed'): TourState => ({
  ...state,
  [list]: [...new Set([...state[list], ...(state.playing?.tours ?? [])])],
  playing: null,
});

// onLaunch is what a launch in v2 starts playing: the run cut short last time,
// the welcome tour for a first run or a move to v2, or the What's new tours
// not seen yet. A first run's What's new counts as seen: the welcome covers it.
export function onLaunch(state: TourState, arrival: Arrival): TourState {
  if (state.playing) return state;
  const all = [welcomeTour.id, ...tours.map((t) => t.id)];
  const known = new Set([...state.seen, ...state.dismissed]);
  if ((arrival === 'fresh' || arrival === 'moved') && !known.has(welcomeTour.id)) {
    return { ...state, seen: [...new Set([...state.seen, ...all.filter((id) => id !== welcomeTour.id)])], playing: { tours: [welcomeTour.id], step: 0 } };
  }
  if (arrival === 'classic') return state;
  const fresh = tours.filter((t) => !known.has(t.id)).map((t) => t.id);
  return fresh.length > 0 ? { ...state, playing: { tours: fresh, step: 0 } } : state;
}

// tourFirst is whether this launch plays a tour (one cut short, or one for
// this update): the release notes then wait for its last step's link instead
// of opening over it.
export const tourFirst = (state: TourState, arrival: Arrival): boolean => !!(state.playing || onLaunch(state, arrival).playing);

// onSwitchToV2 is choosing New in Settings: the welcome tour, once.
export function onSwitchToV2(state: TourState): TourState {
  if (state.playing || state.seen.includes(welcomeTour.id) || state.dismissed.includes(welcomeTour.id)) return state;
  return { ...state, seen: [...new Set([...state.seen, ...tours.map((t) => t.id)])], playing: { tours: [welcomeTour.id], step: 0 } };
}

export const play = (state: TourState, ids: string[]): TourState => ({ ...state, playing: { tours: ids, step: 0 } });

// goTo moves the run to step, finishing it, every tour in it seen, past its end.
export function goTo(state: TourState, step: number): TourState {
  if (!state.playing) return state;
  if (step >= stepsOf(state.playing).length) return finish(state, 'seen');
  return { ...state, playing: { ...state.playing, step: Math.max(0, step) } };
}

// skip ends the run, every tour in it dismissed.
export const skip = (state: TourState): TourState => finish(state, 'dismissed');

// ——— The store ———

const listeners = new Set<() => void>();
let current: TourState | undefined;

export function tourState(): TourState {
  if (!current) {
    try {
      current = parseState(localStorage.getItem(storageKey));
    } catch {
      current = empty;
    }
  }
  return current;
}

export function setTourState(next: TourState | ((s: TourState) => TourState)) {
  current = typeof next === 'function' ? next(tourState()) : next;
  try {
    localStorage.setItem(storageKey, JSON.stringify(current));
  } catch {
    // Kept for this session.
  }
  for (const fn of listeners) fn();
}

export function useTourState(): TourState {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    tourState,
  );
}

export const startTour = (...ids: string[]) => setTourState((s) => play(s, ids));

// ——— Where the card goes ———

export type Box = { left: number; top: number; width: number; height: number };

// placeCard puts the card of size beside target in a viewport: to its right,
// left, below or above, whichever fits first, and in the middle with no
// target. A narrow window, or a target too big to sit beside, docks it at
// the bottom (the top, for a target in the bottom half).
export function placeCard(target: Box | null, size: { width: number; height: number }, vp: { width: number; height: number }, gap = 14, margin = 12): { left: number; top: number } {
  const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(v, Math.max(lo, hi)));
  const centreX = (vp.width - size.width) / 2;
  if (!target) return { left: Math.max(margin, centreX), top: Math.max(margin, (vp.height - size.height) / 2) };
  const right = target.left + target.width;
  const bottom = target.top + target.height;
  const alongY = clamp(target.top, margin, vp.height - size.height - margin);
  const alongX = clamp(target.left + target.width / 2 - size.width / 2, margin, vp.width - size.width - margin);
  if (vp.width >= 640) {
    if (right + gap + size.width <= vp.width - margin) return { left: right + gap, top: alongY };
    if (target.left - gap - size.width >= margin) return { left: target.left - gap - size.width, top: alongY };
    if (bottom + gap + size.height <= vp.height - margin) return { left: alongX, top: bottom + gap };
    if (target.top - gap - size.height >= margin) return { left: alongX, top: target.top - gap - size.height };
  }
  const low = target.top + target.height / 2 > vp.height / 2;
  return { left: Math.max(margin, centreX), top: low ? margin : Math.max(margin, vp.height - size.height - margin) };
}
