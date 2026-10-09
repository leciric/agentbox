// Formatting for the token ledger (D83). formatTokens in chat.ts is for a
// context window — 200k, 1M — and rounds too hard for a ledger, where 12.4M and
// 1.3B are the numbers worth telling apart.

import { formatNumber, t } from '../../shared/i18n/index.ts';

// fixed is a number with a set count of decimals, in the language's own
// separator, and never grouped: 1.30B, not 1,300.00M.
function fixed(n: number, digits: number): string {
  return formatNumber(n, { minimumFractionDigits: digits, maximumFractionDigits: digits, useGrouping: false });
}

export function humanTokens(n: number): string {
  if (n >= 1e9) return `${fixed(n / 1e9, 2)}B`;
  if (n >= 1e6) return `${fixed(n / 1e6, 1)}M`;
  if (n >= 1e3) return `${fixed(n / 1e3, 1)}K`;
  return String(n);
}

// usd is the AI tool's own estimate at API prices. A subscription isn't billed
// per token, so this is a way to compare, never a bill.
export function usd(v: number): string {
  if (!v) return '—';
  if (v < 0.01) return '<$0.01';
  if (v >= 1000) return `$${fixed(v / 1000, 1)}K`;
  return `$${fixed(v, 2)}`;
}

// tps is an average tokens-per-second reading (state.TokenTotal.TPSOutput /
// TPSGenerationMS, as a rate): "—" for 0, which is never a turn that was slow
// but a stretch with nothing that timed itself to average.
export function tps(n: number): string {
  if (!n) return '—';
  if (n >= 100) return `${Math.round(n)} tok/s`;
  return `${fixed(n, 1)} tok/s`;
}

// share is a fraction as a percentage a person reads: never "0%" for something
// that did happen.
export function share(part: number, whole: number): string {
  if (!whole || !part) return '0%';
  const pct = (part / whole) * 100;
  if (pct < 1) return '<1%';
  // Not "100%" for something that fell short of it: cache reads are 99.6% of
  // a long session, and the rest is the part worth seeing.
  if (pct > 99 && pct < 100) return `${formatNumber(Math.floor(pct * 10) / 10)}%`;
  return `${Math.round(pct)}%`;
}

// A Claude account's usage limits, as the last chat on it reported them (D85).

// windowNow is a window as it stands: its share used, or null once it has
// reset since the reading — the number then describes a window that is over.
export function windowNow(w: { utilization: number; resetsAt: string }, now = Date.now()): number | null {
  const resets = Date.parse(w.resetsAt);
  if (Number.isFinite(resets) && resets > 0 && resets <= now) return null;
  return w.utilization;
}

// windowLeft is the time a window has left, to the minute: "3h 20m", "1h",
// "35m", "<1m". It is null when the reset is unknown or already past, so the
// caller falls back to naming the window.
export function windowLeft(resetsAt: string, now = Date.now()): string | null {
  const resets = Date.parse(resetsAt);
  if (!Number.isFinite(resets) || resets <= now) return null;
  const minutes = Math.floor((resets - now) / 60_000);
  if (minutes < 1) return t('shell.top.leftLessThanMinute');
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (h === 0) return t('shell.top.leftMinutes', { m });
  return m === 0 ? t('shell.top.leftHours', { h }) : t('shell.top.leftHoursMinutes', { h, m });
}

// limitTone is how worrying a share of a limit is, in the top bar's own
// thresholds for the host's meters.
export function limitTone(fraction: number | null): 'ok' | 'warn' | 'high' {
  if (fraction === null) return 'ok';
  if (fraction > 0.85) return 'high';
  if (fraction > 0.65) return 'warn';
  return 'ok';
}
