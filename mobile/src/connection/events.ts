// The daemon's events, live: its server-sent stream at /api/v1/events, the
// one the web phone app follows (desktop/src/renderer/web/bridge.ts), read
// with react-native-sse since React Native has no EventSource, and with the
// token in a header, which a browser's EventSource can't send.
//
// It is followed again from scratch when it can't be trusted, as the web
// app's reconnect.ts does: after an error, a little later each time (up to
// 15 s); when it went silent though it pings every 15 s (?ping=1), as a
// tunnel that dropped without a word leaves it; and when the app comes back
// to the foreground after a while, since the phone may have cut it while
// asleep. Each time it opens again everything is read again, so nothing sent
// meanwhile is lost. t3code's equivalents are packages/client-runtime's
// session state and apps/mobile/src/connection/app-state-wakeups.ts.
import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import { AppState } from 'react-native';
import EventSource from 'react-native-sse';
import * as T from '../shared/api.ts';
import { applyChatEvent } from '../features/threads/thread.ts';
import { session } from './session.ts';

export type Connection = 'connecting' | 'connected' | 'disconnected';

const types = [T.EventAgent, T.EventProject, T.EventChat, T.EventQuestion, T.EventPulls, T.EventReview, T.EventAgentEvent, T.EventMedia, T.EventLAN, 'ping'] as const;
type Type = (typeof types)[number];

const silentAfter = 45_000;
const awayFor = 15_000;
const maxBackoff = 15_000;

let state: Connection = 'connecting';
const listeners = new Set<() => void>();
function setState(next: Connection) {
  if (next === state) return;
  state = next;
  for (const fn of listeners) fn();
}

export function useConnection(): Connection {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => state,
    () => state,
  );
}

let source: EventSource<Type> | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;
let failures = 0;
let lastHeard = Date.now();
let client: QueryClient | undefined;

// apply is what an event does to the queries the screens read.
export function apply(queryClient: QueryClient, type: string, data: unknown): void {
  switch (type) {
    case T.EventChat:
      applyChatEvent(queryClient, data as T.ChatEvent);
      break;
    case T.EventAgent: {
      const change = data as T.AgentChange;
      const agents = queryClient.getQueryData<T.Agent[]>(['agents']);
      if (!change.removed && agents?.some((a) => a.ref === change.ref)) {
        queryClient.setQueryData<T.Agent[]>(['agents'], agents.map((a) => (a.ref === change.ref ? { ...a, state: change.state } : a)));
      } else void queryClient.invalidateQueries({ queryKey: ['agents'] });
      break;
    }
    case T.EventProject:
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      break;
    case T.EventQuestion:
      void queryClient.invalidateQueries({ queryKey: ['questions', (data as T.Question).project] });
      break;
    case T.EventPulls:
      void queryClient.invalidateQueries({ queryKey: ['pulls', (data as T.PullsChange).project] });
      void queryClient.invalidateQueries({ queryKey: ['review'] });
      break;
    case T.EventReview:
    case T.EventAgentEvent:
      void queryClient.invalidateQueries({ queryKey: ['review'] });
      break;
    case T.EventMedia:
      void queryClient.invalidateQueries({ queryKey: ['media'] });
      break;
  }
}

function follow() {
  source?.removeAllEventListeners();
  source?.close();
  source = undefined;
  if (timer) clearTimeout(timer);
  timer = undefined;
  const s = session();
  if (!s || !client) return;
  const queryClient = client;
  if (state !== 'disconnected') setState('connecting');
  lastHeard = Date.now();
  const es = new EventSource<Type>(`${s.origin}/api/v1/events?ping=1`, {
    headers: { Authorization: `Bearer ${s.token}` },
    // Never ends a long-lived stream on its own; followed again by us.
    pollingInterval: 0,
  });
  source = es;
  es.addEventListener('open', () => {
    failures = 0;
    lastHeard = Date.now();
    setState('connected');
    // What was sent while it was away: read everything again.
    void queryClient.invalidateQueries();
  });
  es.addEventListener('error', () => {
    if (source !== es) return;
    setState('disconnected');
    es.close();
    const delay = Math.min(1000 * 2 ** failures, maxBackoff);
    failures++;
    timer = setTimeout(follow, delay);
  });
  for (const type of types) {
    es.addEventListener(type, (ev) => {
      lastHeard = Date.now();
      if (type === 'ping' || !('data' in ev) || !ev.data) return;
      try {
        // The stream's data is a T.Event: its type, time and data.
        apply(queryClient, type, (JSON.parse(ev.data) as T.Event).data);
      } catch {
        // not an event we understand
      }
    });
  }
}

let started = false;

// connect follows the paired daemon's events into queryClient, from now on.
export function connect(queryClient: QueryClient): () => void {
  client = queryClient;
  follow();
  if (started) return stop;
  started = true;
  let hiddenAt: number | undefined;
  AppState.addEventListener('change', (next) => {
    if (next !== 'active') {
      hiddenAt ??= Date.now();
      return;
    }
    const away = hiddenAt !== undefined && Date.now() - hiddenAt > awayFor;
    hiddenAt = undefined;
    if (away || state !== 'connected' || Date.now() - lastHeard > silentAfter) reconnectNow();
  });
  setInterval(() => {
    if (source && state === 'connected' && AppState.currentState === 'active' && Date.now() - lastHeard > silentAfter) follow();
  }, 5_000);
  return stop;
}

function stop() {
  source?.removeAllEventListeners();
  source?.close();
  source = undefined;
  if (timer) clearTimeout(timer);
  timer = undefined;
  client = undefined;
}

// reconnectNow follows the stream again at once (the bar's "Retry now").
export function reconnectNow(): void {
  failures = 0;
  follow();
}
