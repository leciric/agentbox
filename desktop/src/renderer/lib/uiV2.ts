// The layout, Settings → General → Layout: New (v2), the lead-first layout
// (components/v2), or Classic. It's how this app looks on this computer, so it
// lives in localStorage beside the other display settings (pageStackMode.ts),
// and the app switches as it changes, with no restart. With Classic, nothing
// of v2 is drawn.
//
// v2 is the default. The flag is '1' for v2 and '0' for Classic; before v2
// was the default it was '1' or missing, so missing is decided once (arrive):
// an install that had tried v2 and turned it off (v2's sidebar or panel left
// their keys) keeps Classic, and every other moves to v2 and is shown its
// tour (tours.ts).
import { useSyncExternalStore } from 'react';

const storageKey = 'agentbox.ui.v2';
// Keys only v2 writes: their being there with the flag missing means v2 was
// tried and turned off, which removed the flag.
const v2Traces = ['agentbox.v2.projects', 'agentbox.ui.v2.panel'];
// Written on every launch since What's new (whatsnew.ts): missing, this is
// the app's first run.
const launchedKey = 'agentbox.whatsnew.lastSeenVersion';

// Arrival is how this launch came to its layout: a first run, an install moved
// to v2 now, one already on v2, or one on Classic.
export type Arrival = 'fresh' | 'moved' | 'v2' | 'classic';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

// arrive decides the layout of an install whose flag is missing, storing it,
// and says how it got there.
export function arrive(storage: Store): Arrival {
  const flag = storage.getItem(storageKey);
  if (flag === '1') return 'v2';
  if (flag === '0') return 'classic';
  if (v2Traces.some((k) => storage.getItem(k) !== null)) {
    storage.setItem(storageKey, '0');
    return 'classic';
  }
  storage.setItem(storageKey, '1');
  return storage.getItem(launchedKey) === null ? 'fresh' : 'moved';
}

const listeners = new Set<() => void>();
let current: boolean | undefined;
let arrival: Arrival | undefined;

function load() {
  if (current !== undefined) return;
  try {
    arrival = arrive(localStorage);
  } catch {
    arrival = 'fresh';
  }
  current = arrival !== 'classic';
}

export function uiV2(): boolean {
  load();
  return current!;
}

// launchArrival is how this launch came to its layout, decided at the first read.
export function launchArrival(): Arrival {
  load();
  return arrival!;
}

export function setUiV2(on: boolean) {
  load();
  current = on;
  try {
    localStorage.setItem(storageKey, on ? '1' : '0');
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
