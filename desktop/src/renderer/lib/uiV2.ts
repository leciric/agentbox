// "Try the new layout (v2)", Settings → General → Experimental: the
// lead-first layout (components/v2), off by default. It's how this app looks
// on this computer, so it lives in localStorage beside the other display
// settings (pageStackMode.ts), and the app switches as it changes, with no
// restart. With it off, nothing of v2 is drawn.
import { useSyncExternalStore } from 'react';

const storageKey = 'agentbox.ui.v2';
const listeners = new Set<() => void>();
let current: boolean | undefined;

export function uiV2(): boolean {
  if (current === undefined) {
    try {
      current = localStorage.getItem(storageKey) === '1';
    } catch {
      current = false;
    }
  }
  return current;
}

export function setUiV2(on: boolean) {
  current = on;
  try {
    if (on) localStorage.setItem(storageKey, '1');
    else localStorage.removeItem(storageKey);
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const listener of listeners) listener();
}

export function useUiV2(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    uiV2,
  );
}
