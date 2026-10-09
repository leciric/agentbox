// Each agent's chat: its thread in the query cache, kept current by the daemon's
// chat events, and the rows the timeline derives from it.
import type { QueryClient } from '@tanstack/react-query';
import { diffLines } from 'diff';
import type * as T from '../../shared/api';
import { formatNumber, t } from '../../shared/i18n/index.ts';
import { api, isProjectChat } from './api.ts';

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

// A chat opens on its latest messages, and older ones come a page at a time as
// you scroll up to them (loadOlder). The daemon pages by messages — yours and
// the AI tool's answers, not tool calls — and starts each page at a turn, so a
// turn, its tool calls and its subagents' work always arrive together.
export const pageSize = 20;

// isMessage is what counts towards a page, the same as isMessage in package chat.
export const isMessage = (it: T.ChatItem) => (it.kind === 'user' || it.kind === 'aside' || it.kind === 'assistant') && !it.parent && !it.hidden;

// fetchThread reads a chat's latest page. When the chat is already held, it
// asks for everything from the oldest item it holds, so reading it again —
// after a missed event, a reconnect, or opening the chat again — never drops
// a message already on screen: one that arrived meanwhile doesn't push the
// oldest out of a page of the same size.
// from, when given, reads back to that item instead of the oldest one held.
export async function fetchThread(queryClient: QueryClient, ref: string, from?: string): Promise<T.ChatThread> {
  const held = queryClient.getQueryData<T.ChatThread>(chatKey(ref));
  const limit = Math.max(pageSize, held?.items.filter(isMessage).length ?? 0);
  const thread = await api.chat(ref, { limit, from: from ?? held?.items[0]?.id });
  const next = advance(thread, recent.get(ref) ?? []);
  const now = queryClient.getQueryData<T.ChatThread>(chatKey(ref));
  return keepHeld(next === 'gap' ? thread : next, now);
}

// keepHeld puts back in front of a thread just read the older items the chat
// held before it, which reading it again mustn't take away: a page loaded
// while it was being read, or anything a daemon that pages without from left
// out. Only items before the read's first one, and only when the chat held
// that one too: a chat cleared meanwhile shares nothing with it, and is
// replaced whole.
export function keepHeld(thread: T.ChatThread, held: T.ChatThread | undefined): T.ChatThread {
  const first = thread.items[0];
  if (!held || !first || !thread.older) return thread;
  const k = held.items.findIndex((it) => it.id === first.id);
  if (k <= 0) return thread;
  return { ...thread, items: [...held.items.slice(0, k), ...thread.items], older: held.older };
}

// loadOlder reads the page before the oldest item a chat holds, and puts it in
// front. It answers whether it added anything.
export async function loadOlder(queryClient: QueryClient, ref: string): Promise<boolean> {
  const held = queryClient.getQueryData<T.ChatThread>(chatKey(ref));
  const first = held?.items[0];
  if (!held?.older || !first) return false;
  const page = await api.chat(ref, { before: first.id, limit: pageSize });
  const now = queryClient.getQueryData<T.ChatThread>(chatKey(ref));
  // Read again or cleared meanwhile: the page no longer goes in front of it.
  if (!now || now.items[0]?.id !== first.id) return false;
  const next = prependPage(now, page, recent.get(ref) ?? []);
  if (next !== now) queryClient.setQueryData(chatKey(ref), next);
  return next !== now;
}

// loadThrough reads a chat back as far as the item id, older than anything it
// holds, which a search found: everything from that item's turn on, in one
// read, as scrolling up to it would have. It answers whether the chat holds
// the item now.
export async function loadThrough(queryClient: QueryClient, ref: string, id: string): Promise<boolean> {
  const has = (thread?: T.ChatThread) => !!thread?.items.some((it) => it.id === id);
  if (has(queryClient.getQueryData<T.ChatThread>(chatKey(ref)))) return true;
  const thread = await fetchThread(queryClient, ref, id);
  queryClient.setQueryData(chatKey(ref), thread);
  return has(thread);
}

// prependPage puts an older page in front of a thread. The page is as of its
// own seq; events after it that changed its items, which the thread skipped
// for not having them, are applied to it here. A chat cleared after it makes
// it history nobody has any more.
export function prependPage(thread: T.ChatThread, page: T.ChatThread, events: T.ChatEvent[]): T.ChatThread {
  const later = events.filter((ev) => ev.seq > page.seq);
  if (later.some((ev) => ev.cleared)) return thread;
  const held = new Set(thread.items.map((it) => it.id));
  let items = page.items.filter((it) => !held.has(it.id));
  if (items.length === 0 && page.older === thread.older) return thread;
  for (const ev of later) {
    const id = ev.item?.id ?? ev.append?.id;
    const i = id === undefined ? -1 : items.findIndex((it) => it.id === id);
    if (i < 0) continue;
    if (ev.item) items = items.with(i, ev.item);
    else if (ev.append) items = items.with(i, { ...items[i], text: (items[i].text ?? '') + ev.append.text });
  }
  return { ...thread, items: [...items, ...thread.items], older: page.older };
}

export function applyChatEvent(queryClient: QueryClient, ev: T.ChatEvent): void {
  const list = recent.get(ev.agent) ?? [];
  list.push(ev);
  if (list.length > keepEvents) list.splice(0, list.length - keepEvents);
  recent.set(ev.agent, list);

  if (ev.session) {
    const { state, stalledSince } = ev.session;
    // What it left running between turns, which makes a ready chat awaiting.
    const background = ev.session.background?.length ? ev.session.background : undefined;
    const sameWork = (a: T.Agent) => (a.background ?? []).join('\n') === (background ?? []).join('\n');
    queryClient.setQueryData<T.Agent[]>(['agents'], (agents) =>
      agents?.some((a) => a.ref === ev.agent && (a.chat !== state || a.stalledSince !== stalledSince || !sameWork(a)))
        ? agents.map((a) => (a.ref === ev.agent ? { ...a, chat: state, stalledSince, background } : a))
        : agents,
    );
    // The lead isn't in the agents list: its state is on the project's chat,
    // which the rail's Project chat avatar reads.
    queryClient.setQueryData<T.ProjectChat>(['projectChat', ev.agent.split('/')[0]], (info) =>
      info && info.ref === ev.agent && info.chat !== state ? { ...info, chat: state } : info,
    );
  }
  // A turn's checkpoint was taken, or a rollback dropped the later ones.
  if (ev.checkpoint || ev.after) void queryClient.invalidateQueries({ queryKey: ['checkpoints', ev.agent] });
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
    if (i < 0 && olderThanHeld(thread, ev.item)) return next;
    next.items = i < 0 ? [...thread.items, ev.item] : thread.items.with(i, ev.item);
  } else if (ev.append) {
    const i = indexOf(thread.items, ev.append.id);
    if (i >= 0) next.items = thread.items.with(i, { ...thread.items[i], text: (thread.items[i].text ?? '') + ev.append.text });
  } else if (ev.after) {
    // A rollback: what came after the turn it went back to is gone. Not
    // holding that item means it is on a page not read yet, before
    // everything held.
    next.items = thread.items.slice(0, indexOf(thread.items, ev.after) + 1);
  }
  return next;
}

// olderThanHeld says an item the thread doesn't have belongs to a page it hasn't
// read yet, rather than being new: new items go at the end, and are never
// older than the ones already there. It comes with that page.
function olderThanHeld(thread: T.ChatThread, it: T.ChatItem): boolean {
  const first = thread.items[0];
  return !!thread.older && !!first && Date.parse(it.createdAt) < Date.parse(first.createdAt);
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
  | { type: 'woken'; key: string; item: T.ChatItem }
  | { type: 'background'; key: string; tasks: string[] }
  | { type: 'assistant'; key: string; item: T.ChatItem; final: boolean }
  | { type: 'work'; key: string; items: T.ChatItem[]; live: boolean }
  | { type: 'fold'; key: string; turn: string; label: string; open: boolean }
  | { type: 'working'; key: string; since: string; stalledSince?: string }
  | { type: 'thinking'; key: string }
  | { type: 'note'; key: string; item: T.ChatItem }
  | { type: 'subagent'; key: string; item: T.ChatItem; children: T.ChatItem[] }
  | { type: 'compaction'; key: string; item: T.ChatItem }
  | { type: 'credential'; key: string; item: T.ChatItem }
  | { type: 'changes'; key: string; files: ChangedFile[] }
  | { type: 'turn'; key: string; checkpoint: T.Checkpoint };

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

// isCredentialRequest is the agent calling request_credential (D95), or
// request_connector, under whatever name its tool gives an MCP call:
// mcp__memory__request_credential in Claude Code, memory.request_credential
// or the like elsewhere. Both wait on a card.
export const isCredentialRequest = (it: T.ChatItem) =>
  it.kind === 'tool' && /request_(credential|connector)/.test(`${it.tool?.name ?? ''} ${it.tool?.title ?? ''}`);

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

// asleep says whether an agent's machine is stopped or paused, the states in
// which its chat is still read from the database and a message sent wakes it:
// the daemon starts or resumes the machine and delivers it (tellAgent). A
// project's chat has no machine. Anything else not running (queued,
// initializing, incomplete, missing) has nothing to wake into.
// toolsReload says whether "Reload tools" can restart a chat's AI tool now,
// and why not otherwise: the daemon refuses while the session is busy, since
// a restart would end the turn or its background work, and a session that
// isn't running reads the current tools when it starts anyway.
export function toolsReload(session: T.ChatSession | undefined): 'ready' | 'busy' | 'off' {
  if (!session || session.state === 'off' || session.state === 'error') return 'off';
  if (session.state !== 'ready' || session.turnStartedAt || (session.background ?? []).length > 0) return 'busy';
  return 'ready';
}

export const asleep = (agent: Pick<T.Agent, 'ref' | 'state'>): 'stopped' | 'paused' | undefined =>
  isProjectChat(agent.ref) ? undefined : agent.state === 'stopped' || agent.state === 'paused' ? agent.state : undefined;

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

// checkpoints are the agent's, by the turn each ends: a settled turn that has
// one ends with a marker to roll back to it or fork from it.
export function timelineRows(thread: T.ChatThread, openTurns: ReadonlySet<string>, checkpoints?: ReadonlyMap<string, T.Checkpoint>): Row[] {
  const rows: Row[] = [];
  const { top, children } = nestedOf(thread.items);
  for (const turn of turnsOf(top)) {
    const { user } = turn;
    const running = !!user && !user.result;
    const body = turn.items.filter((it) => !isSilent(it) && (it.kind === 'assistant' || it.kind === 'subagent' || isWork(it) || isNote(it) || isAside(it)));
    const final = body.findLast((it) => it.kind === 'assistant');
    const settled = !!user?.result;
    const hidden = settled ? body.filter(folds).length : 0;
    const open = hidden === 0 || openTurns.has(turn.id);

    // A woken turn has no message at its head: the AI tool's session started
    // it, because work it left running in the background ended.
    if (user?.woken) rows.push({ type: 'woken', key: user.id, item: user });
    else if (user && !isSilent(user)) rows.push({ type: 'user', key: user.id, item: user });
    if (running) rows.push({ type: 'working', key: `working:${turn.id}`, since: user.createdAt, stalledSince: thread.session?.stalledSince });
    if (hidden > 0) rows.push({ type: 'fold', key: `fold:${turn.id}`, turn: turn.id, label: foldLabel(user!), open });

    let group: T.ChatItem[] = [];
    const endGroup = (live: boolean) => {
      if (group.length > 0) rows.push({ type: 'work', key: `work:${group[0].id}`, items: group, live });
      group = [];
    };
    for (const it of body) {
      if (!open && folds(it)) continue;
      if (isWork(it)) {
        group.push(it);
        // A credential request the agent is still blocked on gets its card
        // right under the call, where the spinner is: it is the one thing in
        // the conversation waiting on you.
        if (isCredentialRequest(it) && isActive(it)) {
          endGroup(running);
          rows.push({ type: 'credential', key: `credential:${it.id}`, item: it });
        }
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
      const checkpoint = user && checkpoints?.get(user.id);
      if (checkpoint) rows.push({ type: 'turn', key: `turn:${turn.id}`, checkpoint });
    }
  }
  // Between turns, what the session still has running: its end wakes it.
  const background = thread.session?.background ?? [];
  if (background.length > 0 && !thread.session?.turnStartedAt) rows.push({ type: 'background', key: 'background', tasks: background });
  return rows;
}

// folds says what a settled turn puts behind its "Worked for …": the work —
// tool calls, thoughts, subagents — never what was said. A message you read
// while the turn ran stays where it was when the turn ends.
const folds = (it: T.ChatItem) => it.kind !== 'assistant' && !isNote(it) && !isAside(it);

function foldLabel(user: T.ChatItem): string {
  const result = user.result!;
  const took = formatDuration((Date.parse(result.endedAt) - Date.parse(user.createdAt)) / 1000);
  if (result.state === 'cancelled') return t('chat.fold.stopped', { time: took });
  if (result.state === 'failed') return t('chat.fold.failed', { time: took });
  return t('chat.fold.worked', { time: took });
}

export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return t('chat.duration.seconds', { s: formatNumber(s) });
  if (s < 3600) return t('chat.duration.minutes', { m: formatNumber(Math.floor(s / 60)), s: formatNumber(s % 60) });
  return t('chat.duration.hours', { h: formatNumber(Math.floor(s / 3600)), m: formatNumber(Math.floor((s % 3600) / 60)) });
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

// workSummary describes a group of tool calls by what they did, like "Ran 3
// commands, edited 1 file and read 2 files": what changed things first, as
// t3code's activity log does. The thoughts and answered requests among them
// show when the group is opened.
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
    commands && t('chat.work.ran', { count: commands }),
    edited.size && t('chat.work.edited', { count: edited.size }),
    reads && t('chat.work.read', { count: reads }),
    searches && t('chat.work.searched', { count: searches }),
    fetches && t('chat.work.fetched', { count: fetches }),
    others && t('chat.work.used', { count: others }),
  ].filter((part): part is string => !!part);
  const parts =
    actions.length > 0
      ? actions
      : [thoughts && t('chat.work.thought', { count: thoughts }), answers && t('chat.work.answered', { count: answers })].filter(
          (part): part is string => !!part,
        );
  const text = parts.length <= 1 ? (parts[0] ?? '') : t('chat.work.join', { head: parts.slice(0, -1).join(t('chat.work.separator')), last: parts.at(-1)! });
  return text.charAt(0).toUpperCase() + text.slice(1);
}

const fileName = (path: string) => path.split('/').filter(Boolean).at(-1) ?? path;

// entryLabel is what a work row says about a finished item.
export function entryLabel(it: T.ChatItem): string {
  if (it.kind === 'thought') return t('chat.work.thoughtDone');
  if (it.kind === 'permission') {
    const perm = it.permission!;
    const option = perm.options.find((o) => o.id === perm.outcome);
    const outcome = perm.outcome === 'cancelled' ? 'cancelled' : option?.kind.startsWith('reject') ? 'denied' : 'allowed';
    return t('chat.work.permission', { outcome, title: perm.title });
  }
  const tool = it.tool!;
  if (tool.kind === 'execute' && tool.command) return tool.command;
  return tool.title || tool.name || t('chat.work.toolCall');
}

// liveLabel is what a work row says about an item while it happens.
export function liveLabel(it: T.ChatItem): string {
  if (it.kind === 'thought') return t('chat.work.thinking');
  const tool = it.tool;
  if (!tool || !isActive(it)) return entryLabel(it);
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
  if (n >= 1_000_000) {
    const digits = n % 1_000_000 === 0 ? 0 : 1;
    return t('chat.tokens.millions', { n: formatNumber(n / 1_000_000, { minimumFractionDigits: digits, maximumFractionDigits: digits }) });
  }
  if (n >= 1000) return t('chat.tokens.thousands', { n: formatNumber(Math.round(n / 1000)) });
  return formatNumber(n);
}

// contextBadge is a context window's size as a model picker's badge reads it,
// "200K" or "1M" in every language: it names the model's variant.
export function contextBadge(n: number): string {
  if (n >= 1_000_000) return `${+(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
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
