// Cockpit mode (components/cockpit/CockpitView.tsx), experimental behind
// Settings.cockpitMode: a view for very wide screens of every project's lead
// in one feed beside a live wall of every agent, and one project's agent on a
// big stage. This is what it decides that isn't drawing: which messages the
// feed shows, which projects the filter keeps, what an agent shows on the
// stage, and which agent is on it.
import type * as T from '../../shared/api';

// Level is what the cockpit shows: every project, or one (focus).
export type Level = { kind: 'all' } | { kind: 'focus'; project: string };

// FeedRow is one message of a lead's conversation in the merged feed.
export type FeedRow = { project: string; ref: string; item: T.ChatItem };

// What the feed keeps of each lead's thread: its latest messages, enough to
// read what's going on without the feed being every conversation whole.
export const feedPerProject = 12;

// feedMessage is whether a chat item is one the feed shows: what you said to
// a lead and what it answered, with words in it, as the chat shows them.
export const feedMessage = (it: T.ChatItem) => (it.kind === 'user' || it.kind === 'assistant') && !it.parent && !it.hidden && !!it.text?.trim();

// feedOf merges the leads' threads into one feed, oldest first so the newest
// is at the bottom by the composer: each lead's last `per` messages.
export function feedOf(threads: readonly { project: string; ref: string; items: readonly T.ChatItem[] | undefined }[], per = feedPerProject): FeedRow[] {
  const rows: FeedRow[] = [];
  for (const th of threads) {
    const items = (th.items ?? []).filter(feedMessage).slice(-per);
    for (const item of items) rows.push({ project: th.project, ref: th.ref, item });
  }
  return rows.sort((a, b) => (a.item.createdAt < b.item.createdAt ? -1 : a.item.createdAt > b.item.createdAt ? 1 : 0));
}

// shownProjects is the filter's projects: every project not hidden, in the
// projects' order. Hiding is what's kept, so a project added later shows.
export const shownProjects = (all: readonly string[], hidden: ReadonlySet<string>): string[] => {
  const shown = all.filter((n) => !hidden.has(n));
  // A filter that hides all of them, as projects come and go, shows them all.
  return shown.length > 0 ? shown : [...all];
};

// toggleShown turns a project on or off in the filter, never the last one off.
export function toggleShown(all: readonly string[], hidden: ReadonlySet<string>, name: string): Set<string> {
  const next = new Set(hidden);
  if (next.has(name)) next.delete(name);
  else if (shownProjects(all, hidden).filter((n) => n !== name).length > 0) next.add(name);
  return next;
}

// onlyShown keeps just one project in the filter; on the one already alone,
// it shows them all again.
export function onlyShown(all: readonly string[], hidden: ReadonlySet<string>, name: string): Set<string> {
  const shown = shownProjects(all, hidden);
  if (shown.length === 1 && shown[0] === name) return new Set();
  return new Set(all.filter((n) => n !== name));
}

// Visual is what an agent shows on the stage: its live display (the desktop
// its browser is on), its Android emulator, its iOS Simulator, or with
// nothing visual its terminal and chat.
export type Visual = 'android' | 'ios' | 'display' | 'none';

// visualOf picks it. A phone the agent started is what it's working on more
// than the desktop behind it, so Android and iOS come first.
export function visualOf(s: { android?: Pick<T.AndroidStatus, 'running'>; ios?: Pick<T.IOSStatus, 'devices'>; display?: boolean }): Visual {
  if (s.android?.running) return 'android';
  if (s.ios?.devices?.some((d) => d.state === 'Booted')) return 'ios';
  if (s.display) return 'display';
  return 'none';
}

// The order the stage picks in when nobody chose: who needs you, then who
// failed, then who's working, then the rest.
const stageRank: Record<string, number> = { needs: 0, error: 1, working: 2, finished: 3, idle: 4, stopped: 5 };

// stagedOf is the agent on the stage: the one chosen while it's still there,
// else the first by stageRank, in the order given.
export function stagedOf<D extends { id: string; tone: string }>(desks: readonly D[], chosen: string | undefined): D | undefined {
  const picked = chosen ? desks.find((d) => d.id === chosen) : undefined;
  if (picked) return picked;
  let best: D | undefined;
  for (const d of desks) if (!best || (stageRank[d.tone] ?? 9) < (stageRank[best.tone] ?? 9)) best = d;
  return best;
}

// The project filter's hidden projects, kept in this window's localStorage:
// what one person on one screen wants to watch.
const hiddenKey = 'agentbox.cockpit.hidden';

export function loadHidden(): Set<string> {
  try {
    const raw = JSON.parse(localStorage.getItem(hiddenKey) ?? '[]');
    return new Set(Array.isArray(raw) ? raw.filter((n): n is string => typeof n === 'string') : []);
  } catch {
    return new Set();
  }
}

export function saveHidden(hidden: ReadonlySet<string>): void {
  try {
    localStorage.setItem(hiddenKey, JSON.stringify([...hidden]));
  } catch {
    // Without storage the filter lasts as long as the window.
  }
}

// fitWall is how many columns the wall of agents takes in a box w×h: the
// most that keeps every tile (a 16:10 screen and a `chrome`-high label) in
// view at the biggest size, or, when even `min`-wide tiles don't all fit,
// as many `min`-wide ones as a row holds, and the wall scrolls.
export function fitWall(n: number, w: number, h: number, gap = 8, chrome = 28, min = 300): number {
  if (n <= 0 || w <= 0) return 1;
  let best = 0;
  let bestW = 0;
  for (let cols = 1; cols <= n; cols++) {
    const tileW = (w - gap * (cols - 1)) / cols;
    if (tileW < min) break;
    const rows = Math.ceil(n / cols);
    const tileH = (tileW * 10) / 16 + chrome;
    if (rows * tileH + gap * (rows - 1) <= h && tileW > bestW) {
      best = cols;
      bestW = tileW;
    }
  }
  if (best) return best;
  return Math.max(1, Math.floor((w + gap) / (min + gap)));
}
