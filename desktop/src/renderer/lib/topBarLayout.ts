// Whether the top bar shows the machine as one status bubble (the default) or
// as a strip with its memory, CPU and disk always in view. It's how this app
// looks on this computer, not something the daemon needs, so it lives in
// localStorage beside the app's other local choices (voice/settings.ts).
import { useSyncExternalStore } from 'react';

const storageKey = 'agentbox.topbar.detailed';
const listeners = new Set<() => void>();
let current: boolean | undefined;

export function topBarDetailed(): boolean {
  if (current === undefined) {
    try {
      current = localStorage.getItem(storageKey) === '1';
    } catch {
      current = false;
    }
  }
  return current;
}

export function setTopBarDetailed(on: boolean) {
  current = on;
  try {
    localStorage.setItem(storageKey, on ? '1' : '0');
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const listener of listeners) listener();
}

export function useTopBarDetailed(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    topBarDetailed,
  );
}
