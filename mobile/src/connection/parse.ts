// What the daemon sends, made safe for the screens to read. The types are
// generated from Go (shared/api.ts), but a phone can be paired with an
// AgentBox older or newer than the one it was built against, and Go writes a
// nil slice as null (ReviewPage.items, ChatPermission.options,
// ProjectPullRequests.pullRequests, …). Every list and object a screen reads
// goes through here: a missing list is empty, a field of the wrong type is
// its zero value, and an item without its id is dropped rather than rendered.
// Fields the phone doesn't read are kept as they came.
import type * as T from '../shared/api.ts';

type Obj = Record<string, unknown>;

const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const optStr = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined);
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
const optNum = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const strs = (v: unknown): string[] | undefined => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : undefined);

// list is v's items that make sense, each made safe; nothing when v isn't a list.
function list<X>(v: unknown, one: (o: Obj) => X | undefined): X[] {
  if (!Array.isArray(v)) return [];
  const out: X[] = [];
  for (const x of v) {
    const item = isObj(x) ? one(x) : undefined;
    if (item !== undefined) out.push(item);
  }
  return out;
}

const opt = <X>(v: unknown, one: (o: Obj) => X | undefined): X | undefined => (isObj(v) ? one(v) : undefined);

// keyed is o with its id field a non-empty string, or undefined.
const keyed = (o: Obj, field: string) => typeof o[field] === 'string' && o[field] !== '';

export function agent(o: Obj): T.Agent | undefined {
  if (!keyed(o, 'ref') || !keyed(o, 'project') || !keyed(o, 'name')) return undefined;
  return {
    ...(o as unknown as T.Agent),
    title: str(o.title),
    state: str(o.state),
    chat: optStr(o.chat),
    stalledSince: optStr(o.stalledSince),
    background: strs(o.background),
    outcome: optStr(o.outcome),
    outcomeUntil: optStr(o.outcomeUntil),
  };
}

export function project(o: Obj): T.Project | undefined {
  if (!keyed(o, 'name')) return undefined;
  return { ...(o as unknown as T.Project), displayName: str(o.displayName) };
}

export function projectChat(v: unknown): T.ProjectChat {
  const o = isObj(v) ? v : {};
  return { ...(o as unknown as T.ProjectChat), project: str(o.project), ref: str(o.ref), started: o.started === true && keyed(o, 'ref'), chat: str(o.chat) };
}

export function question(o: Obj): T.Question | undefined {
  if (!keyed(o, 'id')) return undefined;
  return {
    ...(o as unknown as T.Question),
    project: str(o.project),
    agent: str(o.agent),
    ref: str(o.ref),
    kind: optStr(o.kind),
    question: str(o.question),
    context: optStr(o.context),
    status: str(o.status),
    createdAt: str(o.createdAt),
  };
}

const option = (o: Obj): T.ChatPermissionOption | undefined => (keyed(o, 'id') ? { id: str(o.id), name: str(o.name), kind: str(o.kind) } : undefined);

export function permission(o: Obj): T.ChatPermission {
  return { ...(o as unknown as T.ChatPermission), callId: str(o.callId), title: str(o.title), options: list(o.options, option), outcome: optStr(o.outcome) };
}

export function tool(o: Obj): T.ChatTool {
  return { ...(o as unknown as T.ChatTool), callId: str(o.callId), title: str(o.title), kind: str(o.kind), status: str(o.status), name: optStr(o.name), command: optStr(o.command), paths: strs(o.paths) };
}

export function chatItem(o: Obj): T.ChatItem | undefined {
  if (!keyed(o, 'id')) return undefined;
  const ap = opt(o.agentPermission, (p) =>
    keyed(p, 'ref') ? { ...(p as unknown as T.ChatAgentPermission), agent: str(p.agent), ref: str(p.ref), item: str(p.item), permission: permission(isObj(p.permission) ? p.permission : {}), tool: opt(p.tool, tool) } : undefined,
  );
  return {
    ...(o as unknown as T.ChatItem),
    id: str(o.id),
    turn: str(o.turn),
    kind: str(o.kind),
    text: optStr(o.text),
    parent: optStr(o.parent),
    images: Array.isArray(o.images) ? list(o.images, (i) => (keyed(i, 'id') ? (i as unknown as T.ChatImage) : undefined)) : undefined,
    tool: opt(o.tool, tool),
    permission: opt(o.permission, permission),
    subagent: opt(o.subagent, (s) => ({ name: str(s.name), task: str(s.task), state: str(s.state) })),
    compaction: opt(o.compaction, (c) => ({ ...(c as unknown as T.ChatCompaction), state: str(c.state) })),
    result: opt(o.result, (r) => r as unknown as T.ChatTurnResult),
    agentPermission: ap,
    createdAt: str(o.createdAt),
    updatedAt: str(o.updatedAt),
  };
}

export function chatSession(v: unknown): T.ChatSession {
  const o = isObj(v) ? v : {};
  return {
    ...(o as unknown as T.ChatSession),
    state: str(o.state),
    tool: str(o.tool),
    detail: optStr(o.detail),
    turnStartedAt: optStr(o.turnStartedAt),
    stalledSince: optStr(o.stalledSince),
    background: strs(o.background),
    options: Array.isArray(o.options) ? (o.options as T.ChatOption[]) : [],
    commands: Array.isArray(o.commands) ? (o.commands as T.ChatCommand[]) : [],
  };
}

export function chatThread(v: unknown): T.ChatThread {
  const o = isObj(v) ? v : {};
  return { ...(o as unknown as T.ChatThread), agent: str(o.agent), seq: num(o.seq), session: chatSession(o.session), items: list(o.items, chatItem), older: o.older === true };
}

export function pullRequest(o: Obj): T.PullRequest | undefined {
  if (typeof o.number !== 'number') return undefined;
  return {
    ...(o as unknown as T.PullRequest),
    title: str(o.title),
    state: str(o.state),
    checks: str(o.checks),
    url: str(o.url),
    baseBranch: optStr(o.baseBranch),
    additions: optNum(o.additions),
    deletions: optNum(o.deletions),
  };
}

export function pulls(v: unknown): T.ProjectPullRequests {
  const o = isObj(v) ? v : {};
  return { ...(o as unknown as T.ProjectPullRequests), project: str(o.project), pullRequests: list(o.pullRequests, pullRequest), mergeMethods: strs(o.mergeMethods) };
}

export function reviewItem(o: Obj): T.ReviewItem | undefined {
  if (!keyed(o, 'ref') || !keyed(o, 'project')) return undefined;
  return {
    ...(o as unknown as T.ReviewItem),
    agent: str(o.agent),
    title: optStr(o.title),
    branch: optStr(o.branch),
    summary: optStr(o.summary),
    fingerprint: str(o.fingerprint),
    changes: opt(o.changes, (c) => ({ ...(c as unknown as T.AgentChanges), files: num(c.files), insertions: num(c.insertions), deletions: num(c.deletions) })),
    pr: opt(o.pr, pullRequest),
  };
}

export function reviewPage(v: unknown): T.ReviewPage {
  const o = isObj(v) ? v : {};
  const items = list(o.items, reviewItem);
  return { ...(o as unknown as T.ReviewPage), items, total: typeof o.total === 'number' && Number.isFinite(o.total) ? o.total : items.length };
}

export function mediaItem(o: Obj): T.MediaItem | undefined {
  if (!keyed(o, 'id')) return undefined;
  return { ...(o as unknown as T.MediaItem), kind: str(o.kind), name: str(o.name), removed: o.removed === true };
}

export function settings(v: unknown): T.Settings {
  const o = isObj(v) ? v : {};
  return { ...(o as unknown as T.Settings), language: str(o.language) };
}

export const agents = (v: unknown) => list(v, agent);
export const projects = (v: unknown) => list(v, project);
export const questions = (v: unknown) => list(v, question);
export const media = (v: unknown) => list(v, mediaItem);

// chatEvent is a chat event the phone can apply, or undefined.
export function chatEvent(v: unknown): T.ChatEvent | undefined {
  if (!isObj(v) || !keyed(v, 'agent') || typeof v.seq !== 'number') return undefined;
  return {
    ...(v as unknown as T.ChatEvent),
    item: opt(v.item, chatItem),
    session: isObj(v.session) ? chatSession(v.session) : undefined,
    append: opt(v.append, (a) => (keyed(a, 'id') ? { id: str(a.id), text: str(a.text) } : undefined)),
    after: optStr(v.after),
    cleared: v.cleared === true,
  };
}

// agentChange is an agent's change of state, or undefined.
export function agentChange(v: unknown): T.AgentChange | undefined {
  if (!isObj(v) || !keyed(v, 'ref')) return undefined;
  return { ...(v as unknown as T.AgentChange), ref: str(v.ref), state: str(v.state), removed: v.removed === true };
}
