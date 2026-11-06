// The main process's event-loop lag, for diagnostics: every IPC call, every
// window's request to the daemon and the tray go through this one thread, so
// when it stalls the whole app does. A histogram (perf_hooks'
// monitorEventLoopDelay, sampled by libuv, which costs next to nothing) is
// read every checkMs; a window whose worst delay passed lagMs is written to
// the app's log and handed to the window, which sends it to the daemon beside
// its own long tasks (renderer/lib/appTiming.ts), so Settings → Diagnostics
// shows a stall of either process.
import { monitorEventLoopDelay, type Histogram } from 'node:perf_hooks';

// A delay past this is one the user could have felt (as appTiming's longTaskMs).
export const lagMs = 200;
const checkMs = 5_000;

export interface Lag {
  at: string; // ISO time of the check
  maxMs: number; // the worst delay in the window
  p99Ms: number;
  windowMs: number; // how long the window was
}

// lagOf is what the histogram saw since it was last reset, or null when its
// worst delay stayed under lagMs.
export function lagOf(h: Pick<Histogram, 'max' | 'percentile'>, windowMs: number, now: Date): Lag | null {
  const maxMs = h.max / 1e6;
  if (!(maxMs > lagMs)) return null;
  return { at: now.toISOString(), maxMs: Math.round(maxMs), p99Ms: Math.round(h.percentile(99) / 1e6), windowMs };
}

// watchLoopLag starts watching; onLag is told of every lagging window. It
// returns what stops it.
export function watchLoopLag(onLag: (lag: Lag) => void): () => void {
  const h = monitorEventLoopDelay({ resolution: 20 });
  h.enable();
  const timer = setInterval(() => {
    const lag = lagOf(h, checkMs, new Date());
    h.reset();
    if (lag) onLag(lag);
  }, checkMs);
  timer.unref();
  return () => {
    clearInterval(timer);
    h.disable();
  };
}
