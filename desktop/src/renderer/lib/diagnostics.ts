// Settings → Diagnostics' data: the daemon's spans, stalls and resource
// samples under /v1/diagnostics, and the app's own spans it's sent
// (appTiming.ts).
//
// TODO(daemon tracing): these shapes are the ones agreed for the daemon's side,
// written here until internal/api has them; then they come from shared/api.ts
// and the fixtures below go, with api.ts's fallback to them.

// A span's figures over the period, per span name.
export interface SpanStat {
  name: string;
  count: number;
  errors: number;
  p50Ms: number;
  p95Ms: number;
  maxMs: number;
}

// One span: an operation the daemon (or the app, named "app …") timed.
export interface Span {
  name: string;
  traceId?: string;
  spanId?: string;
  start: string;
  durationMs: number;
  error?: string;
  attrs?: Record<string, string>;
}

export interface SpanReport {
  stats: SpanStat[];
  slowest: Span[];
  failures: Span[];
}

// A stall is a stretch the daemon couldn't get on with its work: its event
// loop, the database, or the machine under memory pressure.
export interface Stall {
  kind: string;
  start: string;
  durationMs: number;
  detail?: string;
}

export interface StallReport {
  stalls: Stall[];
}

export interface ResourceSample {
  at: string;
  // cpu is a percentage of one core, as the usage endpoints give it.
  cpu: number;
  memory: number;
}

// The samples of the VM ("vm") or one agent (its ref).
export interface ResourceSeries {
  ref: string;
  kind: 'vm' | 'agent';
  memTotal?: number;
  samples: ResourceSample[];
}

export interface ResourceReport {
  series: ResourceSeries[];
}

// One of the app's spans, as posted to client-spans.
export interface ClientSpan {
  kind: 'request' | 'longtask';
  name: string;
  start: string;
  durationMs: number;
  status?: number;
  error?: string;
}

export const periods = ['15m', '30m', '1h', '6h', '24h'] as const;
export type Period = (typeof periods)[number];

export const resourceWindows = [
  { id: '5m', ms: 5 * 60_000, bucketMs: 10_000 },
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
export function buckets(
  samples: ResourceSample[],
  pick: (s: ResourceSample) => number,
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

// spanDetails is a span as text to paste into a bug report.
export function spanDetails(span: Span, version?: string): string {
  const lines = [
    `span: ${span.name}`,
    `duration: ${formatMs(span.durationMs)} (${span.durationMs} ms)`,
    `start: ${span.start}`,
  ];
  if (span.traceId) lines.push(`trace: ${span.traceId}${span.spanId ? ` span ${span.spanId}` : ''}`);
  if (span.error) lines.push(`error: ${span.error}`);
  for (const [k, v] of Object.entries(span.attrs ?? {})) lines.push(`${k}: ${v}`);
  if (version) lines.push(`agentbox: ${version}`);
  return lines.join('\n');
}

// Fixtures, for a daemon without /v1/diagnostics yet and for the preview.
// Deterministic, so a screenshot is the same twice.

function rand(seed: number) {
  let x = seed;
  return () => {
    x = (x * 1103515245 + 12345) % 2 ** 31;
    return x / 2 ** 31;
  };
}

const fixtureOps: [name: string, p50: number, spread: number, count: number, errors: number][] = [
  ['POST /v1/agents', 180, 6, 14, 1],
  ['job create-agent', 21_400, 1.8, 14, 1],
  ['incus start', 3_200, 2.5, 22, 0],
  ['git worktree add', 410, 3, 14, 0],
  ['acp prompt', 38_000, 4, 96, 3],
  ['GET /v1/agents', 4.2, 3, 2_310, 0],
  ['GET /v1/usage', 61, 2, 3_620, 2],
  ['sqlite write', 1.1, 8, 9_400, 0],
  ['memory consolidate', 12_800, 2, 6, 1],
  ['app GET /v1/agents', 9.8, 4, 2_290, 0],
  ['app POST /v1/projects/{}/chat', 48, 3, 41, 0],
  ['app long task', 260, 1.6, 7, 0],
];

const fixtureErrors = [
  'incus: Error: Failed to start device "eth0": network "agentbox0" not found',
  'context deadline exceeded (Client.Timeout exceeded while awaiting headers)',
  'acp: session ended: claude-agent-acp exited with status 1',
  'database is locked (SQLITE_BUSY)',
];

export function fixtureSpans(now = Date.now()): SpanReport {
  const r = rand(7);
  const stats: SpanStat[] = fixtureOps.map(([name, p50, spread, count, errors]) => ({
    name,
    count,
    errors,
    p50Ms: p50,
    p95Ms: p50 * spread,
    maxMs: p50 * spread * (1.2 + r()),
  }));
  const slowest: Span[] = Array.from({ length: 50 }, (_, i) => {
    const [name, p50, spread] = fixtureOps[Math.floor(r() * 5)];
    return {
      name,
      traceId: Math.floor(r() * 2 ** 52).toString(16).padStart(16, '0'),
      start: new Date(now - r() * 30 * 60_000).toISOString(),
      durationMs: Math.round(p50 * spread * (1 + r())),
      attrs: { agent: `pawly/agent-0${1 + (i % 4)}` },
    };
  }).sort((a, b) => b.durationMs - a.durationMs);
  const failures: Span[] = Array.from({ length: 8 }, (_, i) => {
    const [name, p50] = fixtureOps[[4, 1, 6, 7][i % 4]];
    return {
      name,
      traceId: Math.floor(r() * 2 ** 52).toString(16).padStart(16, '0'),
      start: new Date(now - (i + r()) * 3 * 60_000).toISOString(),
      durationMs: Math.round(p50 * (0.5 + r())),
      error: fixtureErrors[i % fixtureErrors.length],
      attrs: { agent: `pawly/agent-0${1 + (i % 3)}` },
    };
  });
  return { stats, slowest, failures };
}

export function fixtureStalls(now = Date.now()): StallReport {
  return {
    stalls: [
      { kind: 'memory pressure', start: new Date(now - 11 * 60_000).toISOString(), durationMs: 8_400, detail: 'memory full 42% over 10s: 2 agents paused' },
      { kind: 'database', start: new Date(now - 47 * 60_000).toISOString(), durationMs: 2_150, detail: 'write lock held by memory consolidate' },
      { kind: 'event loop', start: new Date(now - 3.2 * 3600_000).toISOString(), durationMs: 1_300, detail: 'GET /v1/agents waited on incus list' },
    ],
  };
}

export function fixtureResources(windowMs: number, now = Date.now()): ResourceReport {
  const r = rand(11);
  const step = windowMs <= 5 * 60_000 ? 2_000 : windowMs <= 15 * 60_000 ? 5_000 : 10_000;
  const series = (ref: string, kind: 'vm' | 'agent', cpu: number, mem: number, from = 0): ResourceSeries => {
    const samples: ResourceSample[] = [];
    for (let t = now - windowMs + from * windowMs; t <= now; t += step) {
      const wave = Math.sin(t / 90_000) * 0.5 + 0.5;
      samples.push({ at: new Date(t).toISOString(), cpu: cpu * (0.3 + wave * r()), memory: mem * (0.8 + 0.2 * wave) });
    }
    return { ref, kind, memTotal: kind === 'vm' ? 16 * 1024 ** 3 : undefined, samples };
  };
  return {
    series: [
      series('vm', 'vm', 340, 9.6 * 1024 ** 3),
      series('pawly/agent-01', 'agent', 180, 3.1 * 1024 ** 3),
      series('pawly/agent-02', 'agent', 95, 2.2 * 1024 ** 3),
      series('pawly/agent-03', 'agent', 60, 1.4 * 1024 ** 3, 0.4),
    ],
  };
}
