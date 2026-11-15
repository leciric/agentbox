// Work (components/WorkView.tsx): every piece of work agents did, live and
// past, newest first, grouped by day, from GET /v1/work.
import type * as T from '../../shared/api.ts';
import { stackOrder } from './stacks.ts';

// workKey is the start of every Work query's key: a verdict, a merge, a
// destroy or a delete refreshes them all.
export const workKey = ['work'] as const;

// What the page filters on: everything, what waits for review, by the pull
// request's state, or with none. The daemon's names (api.WorkFilter*).
export type WorkFilter = '' | 'review' | 'open' | 'merged' | 'none' | 'closed';
export const workFilters: readonly WorkFilter[] = ['', 'review', 'open', 'merged', 'none', 'closed'];

export const isWorkFilter = (s: string | undefined): s is WorkFilter => workFilters.includes((s ?? '') as WorkFilter);

// workQuery is a page's query. The list asks for what its rows show (brief):
// a row's report is its status alone and it has no task, and the review reads
// the report of the one it opens.
export function workQuery(filter: WorkFilter, q: string, limit: number, cursor?: string): URLSearchParams {
  const out = new URLSearchParams({ limit: String(limit), brief: '1' });
  if (filter) out.set('filter', filter);
  if (q.trim()) out.set('q', q.trim());
  if (cursor) out.set('cursor', cursor);
  return out;
}

// dayOf is the local day a piece of work is grouped under, as YYYY-MM-DD.
export function dayOf(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// byDay groups items, already newest first, under their day, in that order.
export function byDay(items: readonly T.WorkItem[]): { day: string; items: T.WorkItem[] }[] {
  const out: { day: string; items: T.WorkItem[] }[] = [];
  for (const item of items) {
    const day = dayOf(item.at);
    const last = out[out.length - 1];
    if (last?.day === day) last.items.push(item);
    else out.push({ day, items: [item] });
  }
  return out;
}

// dayLabel names a day: today, yesterday, a weekday within the week, or the
// date, in the app's language.
export function dayLabel(day: string, now: Date, locale: string, words: { today: string; yesterday: string }): string {
  const [y, m, d] = day.split('-').map(Number);
  const date = new Date(y, m - 1, d);
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const days = Math.round((start.getTime() - date.getTime()) / 86_400_000);
  if (days === 0) return words.today;
  if (days === 1) return words.yesterday;
  if (days > 1 && days < 7) return date.toLocaleDateString(locale, { weekday: 'long' });
  return date.toLocaleDateString(locale, { day: 'numeric', month: 'long', ...(date.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}) });
}

// awaitingReview is whether a review item still waits for a verdict: a live
// agent that finished (it has a fingerprint) and has none. Only then does the
// review offer Merge, Mark reviewed and Request changes.
export const awaitingReview = (item: T.ReviewItem) => !!item.fingerprint && !item.verdict;

// outcomeOf is what a row says came of the work: its report's status (done,
// partial, blocked, failed), else whether it's still at work.
export function outcomeOf(w: T.WorkItem): string {
  if (w.report?.status) return w.report.status;
  return w.status === 'working' ? 'working' : '';
}

// WorkRow is a line of the list as it is drawn: a day's heading, or a piece
// of work under it, depth down a stack of that day's work (stackOrder), first
// when it is the day's first.
export type WorkRow = { kind: 'day'; key: string; day: string } | { kind: 'work'; key: string; work: T.WorkItem; depth: number; first: boolean };

const branchKey = (project: string, branch?: string) => (branch ? `${project}\n${branch}` : undefined);

// workRows is the list's lines, in order: items, newest first, under their
// days, each day's stacks together. One flat list, so only the lines in view
// are drawn however many are loaded.
export function workRows(items: readonly T.WorkItem[]): WorkRow[] {
  const out: WorkRow[] = [];
  for (const g of byDay(items)) {
    out.push({ kind: 'day', key: `day:${g.day}`, day: g.day });
    const stacked = stackOrder(
      g.items,
      (w) => `${w.item.project}\n${w.item.branch ?? ''}`,
      (w) => branchKey(w.item.project, w.item.baseRef),
    );
    stacked.forEach(({ item: work, depth }, n) => out.push({ kind: 'work', key: work.item.ref, work, depth, first: n === 0 }));
  }
  return out;
}

// The loaded pages of a list, as the query keeps them.
export type WorkPages = { pages: T.WorkPage[]; pageParams: unknown[] };

// comesAfter is whether a sorts after b in the list: older, or of the same
// moment and later by project and agent, the daemon's order.
function comesAfter(a: T.WorkItem, b: T.WorkItem): boolean {
  const at = Date.parse(a.at) - Date.parse(b.at);
  if (at !== 0) return at < 0;
  if (a.item.project !== b.item.project) return a.item.project > b.item.project;
  return a.item.agent > b.item.agent;
}

// mergeHead puts a fresh first page (head) in front of the pages already
// loaded, instead of reading every one of them again: what the minute's
// refresh does. The later pages keep the work that still comes after the new
// first page's last, so what moved into or out of the first page is neither
// listed twice nor lost; a change further down waits for the event that
// reports it, which reads the pages again.
export function mergeHead(data: WorkPages | undefined, head: T.WorkPage): WorkPages | undefined {
  if (!data || data.pages.length === 0) return data;
  const last = head.items[head.items.length - 1];
  if (data.pages.length === 1 || !head.next || !last) return { pages: [head], pageParams: data.pageParams.slice(0, 1) };
  const fresh = new Set(head.items.map((w) => w.item.ref));
  const rest = data.pages.slice(1).map((p) => {
    const items = p.items.filter((w) => !fresh.has(w.item.ref) && comesAfter(w, last));
    return items.length === p.items.length ? p : { ...p, items };
  });
  return { pages: [head, ...rest], pageParams: data.pageParams };
}
