// The weekly recap (GET /v1/recap, components/RecapView): how its numbers
// read, on the page and on the share cards (lib/recapCard.ts) alike.

import type * as T from '../../shared/api';
import { formatNumber, formatTime, language } from '../../shared/i18n/index.ts';

// recapQuery is the recap's request: this week (weeks 0) or one before, in
// the time zone the app runs in.
export function recapQuery(weeks: number, now = new Date()): string {
  return `/v1/recap?offset=${-now.getTimezoneOffset()}&weeks=${weeks}`;
}

// duration is agents' work as a person says it: 42h, 3.5h, 45m.
export function duration(seconds: number): string {
  const hours = seconds / 3600;
  if (hours >= 10) return `${formatNumber(Math.round(hours))}h`;
  if (hours >= 1) return `${formatNumber(Math.round(hours * 10) / 10)}h`;
  return `${Math.round(seconds / 60)}m`;
}

// compact is a count short enough for a card: 940, 12.4k, 1.2M.
export function compact(n: number): string {
  return formatNumber(n, { notation: 'compact', maximumFractionDigits: n >= 1000 && n < 10_000 ? 1 : n < 1e6 ? 0 : 1 });
}

// Spend is how a cost was come by (api.Recap.CostUSD, or a model's): as the
// AI tools reported it, partly or wholly estimated from list prices, or
// nothing at all to show.
export type Spend = 'reported' | 'partly' | 'estimated' | 'none';

export function spendOf(c: { costUsd: number; estimatedUsd: number }): Spend {
  if (!(c.costUsd > 0)) return 'none';
  if (!(c.estimatedUsd > 0)) return 'reported';
  // Summing in another order can leave the estimate a hair from the total.
  return c.estimatedUsd >= c.costUsd * 0.9999 ? 'estimated' : 'partly';
}

// modelLabel is a model as people call it: claude-opus-4-5 is Opus 4.5, and
// claude-sonnet-5-20261001 Sonnet 5. Other tools' names are kept as they are.
export function modelLabel(model: string): string {
  const m = model.match(/^claude-([a-z]+)-(\d+(?:-\d{1,2}(?!\d))?)(?:-\d{8})?(.*)$/);
  if (!m) return model;
  return `${m[1][0].toUpperCase()}${m[1].slice(1)} ${m[2].replace('-', '.')}${m[3]}`;
}

// lastDay is the week's last day: its End is the midnight after it.
export function lastDay(recap: T.Recap): Date {
  return new Date(new Date(recap.end).getTime() - 12 * 3600 * 1000);
}

// weekRange is the week's dates: Oct 18 – 24, 2026.
export function weekRange(recap: T.Recap): string {
  const f = new Intl.DateTimeFormat(language(), { month: 'short', day: 'numeric', year: 'numeric' });
  return f.formatRange(new Date(recap.start), lastDay(recap));
}

// dayName is a day of the week, short: Mon, seg.
export function dayName(date: string): string {
  return new Intl.DateTimeFormat(language(), { weekday: 'short' }).format(new Date(`${date}T12:00:00`));
}

// awayShare is the part of the work done while the user was away, 0 to 1.
export function awayShare(recap: T.Recap): number {
  return recap.workSeconds ? recap.awaySeconds / recap.workSeconds : 0;
}

// peakHour is the hour of the day agents worked most, or -1 in a week with
// no work.
export function peakHour(recap: T.Recap): number {
  let best = -1;
  recap.hours.forEach((s, h) => {
    if (s > 0 && (best < 0 || s > recap.hours[best])) best = h;
  });
  return best;
}

// peakLabel is the hour agents worked most, as the clock says it; '' in a
// week with no work.
export function peakLabel(recap: T.Recap): string {
  const h = peakHour(recap);
  if (h < 0) return '';
  const at = new Date();
  at.setHours(h, 0, 0, 0);
  return formatTime(at, { hour: 'numeric', minute: '2-digit' });
}

export type CardFormat = 'landscape' | 'square';

// cardSize is a card's size in pixels: X's 1.91:1 link card, and a square.
export const cardSize: Record<CardFormat, { width: number; height: number }> = {
  landscape: { width: 1200, height: 630 },
  square: { width: 1080, height: 1080 },
};

// cardFileName is what a saved card is called.
export function cardFileName(recap: T.Recap, format: CardFormat): string {
  const d = lastDay(recap);
  const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  return `agentbox-week-${day}-${format}.png`;
}

// isEmpty is a week nothing happened in.
export function isEmpty(recap: T.Recap): boolean {
  return recap.workSeconds === 0 && recap.agents === 0 && recap.tokens === 0;
}
