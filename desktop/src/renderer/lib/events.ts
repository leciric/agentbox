// Keeps the query cache in step with the daemon's event stream, and collects
// job logs as they stream in.
import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import type { ConnectionState } from '../../preload';
import * as T from '../../shared/api';
import { applyChatEvent, resetChatEvents } from './chat';

const listeners = new Set<() => void>();
const mediaListeners = new Set<(item: T.MediaItem) => void>();
const logs = new Map<string, string[]>();
const noLines: string[] = [];
let connection: ConnectionState = { state: 'connecting' };

function notify(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useConnection(): ConnectionState {
  return useSyncExternalStore(subscribe, () => connection);
}

export function useJobLog(id: string | undefined): string[] {
  return useSyncExternalStore(subscribe, () => (id ? (logs.get(id) ?? noLines) : noLines));
}

// onMedia calls fn for every media item added or removed, from any agent.
export function onMedia(fn: (item: T.MediaItem) => void): () => void {
  mediaListeners.add(fn);
  return () => {
    mediaListeners.delete(fn);
  };
}

// addFetchedLog merges a log fetched from the API with the lines that have
// arrived as events. Lines are kept by position, so a line that arrives both
// ways shows once. (slice, not spread, keeps the gaps of lines still to come.)
export function addFetchedLog(id: string, text: string): void {
  const fetched = text === '' ? [] : text.replace(/\n$/, '').split('\n');
  const merged = (logs.get(id) ?? []).slice();
  fetched.forEach((line, i) => (merged[i] = line));
  logs.set(id, merged);
  notify();
}

const newestFirst = (a: { createdAt: string }, b: { createdAt: string }) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime();

function upsert<V extends { id: string; createdAt: string }>(list: V[] | undefined, value: V): V[] | undefined {
  if (!list) return list;
  return [value, ...list.filter((v) => v.id !== value.id)].sort(newestFirst);
}

export function connectEvents(queryClient: QueryClient): void {
  window.agentbox.onEvent((raw) => {
    const event = raw as T.Event;
    switch (event.type) {
      case T.EventChat:
        applyChatEvent(queryClient, event.data as T.ChatEvent);
        break;
      case T.EventAgentEvent: {
        // One of a project's agents reported something. It goes at the head of
        // that project's list, which is what the chat's agent threads read.
        const agentEvent = event.data as T.AgentEvent;
        queryClient.setQueryData<T.AgentEvent[]>(['agentEvents', agentEvent.project], (events) =>
          events ? [agentEvent, ...events.filter((e) => e.id !== agentEvent.id)] : events,
        );
        break;
      }
      case T.EventQuestion: {
        // A question was asked, escalated or answered. The thread that shows it
        // reads the question itself, so it can offer to answer one still waiting.
        const question = event.data as T.Question;
        void queryClient.invalidateQueries({ queryKey: ['questions', question.project] });
        break;
      }
      case T.EventTheme:
        // The desktop this machine runs changed its theme, or the setting
        // that decides whether to follow it did. The query holds it; the
        // hook on it (useHostTheme) is what restyles the window.
        queryClient.setQueryData(['theme'], event.data as T.Theme);
        break;
      case T.EventUpdate:
        // The daily update check found something, or was turned on or off.
        queryClient.setQueryData(['update'], event.data as T.UpdateStatus);
        break;
      case T.EventUsage:
        queryClient.setQueryData(['usage'], event.data as T.Usage);
        break;
      case T.EventProject:
        void queryClient.invalidateQueries({ queryKey: ['projects'] });
        // The project's notes change with it: the lead writes them too.
        void queryClient.invalidateQueries({ queryKey: ['notes'] });
        // A change with no project named is the list itself: a section made,
        // renamed or deleted, or the projects reordered (D79).
        void queryClient.invalidateQueries({ queryKey: ['sections'] });
        break;
      case T.EventPulls: {
        // The daemon re-read GitHub behind an answer it had already given,
        // and something moved. Refetching here is what lets both views poll
        // slowly without going stale.
        const { project } = event.data as T.PullsChange;
        void queryClient.invalidateQueries({ queryKey: ['pulls', project] });
        void queryClient.invalidateQueries({ queryKey: ['fleet', project] });
        break;
      }
      case T.EventAgent: {
        const change = event.data as T.AgentChange;
        const agents = queryClient.getQueryData<T.Agent[]>(['agents']);
        if (!change.removed && agents?.some((a) => a.ref === change.ref)) {
          queryClient.setQueryData<T.Agent[]>(
            ['agents'],
            agents.map((a) => (a.ref === change.ref ? { ...a, state: change.state, ip: change.ip ?? '' } : a)),
          );
        } else {
          void queryClient.invalidateQueries({ queryKey: ['agents'] });
        }
        if (change.state !== 'running') {
          void queryClient.invalidateQueries({ queryKey: ['browser', change.ref] });
          void queryClient.invalidateQueries({ queryKey: ['android', change.ref] });
        }
        break;
      }
      case T.EventJob: {
        const job = event.data as T.Job;
        queryClient.setQueryData(['job', job.id], job);
        queryClient.setQueryData<T.Job[]>(['jobs'], (jobs) => upsert(jobs, job));
        if (job.status !== T.JobRunning) {
          for (const key of ['agents', 'projects', 'snapshots', 'base', 'diff', 'image', 'setup', 'browser']) {
            void queryClient.invalidateQueries({ queryKey: [key] });
          }
        }
        break;
      }
      case T.EventJobLog: {
        const { job, n, line } = event.data as T.JobLogLine;
        const lines = (logs.get(job) ?? []).slice();
        lines[n] = line;
        logs.set(job, lines);
        notify();
        break;
      }
      case T.EventMedia: {
        const item = event.data as T.MediaItem;
        queryClient.setQueryData<T.MediaItem[]>(['media', item.agent], (items) =>
          item.removed ? items?.filter((i) => i.id !== item.id) : upsert(items, item),
        );
        if (item.kind === 'recording') void queryClient.invalidateQueries({ queryKey: ['recording', item.agent] });
        for (const fn of mediaListeners) fn(item);
        break;
      }
    }
  });

  const setConnection = (state: ConnectionState) => {
    const reconnected = state.state === 'connected' && connection.state !== 'connected';
    connection = state;
    notify();
    if (reconnected) {
      // A restarted daemon numbers chat events from the start again.
      resetChatEvents();
      void queryClient.invalidateQueries();
    }
  };
  window.agentbox.onConnection(setConnection);
  void window.agentbox.connection().then(setConnection);
}
