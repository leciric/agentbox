// The agents being destroyed in the background: DestroyAgentDialog closes at
// once and hands the destroy to removeAgent, so nothing waits on it; the
// sidebar marks their rows "removing…" until it ends, with a toast either way.
import { useSyncExternalStore } from 'react';

const removing = new Set<string>();
const listeners = new Set<() => void>();
let snapshot: ReadonlySet<string> = new Set();

function changed() {
  snapshot = new Set(removing);
  for (const l of listeners) l();
}

// removeAgent runs destroy for ref, marking it removing until it settles.
// A second call while one runs is ignored.
export async function removeAgent(ref: string, destroy: () => Promise<unknown>): Promise<'done' | 'failed' | 'busy'> {
  if (removing.has(ref)) return 'busy';
  removing.add(ref);
  changed();
  try {
    await destroy();
    return 'done';
  } finally {
    removing.delete(ref);
    changed();
  }
}

export const isRemoving = (ref: string) => removing.has(ref);

export function useRemoving(): ReadonlySet<string> {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => snapshot,
  );
}
