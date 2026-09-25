// Each agent's chat: its thread in the query cache, kept current by the daemon's
// chat events, and the rows the timeline derives from it.
import type { QueryClient } from '@tanstack/react-query';
import { diffLines } from 'diff';
import type * as T from '../../shared/api';
import { api } from './api';

export const chatKey = (ref: string) => ['chat', ref];

// The latest chat events of each agent. A thread that was being fetched while
// they arrived applies the ones it doesn't include yet.
const recent = new Map<string, T.ChatEvent[]>();
const keepEvents = 4000;

// resetChatEvents forgets them, when the daemon or the environment changes and
// its events start counting again.
export function resetChatEvents(): void {
  recent.clear();
}

export async function fetchThread(ref: string): Promise<T.ChatThread> {
  const thread = await api.chat(ref);
  const next = advance(thread, recent.get(ref) ?? []);
  return next === 'gap' ? thread : next;
}

export function applyChatEvent(queryClient: QueryClient, ev: T.ChatEvent): void {
  const list = recent.get(ev.agent) ?? [];
  list.push(ev);
  if (list.length > keepEvents) list.splice(0, list.length - keepEvents);
  recent.set(ev.agent, list);

  if (ev.session) {
    const state = ev.session.state;
    queryClient.setQueryData<T.Agent[]>(['agents'], (agents) =>
      agents?.some((a) => a.ref === ev.agent && a.chat !== state) ? agents.map((a) => (a.ref === ev.agent ? { ...a, chat: state } : a)) : agents,
    );
  }
  const thread = queryClient.getQueryData<T.ChatThread>(chatKey(ev.agent));
  if (!thread) return;
  const next = advance(thread, [ev]);
  if (next === 'gap') void queryClient.invalidateQueries({ queryKey: chatKey(ev.agent) });
  else if (next !== thread) queryClient.setQueryData(chatKey(ev.agent), next);
}

// advance applies events in order. Events the thread already includes are
// skipped; a missing one means the thread has to be fetched again.
function advance(thread: T.ChatThread, events: T.ChatEvent[]): T.ChatThread | 'gap' {
  let next = thread;
  for (const ev of events) {
    if (ev.seq <= next.seq) continue;
    if (ev.seq !== next.seq + 1) return 'gap';
    next = applyEvent(next, ev);
  }
  return next;
}

function applyEvent(thread: T.ChatThread, ev: T.ChatEvent): T.ChatThread {
  const next = { ...thread, seq: ev.seq };
  if (ev.cleared) {
    next.items = [];
  } else if (ev.session) {
    next.session = ev.session;
  } else if (ev.item) {
    const i = indexOf(thread.items, ev.item.id);
    next.items = i < 0 ? [...thread.items, ev.item] : thread.items.with(i, ev.item);
  } else if (ev.append) {
    const i = indexOf(thread.items, ev.append.id);
    if (i >= 0) next.items = thread.items.with(i, { ...thread.items[i], text: (thread.items[i].text ?? '') + ev.append.text });
  }
  return next;
}

// indexOf searches from the end, where changes happen.
function indexOf(items: T.ChatItem[], id: string): number {
  for (let i = items.length - 1; i >= 0; i--) if (items[i].id === id) return i;
  return -1;
}

// The timeline, after t3code's: settled turns fold behind "Worked for …",
// consecutive tool calls become one summary row, and the running turn shows
// what's happening now.
export type Row =
  | { type: 'user'; key: string; item: T.ChatItem }
  | { type: 'assistant'; key: string; item: T.ChatItem; final: boolean }
  | { type: 'work'; key: string; items: T.ChatItem[]; live: boolean }
  | { type: 'fold'; key: string; turn: string; label: string; open: boolean }
  | { type: 'working'; key: string; since: string }
  | { type: 'thinking'; key: string }
  | { type: 'note'; key: string; item: T.ChatItem }
  | { type: 'subagent'; key: string; item: T.ChatItem; children: T.ChatItem[] }
  | { type: 'compaction'; key: string; item: T.ChatItem }
  | { type: 'changes'; key: string; files: ChangedFile[] };

interface Turn {
  id: string;
  user?: T.ChatItem;
  items: T.ChatItem[];
}

function turnsOf(items: T.ChatItem[]): Turn[] {
  const turns: Turn[] = [];
  const byId = new Map<string, Turn>();
  // Only a user message heads a turn. An aside — one sent while the turn was
  // already running — carries the turn it landed in, and joins that one.
  for (const it of items) {
    if (it.kind === 'user') {
      const turn = { id: it.id, user: it, items: [] };
      turns.push(turn);
      byId.set(it.id, turn);
      continue;
    }
    let turn = byId.get(it.turn);
    if (!turn) {
      turn = { id: it.turn || `loose:${it.id}`, items: [] };
      turns.push(turn);
      byId.set(turn.id, turn);
    }
    turn.items.push(it);
  }
  return turns;
}

export const isWork = (it: T.ChatItem) => it.kind === 'tool' || it.kind === 'thought' || (it.kind === 'permission' && !!it.permission?.outcome);
// A compaction's card ([D73]) reads like a notice: it marks where the session
// changed, so a settled turn's fold never hides it.
const isNote = (it: T.ChatItem) => it.kind === 'notice' || it.kind === 'error' || it.kind === 'compaction';
const isAside = (it: T.ChatItem) => it.kind === 'aside';

// What the timeline leaves out, whatever its kind: the prose AgentBox writes a
// project's chat when one of its agents finishes or asks. That is addressed to
// the model, and the app shows the agent's thread in the rail instead; the turn
// such a notice starts carries the same text as a user message and is marked
// the same way. The chat's own answer, an ordinary assistant message, stays.
//
// The flag and never the kind: a notice is also how a rollover marks where the
// session changed ([D73]), and that one is meant to be read. The cost is that
// conversations written before the flag existed still show their old agent
// notices, which is history, left as it was.
export const isSilent = (it: T.ChatItem) => !!it.hidden;

export function isActive(it: T.ChatItem | undefined): boolean {
  return (
    !!it &&
    ((it.kind === 'thought' && !!it.streaming) ||
      (it.kind === 'tool' && (it.tool?.status === 'pending' || it.tool?.status === 'in_progress')) ||
      (it.kind === 'subagent' && it.subagent?.state === 'running'))
  );
}

// nestedOf splits out what subagents did (D86): an item with a parent belongs
// under that subagent's card, not in the conversation. The rest is returned
// in order, and the nested items by the card they belong to.
function nestedOf(items: T.ChatItem[]): { top: T.ChatItem[]; children: Map<string, T.ChatItem[]> } {
  const top: T.ChatItem[] = [];
  const children = new Map<string, T.ChatItem[]>();
  for (const it of items) {
    if (!it.parent) {
      top.push(it);
      continue;
    }
    const list = children.get(it.parent);
    if (list) list.push(it);
    else children.set(it.parent, [it]);
  }
  return { top, children };
}

export function timelineRows(thread: T.ChatThread, openTurns: ReadonlySet<string>): Row[] {
  const rows: Row[] = [];
  const { top, children } = nestedOf(thread.items);
  for (const turn of turnsOf(top)) {
    const { user } = turn;
    const running = !!user && !user.result;
    const body = turn.items.filter((it) => !isSilent(it) && (it.kind === 'assistant' || it.kind === 'subagent' || isWork(it) || isNote(it) || isAside(it)));
    const final = body.findLast((it) => it.kind === 'assistant');
    const settled = !!user?.result;
    const hidden = settled ? body.filter((it) => it !== final && !isNote(it) && !isAside(it)).length : 0;
    const open = hidden === 0 || openTurns.has(turn.id);

    if (user && !isSilent(user)) rows.push({ type: 'user', key: user.id, item: user });
    if (running) rows.push({ type: 'working', key: `working:${turn.id}`, since: user.createdAt });
    if (hidden > 0) rows.push({ type: 'fold', key: `fold:${turn.id}`, turn: turn.id, label: foldLabel(user!), open });

    let group: T.ChatItem[] = [];
    const endGroup = (live: boolean) => {
      if (group.length > 0) rows.push({ type: 'work', key: `work:${group[0].id}`, items: group, live });
      group = [];
    };
    for (const it of body) {
      if (!open && it !== final && !isNote(it) && !isAside(it)) continue;
      if (isWork(it)) {
        group.push(it);
        continue;
      }
      endGroup(false);
      if (isAside(it)) {
        rows.push({ type: 'user', key: it.id, item: it });
        continue;
      }
      if (it.kind === 'subagent') {
        rows.push({ type: 'subagent', key: it.id, item: it, children: children.get(it.id) ?? [] });
        continue;
      }
      if (it.kind === 'compaction') {
        rows.push({ type: 'compaction', key: it.id, item: it });
        continue;
      }
      rows.push(it.kind === 'assistant' ? { type: 'assistant', key: it.id, item: it, final: settled && it === final } : { type: 'note', key: it.id, item: it });
    }
    endGroup(running);

    if (running) {
      const last = body.at(-1);
      if (!(last?.kind === 'assistant' && last.streaming) && !isActive(last)) rows.push({ type: 'thinking', key: `thinking:${turn.id}` });
    }
    if (settled) {
      // What a subagent edited is as much the turn's change as what the agent
      // did, so it counts too.
      const nested = turn.items.filter((it) => it.kind === 'subagent').flatMap((it) => children.get(it.id) ?? []);
      const files = changedFiles([...turn.items, ...nested]);
      if (files.length > 0) rows.push({ type: 'changes', key: `changes:${turn.id}`, files });
    }
  }
  return rows;
}

function foldLabel(user: T.ChatItem): string {
  const result = user.result!;
  const took = formatDuration((Date.parse(result.endedAt) - Date.parse(user.createdAt)) / 1000);
  if (result.state === 'cancelled') return `Stopped after ${took}`;
  if (result.state === 'failed') return `Failed after ${took}`;
  return `Worked for ${took}`;
}

export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

// Permission requests waiting for your answer, oldest first.
export function pendingPermissions(thread: T.ChatThread): T.ChatItem[] {
  return thread.items.filter((it) => it.kind === 'permission' && !it.permission?.outcome);
}

// The plan of the running turn, or of the last one.
export function currentPlan(thread: T.ChatThread): T.ChatPlanEntry[] | undefined {
  const plan = thread.items.findLast((it) => it.kind === 'plan');
  if (!plan?.plan?.length) return undefined;
  const lastUser = thread.items.findLast((it) => it.kind === 'user');
  return !lastUser || plan.turn === lastUser.id ? plan.plan : undefined;
}

export function toolOf(thread: T.ChatThread, callId: string): T.ChatTool | undefined {
  return thread.items.findLast((it) => it.tool?.callId === callId)?.tool;
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

// workSummary describes a group of tool calls by what they did, like "Ran 3
// commands, read 2 files and edited 1 file". The thoughts and answered requests
// among them show when the group is opened.
export function workSummary(items: T.ChatItem[]): string {
  let commands = 0;
  let reads = 0;
  let searches = 0;
  let fetches = 0;
  let others = 0;
  let thoughts = 0;
  let answers = 0;
  const edited = new Set<string>();
  for (const it of items) {
    if (it.kind === 'thought') thoughts++;
    else if (it.kind === 'permission') answers++;
    else {
      const tool = it.tool!;
      switch (tool.kind) {
        case 'execute':
          commands++;
          break;
        case 'read':
          reads++;
          break;
        case 'edit':
        case 'delete':
        case 'move':
          for (const path of tool.paths?.length ? tool.paths : [tool.title]) edited.add(path);
          break;
        case 'search':
          searches++;
          break;
        case 'fetch':
          fetches++;
          break;
        default:
          others++;
      }
    }
  }
  const actions = [
    commands && `ran ${plural(commands, 'command')}`,
    reads && `read ${plural(reads, 'file')}`,
    edited.size && `edited ${plural(edited.size, 'file')}`,
    searches && `searched ${searches === 1 ? 'once' : `${searches} times`}`,
    fetches && `fetched ${plural(fetches, 'page')}`,
    others && `used ${plural(others, 'tool')}`,
  ].filter((part): part is string => !!part);
  const parts =
    actions.length > 0
      ? actions
      : [thoughts && (thoughts === 1 ? 'thought' : `thought ${thoughts} times`), answers && `answered ${plural(answers, 'request')}`].filter(
          (part): part is string => !!part,
        );
  const text = parts.length <= 1 ? (parts[0] ?? '') : `${parts.slice(0, -1).join(', ')} and ${parts.at(-1)}`;
  return text.charAt(0).toUpperCase() + text.slice(1);
}

const fileName = (path: string) => path.split('/').filter(Boolean).at(-1) ?? path;

// entryLabel is what a work row says about a finished item.
export function entryLabel(it: T.ChatItem): string {
  if (it.kind === 'thought') return 'Thought';
  if (it.kind === 'permission') {
    const perm = it.permission!;
    const option = perm.options.find((o) => o.id === perm.outcome);
    const verb = perm.outcome === 'cancelled' ? 'Cancelled' : option?.kind.startsWith('reject') ? 'Denied' : 'Allowed';
    return `${verb}: ${perm.title}`;
  }
  const tool = it.tool!;
  if (tool.kind === 'execute' && tool.command) return tool.command;
  return tool.title || tool.name || 'Tool call';
}

// liveLabel is what a work row says about an item while it happens.
export function liveLabel(it: T.ChatItem): string {
  if (it.kind === 'thought') return 'Thinking';
  const tool = it.tool;
  if (!tool || !isActive(it)) return entryLabel(it);
  const path = tool.paths?.[0];
  switch (tool.kind) {
    case 'execute':
      return `Running ${tool.command || tool.title}`;
    case 'edit':
      return path ? `Editing ${fileName(path)}` : tool.title;
    case 'read':
      return path ? `Reading ${fileName(path)}` : tool.title;
    case 'search':
      return `Searching: ${tool.title}`;
    case 'fetch':
      return `Fetching ${tool.title}`;
  }
  return tool.title || tool.name || 'Working';
}

export interface ChangedFile {
  path: string;
  added: number;
  removed: number;
  diffs: T.ChatDiff[];
}

const counted = new WeakMap<T.ChatDiff, { added: number; removed: number }>();

export function lineCounts(diff: T.ChatDiff): { added: number; removed: number } {
  let counts = counted.get(diff);
  if (!counts) {
    counts = { added: 0, removed: 0 };
    for (const part of diffLines(diff.oldText, diff.newText)) {
      if (part.added) counts.added += part.count ?? 0;
      else if (part.removed) counts.removed += part.count ?? 0;
    }
    counted.set(diff, counts);
  }
  return counts;
}

// changedFiles sums up the edits a turn's tool calls made, file by file.
export function changedFiles(items: T.ChatItem[]): ChangedFile[] {
  const byPath = new Map<string, ChangedFile>();
  for (const it of items) {
    const tool = it.tool;
    if (!tool?.diffs || tool.status !== 'completed') continue;
    for (const diff of tool.diffs) {
      const file = byPath.get(diff.path) ?? { path: diff.path, added: 0, removed: 0, diffs: [] };
      const { added, removed } = lineCounts(diff);
      file.added += added;
      file.removed += removed;
      file.diffs.push(diff);
      byPath.set(diff.path, file);
    }
  }
  return [...byPath.values()];
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}k`;
  return String(n);
}

// contextHint reads a model choice's context-window size, when the tool states
// one: Claude Code's model picker names a long-context variant with a "1M
// context" phrase in the description, or a "[1m]" suffix on the value. A
// choice that says nothing gets no hint — never guessed. This is the only
// source for a choice other than the one actually running: the adapter
// doesn't advertise a per-model size, so an account whose model list states
// nothing (see modelHint in Composer.tsx for the running model's real size)
// just shows no badge for its other choices.
export function contextHint(choice: T.ChatOptionChoice): string | undefined {
  const match = choice.description?.match(/(\d+)\s*([mk])\s+context\b/i) ?? choice.value.match(/\[(\d+)([mk])\]$/i);
  return match ? `${match[1]}${match[2].toUpperCase()}` : undefined;
}
