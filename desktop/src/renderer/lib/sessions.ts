// The sessions of a project's chat (internal/state/leadsessions.go): each a
// lead conversation of its own on one topic, as many as the user opens, none
// once they've all been closed. The chat shows one at a time, its tabs above
// it and the sidebar listing them under the project; which one this computer
// last had open is kept here, in localStorage, per project. When each was last
// seen, for its unread dot, is the daemon's (LeadSession.seenAt), so every
// window and the phone agree: this window tells it (markSessionSeen), and
// holds what it saw until the daemon's list says so too.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

// sessionRef is api.ts's, which this can't import: the ref a session's chat is
// known by.
const sessionRef = (project: string, session: string) => `${project}/${session === 'main' ? 'lead' : `lead.${session}`}`;

const currentKey = 'agentbox.sessions.current';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

const storage = (): Store | undefined => {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
};

function readMap(key: string, s = storage()): Record<string, string> {
  try {
    const raw = s?.getItem(key);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function writeMap(key: string, value: Record<string, string>, s = storage()): void {
  try {
    s?.setItem(key, JSON.stringify(value));
  } catch {
    // a full or blocked store: the choice lasts this window only
  }
}

let current = readMap(currentKey);
// seenHere is when this window last showed each session, by ref.
let seenHere: Record<string, string> = {};
let sendSeen: ((project: string, session: string, at: string) => void) | undefined;
const listeners = new Set<() => void>();
const notify = () => {
  for (const l of listeners) l();
};
const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => listeners.delete(fn);
};

// byActivity is the sessions, the most recently active first.
export const byActivity = (sessions: readonly T.LeadSession[]): T.LeadSession[] =>
  [...sessions].sort((a, b) => new Date(b.activeAt).getTime() - new Date(a.activeAt).getTime());

// pickSession is the session a project's chat shows: the one chosen last, while
// it exists, and otherwise the most recently active; undefined with none.
export function pickSession(sessions: readonly T.LeadSession[], chosen: string | undefined): T.LeadSession | undefined {
  return sessions.find((s) => s.id === chosen) ?? byActivity(sessions)[0];
}

// withSession is sessions with s in it, replacing the one with its id: a
// session just made, before the list is read again.
export function withSession(sessions: readonly T.LeadSession[], s: T.LeadSession): T.LeadSession[] {
  return sessions.some((x) => x.id === s.id) ? sessions.map((x) => (x.id === s.id ? s : x)) : [...sessions, s];
}

// openSession is the session clicking a project opens: the one an agent's
// question waits in (the oldest question's, when several wait), and otherwise
// pickSession's. A question waits in its agent's session (Agent.leadSession),
// or, for an agent no session owns or whose session was closed or deleted, in
// the open one most recently active, as the daemon routes it
// (daemon/leadsessions.go). A
// question from an agent not in agents is left out: it's gone.
export function openSession(
  sessions: readonly T.LeadSession[],
  chosen: string | undefined,
  questions: readonly T.Question[],
  agents: readonly T.Agent[],
): T.LeadSession | undefined {
  const owners = new Map(agents.map((a) => [a.ref, a.leadSession ?? '']));
  const waiting = questions
    .filter((q) => (q.status === 'pending' || q.status === 'escalated') && owners.has(q.ref))
    .sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime());
  const oldest = waiting[0];
  if (oldest) {
    const owner = owners.get(oldest.ref);
    const s = sessions.find((x) => x.id === owner) ?? byActivity(sessions)[0];
    if (s) return s;
  }
  return pickSession(sessions, chosen);
}

// neighbour is the session after (or before, with step -1) id in the tabs'
// order, wrapping round.
export function neighbour(sessions: readonly T.LeadSession[], id: string | undefined, step: 1 | -1): T.LeadSession | undefined {
  if (sessions.length === 0) return undefined;
  const i = sessions.findIndex((s) => s.id === id);
  if (i < 0) return sessions[0];
  return sessions[(i + step + sessions.length) % sessions.length];
}

// afterClose is the session to show once id is closed: the one beside it.
export function afterClose(sessions: readonly T.LeadSession[], id: string): T.LeadSession | undefined {
  const i = sessions.findIndex((s) => s.id === id);
  const rest = sessions.filter((s) => s.id !== id);
  return rest[Math.min(Math.max(i, 0), rest.length - 1)];
}

// SESSION_COLORS are the colours a session can have (state.SessionColors),
// each with a [data-session-color] rule in styles.css.
export const SESSION_COLORS = ['rose', 'sky', 'mint', 'lilac', 'peach'] as const;

// sessionColor is the colour a chat of sessions shows for s (LeadSession.Color,
// styles.css' [data-session-color]): none while the chat has one session, for
// a session from before colours, or for no session.
export function sessionColor(sessions: readonly T.LeadSession[], s: T.LeadSession | undefined): string | undefined {
  return sessions.length >= 2 && s?.color ? s.color : undefined;
}

// isUnread says a session has a message newer than when it was last seen,
// anywhere (seenAt, the daemon's) or here (seenHere). One never seen counts
// from when it was opened, so a new session isn't unread for being new.
export function isUnread(s: T.LeadSession, seenHere?: string): boolean {
  if (!s.last) return false;
  const times = [s.seenAt, seenHere].filter((x): x is string => !!x).map((x) => new Date(x).getTime());
  const since = times.length ? Math.max(...times) : new Date(s.createdAt).getTime();
  return new Date(s.last.at).getTime() > since;
}

export type SessionStatus = 'needsYou' | 'failed' | 'working' | 'idle';

// sessionStatus is where a session stands (LeadSession.status, the daemon's
// daemon/sessionstatus.go): something waits on you in it, its chat stopped
// or stalled, its lead works, or none of these. A daemon too old to say is
// read from its chat's state.
export function sessionStatus(s: T.LeadSession): SessionStatus {
  const known: readonly string[] = ['needsYou', 'failed', 'working', 'idle'] satisfies SessionStatus[];
  if (s.status && known.includes(s.status)) return s.status as SessionStatus;
  if (s.chat === 'waiting') return 'needsYou';
  if (s.chat === 'error') return 'failed';
  return isWorking(s) ? 'working' : 'idle';
}

// needsYouCount is how many things wait on you in s; at least 1 when an old
// daemon only says its chat waits.
export const needsYouCount = (s: T.LeadSession): number => (sessionStatus(s) === 'needsYou' ? Math.max(1, s.needsYou ?? 0) : 0);

export interface SessionAlert {
  // needsYou: a permission request now waits in it (a question notifies on
  // its own); failed: its chat stopped or stalled; done: its lead's turn
  // ended with none of its agents still working.
  kind: 'needsYou' | 'failed' | 'done';
  session: T.LeadSession;
}

// sessionAlerts is what changed between two lists of a project's sessions that
// is worth a notice. A session in only one of them is none: it was just
// opened, closed or first read.
export function sessionAlerts(prev: readonly T.LeadSession[] | undefined, next: readonly T.LeadSession[]): SessionAlert[] {
  if (!prev) return [];
  const before = new Map(prev.map((s) => [s.id, s]));
  const out: SessionAlert[] = [];
  for (const s of next) {
    const p = before.get(s.id);
    if (!p) continue;
    const was = sessionStatus(p);
    const now = sessionStatus(s);
    const asks = (x: T.LeadSession) => needsYouCount(x) - (x.questions ?? 0);
    if (asks(s) > asks(p)) out.push({ kind: 'needsYou', session: s });
    else if (now === 'failed' && was !== 'failed') out.push({ kind: 'failed', session: s });
    else if (was === 'working' && now === 'idle' && !s.agentsWorking) out.push({ kind: 'done', session: s });
  }
  return out;
}

// topicFrom is a new session's topic made from the message it starts with: its
// first line, cut at a word to fit a tab; undefined with no text.
export function topicFrom(text: string, max = 60): string | undefined {
  const line = text.trim().split('\n')[0].replace(/\s+/g, ' ').trim();
  if (!line) return undefined;
  if (line.length <= max) return line;
  const cut = line.slice(0, max);
  const space = cut.lastIndexOf(' ');
  return `${(space > max / 2 ? cut.slice(0, space) : cut).replace(/[\s,.;:]+$/, '')}…`;
}

// isWorking says a session's lead is in a turn.
export const isWorking = (s: T.LeadSession) => s.chat === 'running' || s.chat === 'starting' || s.chat === 'waiting';

// useChosenSession is the session id this computer last showed for project.
export function useChosenSession(project: string): string | undefined {
  return useSyncExternalStore(subscribe, () => current[project]);
}

export function chosenSession(project: string): string | undefined {
  return current[project];
}

// chooseSession shows a session of project's chat, and marks it seen.
export function chooseSession(project: string, session: string): void {
  if (current[project] !== session) {
    current = { ...current, [project]: session };
    writeMap(currentKey, current);
  }
  markSessionSeen(project, session);
}

// useSeenAt is when this window last showed each session, by ref.
export function useSeenAt(): Record<string, string> {
  return useSyncExternalStore(subscribe, () => seenHere);
}

// sendSeenWith is how markSessionSeen tells the daemon (api.seeLeadSession),
// which App sets.
export function sendSeenWith(fn: (project: string, session: string, at: string) => void): void {
  sendSeen = fn;
}

// markSessionSeen records that a session's chat was on screen now, here and
// in the daemon, for every other window and the phone.
export function markSessionSeen(project: string, session: string, at = new Date()): void {
  const iso = at.toISOString();
  seenHere = { ...seenHere, [sessionRef(project, session)]: iso };
  notify();
  sendSeen?.(project, session, iso);
}

// shownSessions counts, by ref, the chats on screen showing each session.
const shownSessions = new Map<string, number>();

// showingSession says a session's chat is on screen until the returned
// function is called: a notice about it then waits for the window to be in
// the background.
export function showingSession(project: string, session: string): () => void {
  const ref = sessionRef(project, session);
  shownSessions.set(ref, (shownSessions.get(ref) ?? 0) + 1);
  return () => {
    const n = (shownSessions.get(ref) ?? 1) - 1;
    if (n > 0) shownSessions.set(ref, n);
    else shownSessions.delete(ref);
  };
}

export const isSessionShown = (project: string, session: string): boolean => shownSessions.has(sessionRef(project, session));

// For the tests: start over from what s holds.
export function resetSessionsForTest(s: Store): void {
  current = readMap(currentKey, s);
  seenHere = {};
  notify();
}
