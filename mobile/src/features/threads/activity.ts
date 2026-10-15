// What a chat is doing now, in one status line, for the list of chats and a
// chat's header: the live states #303 brought to the phone (the tool call it
// runs, thinking, writing, a permission to give, with how long its turn has
// taken), and otherwise how the rail sums the agent up. A port of the
// desktop's renderer/lib/chatActivity.ts and agentStatus.ts's chatLabel,
// with the same messages, minus what the phone doesn't show (memory holds).
import type * as T from '../../shared/api.ts';
import { formatTime, t } from '../../shared/i18n.ts';
import { isActive, pendingPermissions } from './thread.ts';

export type StatusTone = 'urgent' | 'error' | 'warning' | 'live' | 'muted';

export interface ChatStatus {
  text: string;
  tone: StatusTone;
  live: boolean;
  since?: string;
  command?: string;
}

const fileName = (path: string) => path.split('/').filter(Boolean).at(-1) ?? path;

// workLabel is what a work row says about a tool call, a thought or a
// permission: while it happens, and once it's done.
export function workLabel(it: T.ChatItem): string {
  if (it.kind === 'thought') return isActive(it) ? t('chat.work.thinking') : t('chat.work.thoughtDone');
  if (it.kind === 'permission' && it.permission) {
    const perm = it.permission;
    const option = perm.options.find((o) => o.id === perm.outcome);
    const outcome = perm.outcome === 'cancelled' ? 'cancelled' : option?.kind.startsWith('reject') ? 'denied' : 'allowed';
    return t('chat.work.permission', { outcome, title: perm.title });
  }
  if (it.kind === 'subagent') return it.subagent?.task || it.subagent?.name || t('chat.activity.subagent');
  const tool = it.tool;
  if (!tool) return t('chat.work.working');
  if (!isActive(it)) return tool.kind === 'execute' && tool.command ? tool.command : tool.title || tool.name || t('chat.work.toolCall');
  const path = tool.paths?.[0];
  switch (tool.kind) {
    case 'execute':
      return t('chat.work.running', { what: tool.command || tool.title });
    case 'edit':
      return path ? t('chat.work.editing', { name: fileName(path) }) : tool.title;
    case 'read':
      return path ? t('chat.work.reading', { name: fileName(path) }) : tool.title;
    case 'search':
      return t('chat.work.searching', { what: tool.title });
    case 'fetch':
      return t('chat.work.fetching', { what: tool.title });
  }
  return tool.title || tool.name || t('chat.work.working');
}

// liveStatus is what the chat is doing now, or undefined when it isn't.
export function liveStatus(thread: T.ChatThread | undefined): ChatStatus | undefined {
  if (!thread) return undefined;
  const { session } = thread;
  if (session.state === 'starting') return { text: session.detail || t('chat.activity.starting'), tone: 'live', live: true };
  const asks = pendingPermissions(thread);
  if (asks.length > 0) {
    const title = asks[0].permission?.title;
    return { text: title ? t('chat.activity.permissionFor', { title }) : t('chat.activity.permission'), tone: 'urgent', live: true };
  }
  if (thread.items.some((it) => it.kind === 'compaction' && it.compaction?.state === 'running')) return { text: t('chat.activity.compacting'), tone: 'live', live: true };
  const head = thread.items.findLast((it) => it.kind === 'user');
  const running = !!head && !head.result;
  if (running || session.state === 'running') {
    const since = running ? head.createdAt : session.turnStartedAt;
    const body = running ? thread.items.filter((it) => it.turn === head.id && !it.parent && !it.hidden) : [];
    // Tool calls can run side by side: the one that started last is news.
    const active = body.findLast(isActive);
    if (active?.kind === 'subagent') {
      const task = active.subagent?.task || active.subagent?.name;
      return { text: task ? t('chat.activity.subagentOn', { task }) : t('chat.activity.subagent'), tone: 'live', live: true, since };
    }
    if (active?.kind === 'tool') {
      const command = active.tool?.kind === 'execute' ? active.tool.command || active.tool.title : undefined;
      return command ? { text: t('chat.activity.running'), tone: 'live', live: true, since, command } : { text: workLabel(active), tone: 'live', live: true, since };
    }
    const last = body.at(-1);
    if (last?.kind === 'assistant' && last.streaming) return { text: t('chat.activity.writing'), tone: 'live', live: true, since };
    return { text: t('chat.activity.thinking'), tone: 'live', live: true, since };
  }
  const background = session.background ?? [];
  if (background.length > 0 && !session.turnStartedAt) return { text: t('chat.activity.background', { tasks: background.join(', ') }), tone: 'live', live: true };
  return undefined;
}

// agentLabel is how the rail sums an agent up when its chat isn't doing
// anything it can see (agentStatus.ts's chatLabel).
export function agentLabel(agent: T.Agent): { text: string; tone: StatusTone } {
  if (agent.state === 'initializing' || agent.state === 'queued') return { text: t('agent.status.initializing'), tone: 'live' };
  if (agent.chat === 'waiting') return { text: t('agent.status.needsYou'), tone: 'urgent' };
  if (agent.state === 'incomplete') return { text: t('agent.status.needsAttention'), tone: 'error' };
  if (agent.state === 'missing') return { text: t('agent.status.missing'), tone: 'error' };
  if (agent.chat === 'running' && agent.stalledSince) return { text: t('agent.status.stalled'), tone: 'error' };
  if (agent.chat === 'running') return { text: t('agent.status.working'), tone: 'live' };
  if (agent.state === 'running' && agent.chat === 'ready' && (agent.background?.length ?? 0) > 0) return { text: t('agent.status.awaiting'), tone: 'live' };
  if (agent.state === 'running' && agent.chat === 'error') return { text: t('agent.status.chatStopped'), tone: 'error' };
  if (agent.outcome === 'resuming' && agent.state !== 'paused') return { text: t('agent.status.resuming'), tone: 'live' };
  if (agent.state === 'running' && agent.outcome === 'limited') {
    return agent.outcomeUntil
      ? { text: t('agent.status.limited', { time: formatTime(agent.outcomeUntil, { hour: '2-digit', minute: '2-digit' }) }), tone: 'warning' }
      : { text: t('agent.status.limitedNoTime'), tone: 'warning' };
  }
  if (agent.state === 'running' && agent.chat !== 'starting') {
    if (agent.outcome === 'asking') return { text: t('agent.status.asking'), tone: 'urgent' };
    if (agent.outcome === 'failed') return { text: t('agent.status.failed'), tone: 'error' };
    if (agent.outcome === 'done') return { text: t('agent.status.done'), tone: 'muted' };
  }
  if (agent.state === 'running') return { text: agent.chat === 'starting' ? t('agent.status.starting') : t('agent.status.idle'), tone: 'muted' };
  if (agent.state === 'paused') return { text: t('agent.status.paused'), tone: 'muted' };
  return { text: t('agent.status.stopped'), tone: 'muted' };
}

// agentStatus is an agent's status line: what its chat is doing, when it is
// doing something the thread shows, and the rail's word otherwise.
export function agentStatus(agent: T.Agent, thread: T.ChatThread | undefined): ChatStatus {
  const label = agentLabel(agent);
  const live = agent.state === 'running' && !agent.stalledSince && label.tone !== 'warning' ? liveStatus(thread) : undefined;
  return live ?? { ...label, live: label.tone === 'live' || label.tone === 'urgent' };
}

// leadStatus is the same for a project's chat, which has no machine.
export function leadStatus(chat: string | undefined, thread: T.ChatThread | undefined): ChatStatus {
  const live = chat && chat !== 'off' ? liveStatus(thread) : undefined;
  if (live) return live;
  if (chat === 'waiting') return { text: t('web.phone.needsYou'), tone: 'urgent', live: true };
  if (chat === 'running') return { text: t('web.phone.working'), tone: 'live', live: true };
  if (chat === 'starting') return { text: t('chat.activity.starting'), tone: 'live', live: true };
  if (chat === 'error') return { text: t('common.error'), tone: 'error', live: false };
  return { text: chat === 'off' || !chat ? t('web.phone.notStarted') : t('web.phone.idle'), tone: 'muted', live: false };
}

// The order of the list, after the rail's: what needs you first, then what
// works, then the rest.
export function rank(agent: T.Agent): number {
  const tone = agentLabel(agent).tone;
  return tone === 'urgent' ? 0 : tone === 'error' ? 1 : tone === 'live' ? 2 : tone === 'warning' ? 3 : 4;
}

// isWaiting says a question waits on the user: the lead couldn't answer it
// and passed it on. Credentials are only ever given on the computer.
export const isWaiting = (q: T.Question) => q.status === 'escalated' && (!q.kind || q.kind === 'decision');
