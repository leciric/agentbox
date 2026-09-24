// Formatting for the token ledger (D83). formatTokens in chat.ts is for a
// context window — 200k, 1M — and rounds too hard for a ledger, where 12.4M and
// 1.3B are the numbers worth telling apart.

export function humanTokens(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(n);
}

// usd is the AI tool's own estimate at API prices. A subscription isn't billed
// per token, so this is a way to compare, never a bill.
export function usd(v: number): string {
  if (!v) return '—';
  if (v < 0.01) return '<$0.01';
  if (v >= 1000) return `$${(v / 1000).toFixed(1)}K`;
  return `$${v.toFixed(2)}`;
}

// share is a fraction as a percentage a person reads: never "0%" for something
// that did happen.
export function share(part: number, whole: number): string {
  if (!whole || !part) return '0%';
  const pct = (part / whole) * 100;
  if (pct < 1) return '<1%';
  // Not "100%" for something that fell short of it: cache reads are 99.6% of
  // a long session, and the rest is the part worth seeing.
  if (pct > 99 && pct < 100) return `${Math.floor(pct * 10) / 10}%`;
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

// limitTone is how worrying a share of a limit is, in the top bar's own
// thresholds for the host's meters.
export function limitTone(fraction: number | null): 'ok' | 'warn' | 'high' {
  if (fraction === null) return 'ok';
  if (fraction > 0.85) return 'high';
  if (fraction > 0.65) return 'warn';
  return 'ok';
}
