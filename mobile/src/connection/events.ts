// The daemon's events, live: its server-sent stream at /api/v1/events, the
// one the web phone app follows (desktop/src/renderer/web/bridge.ts), read
// with react-native-sse since React Native has no EventSource, and with the
// token in a header, which a browser's EventSource can't send.
//
// It is followed again from scratch when it can't be trusted: when it fails,
// never opens, goes silent, the app comes back to the foreground or the
// network comes back (reconnect.ts says when). Each time it opens again
// everything is read again, so nothing sent meanwhile is lost. t3code's
// equivalents are packages/client-runtime's session state and
// apps/mobile/src/connection/app-state-wakeups.ts.
import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import * as Network from 'expo-network';
import { AppState } from 'react-native';
import EventSource from 'react-native-sse';
import * as T from '../shared/api.ts';
import { applyChatEvent } from '../features/threads/thread.ts';
import * as P from './parse.ts';
import { reconnector, type Reconnector } from './reconnect.ts';
import { forget, session } from './session.ts';

export type Connection = 'connecting' | 'connected' | 'disconnected';

const types = [T.EventAgent, T.EventProject, T.EventChat, T.EventLeadSessions, T.EventLeadSessionStatus, T.EventQuestion, T.EventPulls, T.EventReview, T.EventAgentEvent, T.EventMedia, T.EventLAN, 'ping'] as const;
type Type = (typeof types)[number];

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
let client: QueryClient | undefined;
let again: Reconnector | undefined;

// scoped is a query's key for the project an event is about, or for every
// project when it doesn't say which.
function scoped(query: string, data: unknown): string[] {
  const project = (data as { project?: unknown } | null)?.project;
  return typeof project === 'string' && project ? [query, project] : [query];
}

// apply is what an event does to the queries the screens read. What it puts
// in them is made safe first (parse.ts), as the API's answers are.
export function apply(queryClient: QueryClient, type: string, data: unknown): void {
  switch (type) {
    case T.EventChat: {
      const ev = P.chatEvent(data);
      if (ev) applyChatEvent(queryClient, ev);
      break;
    }
    case T.EventAgent: {
      const change = P.agentChange(data);
      if (!change) break;
      const agents = queryClient.getQueryData<T.Agent[]>(['agents']);
      if (!change.removed && agents?.some((a) => a.ref === change.ref)) {
        queryClient.setQueryData<T.Agent[]>(['agents'], agents.map((a) => (a.ref === change.ref ? { ...a, state: change.state } : a)));
      } else void queryClient.invalidateQueries({ queryKey: ['agents'] });
      break;
    }
    case T.EventProject:
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      break;
    case T.EventLeadSessions:
    case T.EventSessionsSettled:
      void queryClient.invalidateQueries({ queryKey: scoped('leadSessions', data) });
      void queryClient.invalidateQueries({ queryKey: scoped('sessionOutput', data) });
      break;
    case T.EventLeadSessionStatus: {
      // Where each of a project's sessions stands, the whole open list.
      const change = P.leadSessionStatus(data);
      if (change) queryClient.setQueryData(['leadSessions', change.project], change.sessions);
      break;
    }
    case T.EventQuestion:
      void queryClient.invalidateQueries({ queryKey: scoped('questions', data) });
      break;
    case T.EventPulls:
      void queryClient.invalidateQueries({ queryKey: scoped('pulls', data) });
      void queryClient.invalidateQueries({ queryKey: scoped('sessionOutput', data) });
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

function close() {
  source?.removeAllEventListeners();
  source?.close();
  source = undefined;
}

function follow() {
  close();
  const s = session();
  if (!s || !client) return;
  const queryClient = client;
  if (state !== 'disconnected') setState('connecting');
  const es = new EventSource<Type>(`${s.origin}/api/v1/events?ping=1`, {
    headers: { Authorization: `Bearer ${s.token}` },
    // Never ends a long-lived stream on its own; followed again by us.
    pollingInterval: 0,
  });
  source = es;
  es.addEventListener('open', () => {
    again?.opened();
    setState('connected');
    // What was sent while it was away: read everything again.
    void queryClient.invalidateQueries();
  });
  es.addEventListener('error', (ev) => {
    if (source !== es) return;
    close();
    setState('disconnected');
    // Unpaired on the computer: the pairing is gone for good, so back to
    // pairing rather than reconnecting to no end, as a refused request does.
    if ('xhrStatus' in ev && ev.xhrStatus === 401) {
      again?.stopped();
      void forget();
      return;
    }
    again?.failed();
  });
  for (const type of types) {
    es.addEventListener(type, (ev) => {
      again?.heard();
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

// ended says the stream finished without an error, as when the daemon
// restarts: react-native-sse then only stops (pollingInterval 0), with no
// event, and the stream would look connected until it went silent.
const ended = () => (source as unknown as { _xhr?: XMLHttpRequest } | undefined)?._xhr?.readyState === XMLHttpRequest.DONE;

// connect follows the paired daemon's events into queryClient, from now on.
export function connect(queryClient: QueryClient): () => void {
  client = queryClient;
  again ??= reconnector({
    follow,
    // An attempt that never opened: the bar offers "Retry now" meanwhile.
    drop: () => {
      close();
      setState('disconnected');
    },
    connected: () => state === 'connected',
    ended,
    appState: AppState,
    network: (fn) => {
      Network.addNetworkStateListener(({ isConnected, type }) => fn({ online: isConnected !== false, kind: type }));
    },
  });
  again.started();
  again.now();
  return stop;
}

function stop() {
  again?.stopped();
  close();
  client = undefined;
}

// reconnectNow follows the stream again at once (the bar's "Retry now").
export function reconnectNow(): void {
  again?.now();
}
