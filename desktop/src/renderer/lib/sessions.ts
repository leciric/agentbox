// The sessions of a project's chat (internal/state/leadsessions.go): each a
// lead conversation of its own on one topic, as many as the user opens, none
// once they've all been closed. The chat shows one at a time, its tabs above
// it and the sidebar listing them under the project; which one this computer
// last had open is kept here, in localStorage, per project, and so is when
// each was last seen, for its unread dot.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

// sessionRef is api.ts's, which this can't import: the ref a session's chat is
// known by.
const sessionRef = (project: string, session: string) => `${project}/${session === 'main' ? 'lead' : `lead.${session}`}`;

const currentKey = 'agentbox.sessions.current';
const seenKey = 'agentbox.sessions.seen';

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
let seen = readMap(seenKey);
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

// sessionColor is the colour a chat of sessions shows for s (LeadSession.Color,
// styles.css' [data-session-color]): none while the chat has one session, for
// a session from before colours, or for no session.
export function sessionColor(sessions: readonly T.LeadSession[], s: T.LeadSession | undefined): string | undefined {
  return sessions.length >= 2 && s?.color ? s.color : undefined;
}

// isUnread says a session has a message newer than when it was last seen.
// One never seen counts from when it was opened, so a new session isn't
// unread for being new.
export function isUnread(s: T.LeadSession, seenAt: string | undefined): boolean {
  if (!s.last) return false;
  const since = new Date(seenAt ?? s.createdAt).getTime();
  return new Date(s.last.at).getTime() > since;
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

// useSeenAt is when each session was last seen, by ref.
export function useSeenAt(): Record<string, string> {
  return useSyncExternalStore(subscribe, () => seen);
}

// markSessionSeen records that a session's chat was on screen now.
export function markSessionSeen(project: string, session: string, at = new Date()): void {
  seen = { ...seen, [sessionRef(project, session)]: at.toISOString() };
  writeMap(seenKey, seen);
  notify();
}

// For the tests: start over from what s holds.
export function resetSessionsForTest(s: Store): void {
  current = readMap(currentKey, s);
  seen = readMap(seenKey, s);
  notify();
}
