// Work (components/WorkView.tsx): every piece of work agents did, live and
// past, newest first, grouped by day, from GET /v1/work.
import type * as T from '../../shared/api.ts';

// workKey is the start of every Work query's key: a verdict, a merge, a
// destroy or a delete refreshes them all.
export const workKey = ['work'] as const;

// What the page filters on: everything, what waits for review, by the pull
// request's state, or with none. The daemon's names (api.WorkFilter*).
export type WorkFilter = '' | 'review' | 'open' | 'merged' | 'none' | 'closed';
export const workFilters: readonly WorkFilter[] = ['', 'review', 'open', 'merged', 'none', 'closed'];

export const isWorkFilter = (s: string | undefined): s is WorkFilter => workFilters.includes((s ?? '') as WorkFilter);

// workQuery is a page's query.
export function workQuery(filter: WorkFilter, q: string, limit: number, cursor?: string): URLSearchParams {
  const out = new URLSearchParams({ limit: String(limit) });
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
