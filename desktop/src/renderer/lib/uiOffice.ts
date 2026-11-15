// The Office (components/office), behind a flag, off by default (Settings →
// General → "Office"): a walkable 3D room with a desk for every agent. Like
// the prototype navigation's flag (uiVerbs.ts), it's how this app looks on
// this computer: localStorage, switched at once.
import { useSyncExternalStore } from 'react';

const storageKey = 'agentbox.ui.office';
const listeners = new Set<() => void>();
let current: boolean | undefined;

export function uiOffice(): boolean {
  if (current === undefined) {
    try {
      current = localStorage.getItem(storageKey) === '1';
    } catch {
      current = false;
    }
  }
  return current;
}

export function setUiOffice(on: boolean) {
  current = on;
  try {
    localStorage.setItem(storageKey, on ? '1' : '0');
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

export const useUiOffice = () =>
  useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, uiOffice);
