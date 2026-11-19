// daemonBusy is whether the daemon is slow to answer the window now: a
// request has been out longer than slowMs, or one timed out (the main
// process's idle timeout, main/requesttimeout.ts) within recentMs. The views
// don't wait on it: react-query keeps what they last had while they ask
// again, and the top bar shows a busy mark (components/BusyMarks.tsx), so a
// daemon stuck behind a loaded VM reads as "busy", not as a frozen app.
import { useSyncExternalStore } from 'react';

export const slowMs = 3_000;
export const recentMs = 15_000;

let next = 0;
const out = new Map<number, number>(); // request → when it was sent
let timedOutAt = -Infinity;
let busy = false;
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | null = null;

// now is performance.now, replaced by tests.
let now = () => performance.now();
export function setClock(fn: () => number): void {
  now = fn;
}

// update works busy out again, and when it next could change on its own.
export function update(): void {
  const t = now();
  let wake = Infinity;
  let slow = false;
  for (const sent of out.values()) {
    if (t - sent >= slowMs) slow = true;
    else wake = Math.min(wake, sent + slowMs);
  }
  const recent = t - timedOutAt < recentMs;
  if (recent) wake = Math.min(wake, timedOutAt + recentMs);
  const was = busy;
  busy = slow || recent;
  if (timer) clearTimeout(timer);
  timer = null;
  if (wake !== Infinity && typeof setTimeout !== 'undefined') {
    timer = setTimeout(update, Math.max(0, wake - t) + 1);
    (timer as { unref?: () => void }).unref?.();
  }
  if (busy !== was) for (const fn of listeners) fn();
}

// begin marks a request sent; the function it returns marks it answered,
// with whether it timed out.
export function begin(): (timedOut?: boolean) => void {
  const id = next++;
  out.set(id, now());
  update();
  return (timedOut = false) => {
    out.delete(id);
    if (timedOut) timedOutAt = now();
    update();
  };
}

export const isBusy = () => busy;

export function useDaemonBusy(): boolean {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    isBusy,
    isBusy,
  );
}

// reset forgets everything, for tests.
export function reset(): void {
  out.clear();
  timedOutAt = -Infinity;
  busy = false;
  if (timer) clearTimeout(timer);
  timer = null;
}
