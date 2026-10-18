// App timing: the renderer's own spans, for Settings → Diagnostics beside the
// daemon's. Two kinds, both cheap to take: every socket request's round trip
// (api.ts's call, the one chokepoint), and the main thread's long tasks from a
// PerformanceObserver. They're kept in a small buffer and posted to the
// daemon's /v1/diagnostics/client-spans in one batch at a time; a daemon
// without that endpoint turns the whole thing off for the session, silently,
// and a batch that fails to send is dropped rather than retried.
import type * as T from '../../shared/api.ts';

export const clientSpansPath = '/v1/diagnostics/client-spans';

// A long task is one the browser reports at 50 ms; only those over this are
// worth a span, as a jank the user could have felt.
export const longTaskMs = 200;
// How long spans wait to be sent, and how many a batch carries: past that,
// the oldest go, so a burst of polling can't grow the buffer.
const flushMs = 30_000;
const maxBuffered = 500; // api.MaxClientSpans, the most the daemon takes at once

type Send = (spans: T.TraceSpan[]) => Promise<unknown>;

let send: Send | null = null;
let buffer: T.TraceSpan[] = [];
let timer: ReturnType<typeof setTimeout> | null = null;
let observer: { disconnect(): void } | null = null;

// Collections whose next segment is a name or an id the user chose: a span's
// name keeps the route, not the agent, so the daemon can group them. Agents
// are named by project and agent, two segments; a chat's files by the rest.
const oneId = new Set([
  'accounts', 'artifacts', 'claude', 'connectors', 'github', 'groups', 'heavy', 'images', 'jobs', 'login',
  'media', 'options', 'permissions', 'phones', 'projects', 'pulls', 'questions', 'review-notes', 'secrets',
  'sections', 'skills', 'snaps', 'snapshots', 'threads', 'usage-stats', 'web', 'schedules',
]);
const twoIds = new Set(['agents', 'archived-agents']);

// routeName is a request's span name: its method and path with the query and
// every chosen name or id taken out, "GET /v1/agents/{}/{}/chat".
export function routeName(method: string, path: string): string {
  const parts = path.split('?')[0].split('/');
  const out: string[] = [];
  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    out.push(part);
    if (part === 'files' && parts[i - 1] === 'chat' && i + 1 < parts.length) {
      out.push('{}');
      break;
    }
    const ids = oneId.has(part) ? 1 : twoIds.has(part) ? 2 : 0;
    for (let n = 0; n < ids && i + 1 < parts.length; n++, i++) out.push('{}');
  }
  return `${method} ${out.join('/')}`;
}

function push(span: T.TraceSpan): void {
  if (!send) return;
  buffer.push(span);
  if (buffer.length > maxBuffered) buffer.splice(0, buffer.length - maxBuffered);
  if (!timer) {
    timer = setTimeout(() => void flush(), flushMs);
    // Node's timers keep a test's process alive; a browser's have no unref.
    (timer as { unref?: () => void }).unref?.();
  }
}

// recordRequest times one socket request. started is performance.now() when
// it was sent; status is the daemon's answer, or 0 when it never arrived.
export function recordRequest(method: string, path: string, started: number, status: number, error?: string): void {
  if (!send || path.startsWith(clientSpansPath)) return;
  const durationMs = performance.now() - started;
  // The daemon's failures count as the app's too; a 4xx is an answer.
  const failed = error ?? (status >= 500 ? `HTTP ${status}` : undefined);
  push({
    name: `app ${routeName(method, path)}`,
    start: new Date(Date.now() - durationMs).toISOString(),
    durationMs: Math.round(durationMs * 10) / 10,
    attrs: { status },
    ...(failed ? { error: failed } : {}),
  });
}

// recordLongTask keeps a long task the observer reported.
export function recordLongTask(startTime: number, duration: number): void {
  if (duration <= longTaskMs) return;
  push({
    name: 'app long task',
    start: new Date(performance.timeOrigin + startTime).toISOString(),
    durationMs: Math.round(duration),
  });
}

// flush sends what's buffered. A daemon that doesn't know the endpoint (404,
// 405) or won't take it (any other 4xx) stops the timing for good; anything
// else loses just this batch.
export async function flush(): Promise<void> {
  if (timer) clearTimeout(timer);
  timer = null;
  const batch = buffer;
  buffer = [];
  if (!send || batch.length === 0) return;
  try {
    await send(batch);
  } catch (err) {
    const status = (err as { status?: number }).status ?? 0;
    if (status >= 400 && status < 500) stopAppTiming();
  }
}

// startAppTiming turns the timing on, sending with send. Until it's called
// (tests, the preview) recording does nothing.
export function startAppTiming(sender: Send): void {
  send = sender;
  if (observer || typeof PerformanceObserver === 'undefined') return;
  if (!PerformanceObserver.supportedEntryTypes?.includes('longtask')) return;
  const o = new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) recordLongTask(entry.startTime, entry.duration);
  });
  o.observe({ type: 'longtask', buffered: true });
  observer = o;
}

export function stopAppTiming(): void {
  send = null;
  buffer = [];
  if (timer) clearTimeout(timer);
  timer = null;
  observer?.disconnect();
  observer = null;
}

// pending is what's waiting to be sent, for tests.
export const pending = (): readonly T.TraceSpan[] => buffer;
