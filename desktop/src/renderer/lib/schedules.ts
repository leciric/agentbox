// Scheduled tasks (internal/daemon/schedules.go): what the Schedules panel and
// the chat's chip need to say about one, and the editor's weekly form, which
// is a cron expression underneath. Pure, so it is tested without a page.
import type * as T from '../../shared/api.ts';
import type { MessageKey } from '../../shared/i18n/en-US.ts';
import { formatDateTime, formatList, formatTime } from '../../shared/i18n/index.ts';

type Translate = (key: MessageKey, values?: Record<string, string | number>) => string;

// Weekly is a cron expression that runs once a day on some days of the week,
// "30 8 * * 1-5": days 0 (Sunday) to 6.
export type Weekly = { days: number[]; hour: number; minute: number };

function expand(field: string, lo: number, hi: number): number[] | null {
  const out = new Set<number>();
  for (const part of field.split(',')) {
    const [range, stepText] = part.split('/');
    const step = stepText === undefined ? 1 : Number(stepText);
    if (!Number.isInteger(step) || step <= 0) return null;
    let from = lo;
    let to = hi;
    if (range !== '*') {
      const [a, b] = range.split('-');
      from = Number(a);
      to = b === undefined ? (stepText === undefined ? from : hi) : Number(b);
    }
    if (!Number.isInteger(from) || !Number.isInteger(to) || from < lo || to > hi || from > to) return null;
    for (let v = from; v <= to; v += step) out.add(v);
  }
  return [...out].toSorted((a, b) => a - b);
}

// parseWeekly reads cron as a Weekly, or null when it is anything else.
export function parseWeekly(cron: string): Weekly | null {
  const text = cron.trim().toLowerCase() === '@daily' ? '0 0 * * *' : cron.trim();
  const f = text.split(/\s+/);
  if (f.length !== 5 || f[2] !== '*' || f[3] !== '*' || !/^\d+$/.test(f[0]) || !/^\d+$/.test(f[1])) return null;
  const minute = Number(f[0]);
  const hour = Number(f[1]);
  if (minute > 59 || hour > 23) return null;
  const days = f[4] === '*' ? [0, 1, 2, 3, 4, 5, 6] : expand(f[4], 0, 7);
  if (!days || days.length === 0) return null;
  return { days: [...new Set(days.map((d) => d % 7))].toSorted((a, b) => a - b), hour, minute };
}

// weeklyCron writes a Weekly as cron, with runs of days as ranges.
export function weeklyCron({ days, hour, minute }: Weekly): string {
  const sorted = [...new Set(days)].toSorted((a, b) => a - b);
  let dow = '*';
  if (sorted.length > 0 && sorted.length < 7) {
    const parts: string[] = [];
    for (let i = 0; i < sorted.length; ) {
      let j = i;
      while (j + 1 < sorted.length && sorted[j + 1] === sorted[j] + 1) j++;
      parts.push(j - i >= 2 ? `${sorted[i]}-${sorted[j]}` : sorted.slice(i, j + 1).join(','));
      i = j + 1;
    }
    dow = parts.join(',');
  }
  return `${minute} ${hour} * * ${dow}`;
}

const weekdays = [1, 2, 3, 4, 5];

// dayName is a day of the week in the app's language: 0 is Sunday.
export function dayName(day: number, width: 'long' | 'short' = 'short'): string {
  // 4 January 1970 was a Sunday.
  return formatDateTime(Date.UTC(1970, 0, 4 + day, 12), { weekday: width, timeZone: 'UTC' });
}

function clock(hour: number, minute: number): string {
  return formatTime(Date.UTC(1970, 0, 1, hour, minute), { timeStyle: 'short', timeZone: 'UTC' });
}

// everyLabel is an interval in minutes, the way people say it.
export function everyLabel(minutes: number, t: Translate): string {
  if (minutes % 1440 === 0) return t('schedules.every.days', { n: minutes / 1440 });
  if (minutes % 60 === 0) return t('schedules.every.hours', { n: minutes / 60 });
  return t('schedules.every.minutes', { n: minutes });
}

// whenLabel says when a schedule runs: "Weekdays at 09:00", "Every 2 hours",
// "Once, 19 Oct 2026, 09:00".
export function whenLabel(s: Pick<T.Schedule, 'kind' | 'at' | 'everyMinutes' | 'cron' | 'timezone'>, t: Translate): string {
  if (s.kind === 'once') return t('schedules.when.once', { at: s.at ? formatDateTime(s.at, { dateStyle: 'medium', timeStyle: 'short', timeZone: zoneOrUndefined(s.timezone) }) : '' });
  if (s.kind === 'every') return everyLabel(s.everyMinutes ?? 0, t);
  const w = s.cron ? parseWeekly(s.cron) : null;
  if (!w) return t('schedules.when.cron', { cron: s.cron ?? '' });
  const at = clock(w.hour, w.minute);
  if (w.days.length === 7) return t('schedules.when.daily', { time: at });
  if (w.days.join() === weekdays.join()) return t('schedules.when.weekdays', { time: at });
  return t('schedules.when.days', { days: formatList(w.days.map((d) => dayName(d, w.days.length === 1 ? 'long' : 'short'))), time: at });
}

function zoneOrUndefined(zone: string): string | undefined {
  try {
    new Intl.DateTimeFormat('en', { timeZone: zone });
    return zone;
  } catch {
    return undefined;
  }
}

// userTimezone is the timezone this app runs in, which it reports to the
// daemon (Settings.timezone) for schedules made by the lead or the CLI.
export function userTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

// localInput is a date as a datetime-local input shows it, in this app's
// timezone; fromLocalInput reads one back as an instant.
export function localInput(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function fromLocalInput(value: string): string {
  return new Date(value).toISOString();
}

// Template is a starting point for a new schedule, the editor's chips.
export type Template = { id: string; title: MessageKey; task: MessageKey; cron?: string; every?: number; target: 'lead' | 'agent'; precheck?: string };

export const templates: Template[] = [
  { id: 'audit', title: 'schedules.template.audit.title', task: 'schedules.template.audit.task', cron: '0 9 * * 1-5', target: 'agent' },
  { id: 'release', title: 'schedules.template.release.title', task: 'schedules.template.release.task', cron: '0 10 * * 5', target: 'lead' },
  { id: 'review', title: 'schedules.template.review.title', task: 'schedules.template.review.task', cron: '0 18 * * *', target: 'lead' },
  { id: 'ci', title: 'schedules.template.ci.title', task: 'schedules.template.ci.task', every: 60, target: 'lead', precheck: 'gh pr list --state open --limit 1 | grep -q .' },
];
