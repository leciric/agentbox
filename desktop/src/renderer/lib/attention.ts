// How loudly a project's row in UI v2's sidebar (components/v2/SidebarV2.tsx)
// calls for the user, in three levels: something needs you (an agent asks a
// question, a permission, a credential or a connector, or stopped on an
// error; or the lead's own chat waits on a permission), something finished
// that you haven't seen (agents ready to review since you last opened the
// project, the chat's unread kind of state), or nothing (working or idle,
// grey as before). The counts are agentCounts.ts's; when each project was
// last seen is kept here, in localStorage, as sessions.ts keeps a session's.
import { useRef, useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';
import type { Tally } from './agentCounts.ts';
import { chatLabel, isAsking } from './agentStatus.ts';
import type { Row } from './leadFirst.ts';

export type Level = 'needs' | 'finished';

export type Attention = {
  level?: Level;
  // How many things are at that level: the agents that need you (and the
  // lead, when its chat waits), or the agents that finished unseen.
  count: number;
};

// attentionOf is a project's level, from its tally, whether its lead's chat
// waits on the user, and how many of its agents finished unseen.
export function attentionOf(c: Tally, leadWaiting: boolean, finished: number): Attention {
  const needs = c.needs + c.error + (leadWaiting ? 1 : 0);
  if (needs > 0) return { level: 'needs', count: needs };
  if (finished > 0) return { level: 'finished', count: finished };
  return { count: 0 };
}

// The rank a level sorts by: what needs you first, then what finished.
const rank = (l: Level | undefined) => (l === 'needs' ? 0 : l === 'finished' ? 1 : 2);

// byAttention puts the projects that need you first, then those with
// something finished, keeping the order they came in within each.
export function byAttention<P extends { name: string }>(projects: readonly P[], needs: ReadonlySet<string>, finished: ReadonlySet<string>): P[] {
  const of = (p: P) => rank(needs.has(p.name) ? 'needs' : finished.has(p.name) ? 'finished' : undefined);
  return projects.map((p, i) => ({ p, i, r: of(p) })).sort((a, b) => a.r - b.r || a.i - b.i).map((x) => x.p);
}

// keepOnScreen is now, plus the project on screen when before had it: a
// project you opened keeps its place while you're on it, so clearing what
// it showed (opening it marks it seen; an ask answered) doesn't move the row
// out from under the pointer. It moves once you leave.
export function keepOnScreen(before: ReadonlySet<string>, now: ReadonlySet<string>, onScreen: string | undefined): ReadonlySet<string> {
  if (!onScreen || now.has(onScreen) || !before.has(onScreen)) return now;
  return new Set([...now, onScreen]);
}

// useKeepOnScreen is keepOnScreen across renders, from what it gave last.
export function useKeepOnScreen(now: ReadonlySet<string>, onScreen: string | undefined): ReadonlySet<string> {
  const last = useRef<ReadonlySet<string>>(new Set());
  const out = keepOnScreen(last.current, now, onScreen);
  last.current = out;
  return out;
}

// unseenFinished is the project's agents ready to review that finished
// (finishedAt, the review head's T.ReviewItem.finishedAt by ref) after seenAt:
// an agent that finishes again after you looked counts again. One the head
// doesn't list has no time to go by, and doesn't count.
export function unseenFinished(rows: readonly Row[], project: string, seenAt: string, finishedAt: ReadonlyMap<string, string>): string[] {
  const since = Date.parse(seenAt) || 0;
  return rows.filter((r) => r.agent.project === project && r.st === 'review' && (Date.parse(finishedAt.get(r.agent.ref) ?? '') || 0) > since).map((r) => r.agent.ref);
}

// The ask a row says in place of its lead's last message: who asks, and what.
export type Ask = { who: string; text: string };

// askOf is what the project's first agent that needs you asks: its question
// in its own words when it has one the user must answer (isAsking), else
// what its status says (a permission, an error). undefined when none does.
export function askOf(rows: readonly Row[], project: string, questions: readonly T.Question[] | undefined): Ask | undefined {
  const mine = rows.filter((r) => r.agent.project === project);
  const qs = (questions ?? []).filter((q) => isAsking([q], q.ref));
  for (const st of ['needs', 'error'] as const) {
    for (const r of mine.filter((x) => x.st === st)) {
      const q = qs.find((x) => x.ref === r.agent.ref);
      const who = r.agent.title || r.agent.name;
      if (q?.question.trim()) return { who, text: oneLine(q.question) };
      return { who, text: chatLabel(r.agent).text };
    }
  }
  return undefined;
}

const oneLine = (s: string) => s.replace(/\s+/g, ' ').trim();

// When each project was last on screen with the window focused. A project
// never seen goes by when this computer first ran this, so the agents
// waiting to review from before don't all light up at once.
const seenKey = 'agentbox.v2.projectSeen';
const sinceKey = 'agentbox.v2.projectSeenSince';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

const storage = (): Store | undefined => {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
};

function read(s: Store | undefined): { seen: Record<string, string>; since: string } {
  let seen: Record<string, string> = {};
  try {
    const parsed = JSON.parse(s?.getItem(seenKey) ?? '{}');
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) seen = parsed;
  } catch {
    // Starts over.
  }
  let since = '';
  try {
    since = s?.getItem(sinceKey) ?? '';
    if (!since) {
      since = new Date().toISOString();
      s?.setItem(sinceKey, since);
    }
  } catch {
    since = since || new Date().toISOString();
  }
  return { seen, since };
}

let store = storage();
let state = read(store);
const listeners = new Set<() => void>();
const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => listeners.delete(fn);
};

// useProjectSeen is when each project was last seen, and since when this
// computer has kept it (the time of a project never seen).
export function useProjectSeen(): { seen: Record<string, string>; since: string } {
  return useSyncExternalStore(subscribe, () => state);
}

// markProjectSeen records that project was on screen at at.
export function markProjectSeen(project: string, at = new Date()): void {
  state = { ...state, seen: { ...state.seen, [project]: at.toISOString() } };
  try {
    store?.setItem(seenKey, JSON.stringify(state.seen));
  } catch {
    // Kept for this run only.
  }
  for (const l of listeners) l();
}

// useWindowFocused is whether this window has the focus: a project on
// screen behind another app isn't seen.
export function useWindowFocused(): boolean {
  return useSyncExternalStore(
    (fn) => {
      window.addEventListener('focus', fn);
      window.addEventListener('blur', fn);
      return () => {
        window.removeEventListener('focus', fn);
        window.removeEventListener('blur', fn);
      };
    },
    () => document.hasFocus(),
  );
}

// For the tests: start over from what s holds.
export function resetProjectSeenForTest(s: Store): void {
  store = s;
  state = read(s);
  for (const l of listeners) l();
}
