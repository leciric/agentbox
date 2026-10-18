// Settings → Diagnostics' helpers: the periods it reads the daemon's spans,
// stalls and resource samples over (/v1/diagnostics), and how it draws them.
import type * as T from '../../shared/api.ts';

export const periods = ['15m', '30m', '1h', '6h', '24h'] as const;
export type Period = (typeof periods)[number];

// The resource history's windows, and the stretch each bar covers. The daemon
// samples every intervalSeconds (30), so no bar is narrower than that.
export const resourceWindows = [
  { id: '5m', ms: 5 * 60_000, bucketMs: 30_000 },
  { id: '15m', ms: 15 * 60_000, bucketMs: 30_000 },
  { id: '1h', ms: 60 * 60_000, bucketMs: 60_000 },
] as const;
export type ResourceWindow = (typeof resourceWindows)[number]['id'];

// formatMs reads a duration the way the tables want it: ms under a second,
// then seconds, then minutes.
export function formatMs(ms: number): string {
  if (ms < 1) return `${ms.toFixed(2)} ms`;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export type Bucket = { start: number; avg: number; peak: number; samples: number };

// buckets averages samples into fixed stretches from now back over windowMs,
// for a chart of bars: an empty stretch (an agent that wasn't running) has
// no samples and is drawn as a gap.
export function buckets<S extends { at: string }>(
  samples: S[],
  pick: (s: S) => number,
  windowMs: number,
  bucketMs: number,
  now = Date.now(),
): Bucket[] {
  const n = Math.ceil(windowMs / bucketMs);
  const from = now - n * bucketMs;
  const out: Bucket[] = Array.from({ length: n }, (_, i) => ({ start: from + i * bucketMs, avg: 0, peak: 0, samples: 0 }));
  for (const s of samples) {
    const i = Math.floor((Date.parse(s.at) - from) / bucketMs);
    if (i < 0 || i >= n) continue;
    const b = out[i];
    const v = pick(s);
    b.avg += v;
    b.peak = Math.max(b.peak, v);
    b.samples++;
  }
  for (const b of out) if (b.samples) b.avg /= b.samples;
  return out;
}

// attrText is one of a span's attributes as text: they're whatever JSON the
// daemon put there.
export function attrText(v: unknown): string {
  return typeof v === 'string' ? v : JSON.stringify(v);
}

// spanAgent is the agent (or else the project) a span was about, for a column.
export function spanAgent(span: T.TraceSpan): string {
  const a = span.attrs ?? {};
  const v = a.agent ?? a.ref ?? a.project;
  return v === undefined ? '' : attrText(v);
}

// spanDetails is a span as text to paste into a bug report.
export function spanDetails(span: T.TraceSpan, version?: string): string {
  const lines = [
    `span: ${span.name}`,
    `duration: ${formatMs(span.durationMs)} (${span.durationMs} ms)`,
    `start: ${span.start}`,
  ];
  if (span.source) lines.push(`source: ${span.source}`);
  if (span.id) lines.push(`id: ${span.id}${span.parent ? ` (parent ${span.parent})` : ''}`);
  if (span.error) lines.push(`error: ${span.error}`);
  for (const [k, v] of Object.entries(span.attrs ?? {})) lines.push(`${k}: ${attrText(v)}`);
  if (version) lines.push(`agentbox: ${version}`);
  return lines.join('\n');
}
