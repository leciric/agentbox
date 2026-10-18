// Groups: one task run on several agents at once, perhaps each on another AI
// tool or model, compared side by side when they finish so one can be kept
// (internal/daemon/groups.go). What here is pure is what the sidebar's chip,
// the lead chat's card, the fan-out dialog and the Compare view share.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

// MemberSt is where one of a group's agents is, as the Compare view labels
// it: still being made, couldn't be, working, finished, or decided — kept, or
// retired with the others.
export type MemberSt = 'creating' | 'failed' | 'working' | 'finished' | 'kept' | 'retired';

export function memberSt(g: Pick<T.Group, 'kept' | 'settledAt'>, m: T.GroupMember): MemberSt {
  if (m.error) return 'failed';
  if (!m.agent) return 'creating';
  if (g.kept && g.kept === m.agent) return 'kept';
  if (g.settledAt) return 'retired';
  if (m.finishedAt && !m.busy) return 'finished';
  return 'working';
}

// open is a group still waiting to be decided.
export const isOpen = (g: T.Group) => !g.settledAt;

// ready says every agent of an open group has finished, or couldn't be made:
// it is time to compare.
export const ready = (g: T.Group) => isOpen(g) && g.finished >= g.members.length;

// projectGroups are a project's open groups, oldest first, as its agents are
// listed in the sidebar.
export function projectGroups(groups: T.Group[] | undefined, project: string): T.Group[] {
  return (groups ?? []).filter((g) => g.project === project && isOpen(g)).sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

// memberOf finds the group an agent was made for, and its place in it.
export function memberOf(groups: T.Group[] | undefined, ref: string): { group: T.Group; member: T.GroupMember } | undefined {
  for (const group of groups ?? []) {
    const member = group.members.find((m) => m.ref === ref);
    if (member) return { group, member };
  }
  return undefined;
}

// tookMs is how long a member took: from its agent being made to its first
// finish, or to now while it works.
export function tookMs(m: T.GroupMember, now: number): number | undefined {
  if (!m.startedAt) return undefined;
  const end = m.finishedAt ? Date.parse(m.finishedAt) : now;
  return Math.max(0, end - Date.parse(m.startedAt));
}

// duration is a span in the fewest words: 45s, 12m, 1h 4m.
export function duration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

// best marks the lowest of a column of numbers, the cheapest or quickest, so
// the Compare view can point at it; nothing when fewer than two are known or
// they are all the same.
export function lowest(values: (number | undefined)[]): number | undefined {
  const known = values.filter((v): v is number => v !== undefined && v > 0);
  if (known.length < 2) return undefined;
  const min = Math.min(...known);
  return known.every((v) => v === min) ? undefined : min;
}

// latestGroupNotes keeps, of a chat's rows, only the last notice of each
// group: the card shows the group as it is now, so one is enough, at the
// latest news of it.
type Noted = Pick<T.ChatItem, 'id' | 'group'>;
const noteOf = (r: object): Noted | undefined => ('item' in r ? (r.item as Noted | undefined) : undefined);
export function latestGroupNotes<R extends object>(rows: R[]): R[] {
  const last = new Map<string, string>();
  for (const r of rows) {
    const it = noteOf(r);
    if (it?.group) last.set(it.group, it.id);
  }
  return rows.filter((r) => {
    const it = noteOf(r);
    return !it?.group || last.get(it.group) === it.id;
  });
}

// ——— What each agent of a new group runs ———

// A variant is one choice in the fan-out dialog's menus: an AI tool, and the
// model it runs, "" for the one new agents start on.
export interface Variant {
  value: string;
  ai: string;
  model: string;
  label: string;
}

const toolNames: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', opencode: 'OpenCode', cursor: 'Cursor' };
export const toolName = (ai: string) => toolNames[ai] ?? ai;

const strip = (name: string) => name.replace(/\s*\(recommended\)$/i, '');

// variants are what an agent of this machine can run: Claude Code on each of
// its models, Codex, OpenCode and Cursor on theirs, only for the tools signed
// in. A tool whose models aren't known yet offers its default.
export function variants(settings: T.Settings | undefined, auth: T.AuthStatus | undefined, defaultLabel: string): Variant[] {
  const out: Variant[] = [];
  const add = (ai: string, choices: T.ChatOptionChoice[] | undefined) => {
    if (!choices?.length) {
      out.push({ value: `${ai}|`, ai, model: '', label: `${toolName(ai)} · ${defaultLabel}` });
      return;
    }
    for (const c of choices) out.push({ value: `${ai}|${c.value}`, ai, model: c.value, label: `${toolName(ai)} · ${strip(c.name || c.value)}` });
  };
  if (auth?.claude !== false) add('claude', settings?.claudeModelChoices);
  // Codex takes no model from AgentBox: it runs the one its login has.
  if (auth?.codex) out.push({ value: 'codex|', ai: 'codex', model: '', label: toolName('codex') });
  if (auth?.opencode) add('opencode', settings?.openCodeModelChoices);
  if (auth?.cursor) add('cursor', settings?.cursorModelChoices);
  return out;
}

// defaultPicks are a new group's first two agents: two different Claude Code
// models when there are two, as comparing models is what a group is mostly
// for, else the first choice twice. Claude Code's own "Default" is no model
// to compare: it is whichever the account's default is.
export function defaultPicks(options: Variant[]): string[] {
  // Opus and Sonnet when the menu has them; never Fable, which nobody picks
  // without meaning to.
  const claude = options.filter((o) => o.ai === 'claude' && o.model !== 'default' && !/fable/i.test(o.model));
  const named = ['opus', 'sonnet'].map((m) => claude.find((o) => o.model === m)).filter((o) => !!o);
  if (named.length === 2) return named.map((o) => o.value);
  if (claude.length >= 2) return [claude[0].value, claude[1].value];
  const first = options[0]?.value ?? 'claude|';
  const other = options.find((o) => o.value !== first)?.value ?? first;
  return [first, other];
}

export function toVariant(value: string): T.FanOutVariant {
  const [ai, model] = value.split('|');
  return { ai, model: model || undefined };
}

// ——— The fan-out dialog, one for the app ———

export interface FanOutSeed {
  project: string;
  title?: string;
  task?: string;
}

let seed: FanOutSeed | null = null;
const listeners = new Set<() => void>();
const set = (next: FanOutSeed | null) => {
  seed = next;
  for (const fn of listeners) fn();
};
export const openFanOut = (next: FanOutSeed) => set(next);
export const closeFanOut = () => set(null);
export function useFanOut(): FanOutSeed | null {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => seed,
  );
}

// ——— Opening the Compare view from where there is no onSelect ———

// The lead chat's card has no way to change the app's view itself: it asks,
// and App, which listens, opens the group.
const compareListeners = new Set<(id: string) => void>();
export const openCompare = (id: string) => {
  for (const fn of compareListeners) fn(id);
};
export function onOpenCompare(fn: (id: string) => void): () => void {
  compareListeners.add(fn);
  return () => compareListeners.delete(fn);
}
