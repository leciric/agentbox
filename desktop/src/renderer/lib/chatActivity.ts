// What a chat is doing right now, in one line: the phone's chat list and its
// chat header show it where the desktop has the whole timeline in view. It
// reads the same thread the timeline does (lib/chat.ts), so the two never
// disagree: thinking, a tool call running and its name, an answer being
// written, a permission to give, work left running in the background. A chat
// that isn't doing anything says how its last turn left it instead, in the
// rail's words (agentStatus.ts's chatLabel and the finish states it reads).
import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';
import { chatLabel, type StatusTone } from './agentStatus.ts';
import { isActive, isSilent, liveLabel, pendingPermissions } from './chat.ts';

export type Activity =
  | { kind: 'starting'; detail?: string }
  | { kind: 'permission'; title: string }
  | { kind: 'compacting' }
  | { kind: 'thinking'; since?: string }
  | { kind: 'tool'; label: string; command?: string; since?: string }
  | { kind: 'subagent'; label: string; since?: string }
  | { kind: 'writing'; since?: string }
  | { kind: 'background'; tasks: string[] };

// liveActivity is what the chat is doing now, or undefined when it isn't
// doing anything: its turn ended, or it never started.
export function liveActivity(thread: T.ChatThread | undefined): Activity | undefined {
  if (!thread) return undefined;
  const { session } = thread;
  if (session.state === 'starting') return { kind: 'starting', detail: session.detail };
  // A permission to give stops everything else: it is what the chat waits on.
  const asks = pendingPermissions(thread);
  if (asks.length > 0) return { kind: 'permission', title: asks[0].permission?.title ?? '' };
  if (thread.items.some((it) => it.kind === 'compaction' && it.compaction?.state === 'running')) return { kind: 'compacting' };

  // The running turn: the last one a message of yours heads, with no result yet.
  const head = thread.items.findLast((it) => it.kind === 'user');
  const running = !!head && !head.result;
  if (running || session.state === 'running') {
    const since = running ? head.createdAt : session.turnStartedAt;
    const body = running ? thread.items.filter((it) => it.turn === head.id && !it.parent && !isSilent(it)) : [];
    // Tool calls can run side by side: the one that started last is news.
    const active = body.findLast(isActive);
    if (active?.kind === 'subagent') return { kind: 'subagent', label: active.subagent?.task || active.subagent?.name || '', since };
    if (active?.kind === 'tool') {
      const command = active.tool?.kind === 'execute' ? active.tool.command || active.tool.title : undefined;
      return { kind: 'tool', label: liveLabel(active), command: command || undefined, since };
    }
    const last = body.at(-1);
    if (last?.kind === 'assistant' && last.streaming) return { kind: 'writing', since };
    return { kind: 'thinking', since };
  }
  const background = session.background ?? [];
  if (background.length > 0 && !session.turnStartedAt) return { kind: 'background', tasks: background };
  return undefined;
}

// A chat's status line: its words and tone, whether it is live (a pulsing
// dot), since when (an elapsed time), and the command it runs, set apart as
// code.
export interface ChatStatus {
  text: string;
  tone: StatusTone;
  live: boolean;
  since?: string;
  command?: string;
}

export function activityStatus(activity: Activity): ChatStatus {
  switch (activity.kind) {
    case 'starting':
      return { text: activity.detail || t('chat.activity.starting'), tone: 'live', live: true };
    case 'permission':
      return { text: activity.title ? t('chat.activity.permissionFor', { title: activity.title }) : t('chat.activity.permission'), tone: 'urgent', live: true };
    case 'compacting':
      return { text: t('chat.activity.compacting'), tone: 'live', live: true };
    case 'thinking':
      return { text: t('chat.activity.thinking'), tone: 'live', live: true, since: activity.since };
    case 'writing':
      return { text: t('chat.activity.writing'), tone: 'live', live: true, since: activity.since };
    case 'subagent':
      return { text: activity.label ? t('chat.activity.subagentOn', { task: activity.label }) : t('chat.activity.subagent'), tone: 'live', live: true, since: activity.since };
    case 'tool':
      return activity.command
        ? { text: t('chat.activity.running'), tone: 'live', live: true, since: activity.since, command: activity.command }
        : { text: activity.label, tone: 'live', live: true, since: activity.since };
    case 'background':
      return { text: t('chat.activity.background', { tasks: activity.tasks.join(', ') }), tone: 'live', live: true };
  }
}

// agentChatStatus is an agent's status line: what its chat is doing, when it
// is doing something it can see, and how the rail sums the agent up
// otherwise — a stalled turn, memory holding its commands back, a question,
// an error, done, idle, stopped.
export function agentChatStatus(agent: T.Agent, thread: T.ChatThread | undefined): ChatStatus {
  const label = chatLabel(agent);
  // The rail's word wins where it knows more than the thread: a turn that
  // stopped making progress, commands held for memory, a machine not up.
  const activity = agent.state === 'running' && !agent.stalledSince && label.tone !== 'warning' ? liveActivity(thread) : undefined;
  if (activity) return activityStatus(activity);
  return { ...label, live: label.tone === 'live' || label.tone === 'urgent' };
}

// leadChatStatus is the same for a project's chat, which has no machine and
// no finish states: the lead is told when its agents finish, rather than
// judged.
export function leadChatStatus(chat: string | undefined, thread: T.ChatThread | undefined): ChatStatus {
  const activity = chat && chat !== 'off' ? liveActivity(thread) : undefined;
  if (activity) return activityStatus(activity);
  if (chat === 'waiting') return { text: t('web.phone.needsYou'), tone: 'urgent', live: true };
  if (chat === 'running') return { text: t('web.phone.working'), tone: 'live', live: true };
  if (chat === 'starting') return { text: t('chat.activity.starting'), tone: 'live', live: true };
  if (chat === 'error') return { text: t('common.error'), tone: 'error', live: false };
  return { text: chat === 'off' || !chat ? t('web.phone.notStarted') : t('web.phone.idle'), tone: 'muted', live: false };
}

// lastReply is when the chat last said something to you: an answer, an
// error, or a turn ending. What you sent doesn't count, nor what its work
// was.
export function lastReply(thread: T.ChatThread | undefined): string | undefined {
  let latest: string | undefined;
  const later = (at: string | undefined) => {
    if (at && (!latest || Date.parse(at) > Date.parse(latest))) latest = at;
  };
  for (const it of thread?.items ?? []) {
    if (it.parent || isSilent(it)) continue;
    if (it.kind === 'assistant' || it.kind === 'error') later(it.updatedAt);
    else if (it.kind === 'user') later(it.result?.endedAt);
  }
  return latest;
}
