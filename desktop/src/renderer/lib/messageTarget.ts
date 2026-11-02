// The target a project chat's composer sends to (shared/messageTarget.ts):
// the lead, or one of the project's agents. Each session remembers its own,
// on this computer, by its chat's ref; the agent cards above the composer and
// the keys set it as the chip does. An agent that was retired reads as the
// lead (liveTarget), and is forgotten the next time the target is set.
import { useSyncExternalStore } from 'react';

const key = 'agentbox.composer.target';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

const storage = (): Store | undefined => {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
};

function read(s = storage()): Record<string, string> {
  try {
    const raw = s?.getItem(key);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

let chosen = read();
const listeners = new Set<() => void>();
const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => listeners.delete(fn);
};

// useChosenTarget is the agent the chat ref's composer sends to, as chosen;
// undefined for its lead. Pass it through liveTarget before using it.
export function useChosenTarget(ref: string): string | undefined {
  return useSyncExternalStore(subscribe, () => chosen[ref]);
}

// chooseTarget points the chat ref's composer at an agent, or with none back
// at its lead.
export function chooseTarget(ref: string, agent: string | undefined): void {
  if (chosen[ref] === agent) return;
  const { [ref]: _, ...rest } = chosen;
  chosen = agent ? { ...rest, [ref]: agent } : rest;
  try {
    storage()?.setItem(key, JSON.stringify(chosen));
  } catch {
    // a full or blocked store: the choice lasts this window only
  }
  for (const l of listeners) l();
}

// For the tests: start over from what s holds.
export function resetTargetsForTest(s: Store): void {
  chosen = read(s);
  for (const l of listeners) l();
}
