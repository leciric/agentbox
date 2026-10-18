// Keeps the query cache in step with the daemon's event stream, and collects
// job logs as they stream in.
import type { InfiniteData, QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import type { ConnectionState } from '../../preload';
import * as T from '../../shared/api.ts';
import { api } from './api.ts';
import { questionsKey, withQuestion } from './inboxPages.ts';
import { applyMediaEvent, setMediaUnseen } from './mediaPages.ts';
import { pastAgentsKey } from './pastAgents.ts';
import { applySeen } from './notifications.ts';
import { onChatItem as onPageCall } from './pages.ts';
import { applyChatEvent, resetChatEvents } from './chat.ts';
import { changesFiles, refreshFiles } from './files.ts';

const listeners = new Set<() => void>();
const mediaListeners = new Set<(item: T.MediaItem) => void>();
const notificationListeners = new Set<(n: T.Notification) => void>();
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

// seedConnection sets the connection for dev/preview.tsx, which has no event
// stream to say it.
export function seedConnection(state: ConnectionState): void {
  connection = state;
  notify();
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

// onNotification calls fn for every notification as the daemon makes it.
export function onNotification(fn: (n: T.Notification) => void): () => void {
  notificationListeners.add(fn);
  return () => {
    notificationListeners.delete(fn);
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
      case T.EventFiles: {
        // Files moved in a worktree whose tree is open: read again the folders
        // and files that did, and what changed (components/files).
        const change = event.data as T.FilesChange;
        for (const dir of change.dirs) void queryClient.invalidateQueries({ queryKey: ['fileTree', change.ref, dir] });
        for (const path of change.files) void queryClient.invalidateQueries({ queryKey: ['file', change.ref, path] });
        if (change.dirs.length > 0) void queryClient.invalidateQueries({ queryKey: ['files', change.ref] });
        void queryClient.invalidateQueries({ queryKey: ['fileStatus', change.ref] });
        break;
      }
      case T.EventChat: {
        const chat = event.data as T.ChatEvent;
        applyChatEvent(queryClient, chat);
        // A tool call that finished may have changed files the tree shows.
        if (chat.item && changesFiles(chat.item)) refreshFiles(queryClient, chat.agent, api.fileStatus);
        // A page published on Hatch joins the project's pages.
        if (chat.item) onPageCall(queryClient, chat.agent, chat.item, api.pages);
        break;
      }
      case T.EventChatCache: {
        // A project chat's cache card went up or down (see CacheCard).
        const cache = event.data as T.ChatCache;
        queryClient.setQueryData(['chatCache', cache.project], cache);
        break;
      }
      case T.EventAgentEvent: {
        // One of a project's agents reported something. It goes at the head of
        // that project's list, which the rail reads for when each last did.
        const agentEvent = event.data as T.AgentEvent;
        queryClient.setQueryData<T.AgentEvent[]>(['agentEvents', agentEvent.project], (events) =>
          events ? [agentEvent, ...events.filter((e) => e.id !== agentEvent.id)] : events,
        );
        // A finish puts the agent on "Ready to review".
        if (agentEvent.kind === T.AgentFinished) {
          void queryClient.invalidateQueries({ queryKey: ['review'] });
          void queryClient.invalidateQueries({ queryKey: ['agentReview'] });
        }
        break;
      }
      case T.EventSchedule: {
        // A schedule was made, changed or removed, or one of them ran.
        const change = event.data as T.ScheduleChange;
        void queryClient.invalidateQueries({ queryKey: ['schedules', change.project] });
        void queryClient.invalidateQueries({ queryKey: ['scheduleRuns', change.project] });
        break;
      }
      case T.EventQuestion: {
        // A question was asked, escalated or answered: the avatars wave while
        // one waits on you, and the credential cards show what it came to.
        const question = event.data as T.Question;
        void queryClient.invalidateQueries({ queryKey: ['questions', question.project] });
        break;
      }
      case T.EventConnector: {
        // A connector was added, changed or removed, or a sign-in finished.
        // Every list it can be in is refetched: its project's, and each of
        // the project's agents', which carry the project's connectors too —
        // and for an AgentBox-wide one (project ''), every list.
        const connector = event.data as T.Connector;
        for (const key of ['connectors', 'pages']) {
          void queryClient.invalidateQueries({
            queryKey: [key],
            predicate: (query) => connector.project === '' || String(query.queryKey[1]).split('/')[0] === connector.project,
          });
        }
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
      case T.EventDisk:
        // The disk guard: a disk came near its floor, reached it or has room
        // again, or it paused or resumed an agent.
        queryClient.setQueryData(['disk'], event.data as T.DiskGuard);
        break;
      case T.EventLAN:
        // Phones: turned on or off, one paired or revoked (Settings, Phone).
        void queryClient.invalidateQueries({ queryKey: ['lan'] });
        break;
      case T.EventProject:
        void queryClient.invalidateQueries({ queryKey: ['projects'] });
        // The project's notes change with it: the lead writes them too.
        void queryClient.invalidateQueries({ queryKey: ['notes'] });
        // A change with no project named is the list itself: a section made,
        // renamed or deleted, or the projects reordered (D79).
        void queryClient.invalidateQueries({ queryKey: ['sections'] });
        // And v2's sidebar's hidden projects.
        void queryClient.invalidateQueries({ queryKey: ['sidebarLayout'] });
        break;
      case T.EventPulls: {
        // The daemon re-read GitHub behind an answer it had already given,
        // and something moved. Refetching here is what lets both views poll
        // slowly without going stale.
        // A re-read that found nothing new still says it ended, so the tab
        // stops showing "refreshing"; the fleet has nothing to redraw.
        const { project, unchanged } = event.data as T.PullsChange;
        void queryClient.invalidateQueries({ queryKey: ['pulls', project] });
        if (!unchanged) {
          void queryClient.invalidateQueries({ queryKey: ['fleet', project] });
          // A pull request fixed brings its agent back to "Ready to review".
          void queryClient.invalidateQueries({ queryKey: ['review'] });
          void queryClient.invalidateQueries({ queryKey: ['agentReview'] });
        }
        break;
      }
      case T.EventGroup: {
        // A group of agents moved: made, one finished, a pick, a decision.
        const { id } = event.data as T.GroupChange;
        void queryClient.invalidateQueries({ queryKey: ['groups'] });
        void queryClient.invalidateQueries({ queryKey: ['group', id] });
        break;
      }
      case T.EventReview:
        // A verdict, from this window or another.
        void queryClient.invalidateQueries({ queryKey: ['review'] });
        void queryClient.invalidateQueries({ queryKey: ['agentReview'] });
        void queryClient.invalidateQueries({ queryKey: ['reviewed'] });
        break;
      case T.EventAgent: {
        const change = event.data as T.AgentChange;
        const agents = queryClient.getQueryData<T.Agent[]>(['agents']);
        if (!change.removed && agents?.some((a) => a.ref === change.ref)) {
          queryClient.setQueryData<T.Agent[]>(
            ['agents'],
            agents.map((a) => (a.ref === change.ref ? { ...a, state: change.state, ip: change.ip ?? '', memory: change.memory } : a)),
          );
        } else {
          void queryClient.invalidateQueries({ queryKey: ['agents'] });
        }
        // An agent whose machine was removed is a past agent now.
        if (change.removed) void queryClient.invalidateQueries({ queryKey: pastAgentsKey });
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
        applyMediaEvent(queryClient, item);
        break;
      }
      case T.EventNotification: {
        const n = event.data as T.Notification;
        if (n.kind === T.NotifyMedia) {
          // Only the Media views' unseen mark: no inbox item, no toast.
          if (n.media && !n.seen) {
            setMediaUnseen(queryClient, true, new Set([n.media.id]));
            void queryClient.invalidateQueries({ queryKey: ['mediaCounts'] });
          }
          break;
        }
        if (!n.seen) queryClient.setQueryData<InfiniteData<T.NotificationPage, string>>(questionsKey, (data) => withQuestion(data, n));
        for (const fn of notificationListeners) fn(n);
        break;
      }
      case T.EventNotificationsSeen:
        // Another window, or the Media view, marked some seen.
        applySeen(queryClient, event.data as T.SeeNotificationsRequest);
        void queryClient.invalidateQueries({ queryKey: questionsKey });
        break;
      case T.EventSnap:
        // agentbox snap took one: the composer (SnapComposer) opens on it.
        void queryClient.invalidateQueries({ queryKey: ['snaps'] });
        break;
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
