// Whether UI v2's sidebar groups the idle projects at the bottom and sorts by
// activity (lib/projectOrder.ts). Off by default: every project is listed in
// a stable order, the pins in the order the user dragged them and then the
// rest by name, and activity never moves one. It's how this app behaves on
// this computer, so it lives in localStorage beside the other display
// settings, and applies as it changes.
import { useSyncExternalStore } from 'react';

const storageKey = 'agentbox.v2.groupIdle';
const listeners = new Set<() => void>();
let current: boolean | undefined;

export function groupIdle(): boolean {
  if (current === undefined) {
    try {
      current = localStorage.getItem(storageKey) === 'true';
    } catch {
      current = false;
    }
  }
  return current;
}

export function setGroupIdle(on: boolean) {
  current = on;
  try {
    localStorage.setItem(storageKey, String(on));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

export const useGroupIdle = () =>
  useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, groupIdle);
