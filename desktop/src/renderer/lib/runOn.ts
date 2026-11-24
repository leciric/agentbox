// Where the agents a shared project's chat makes for this AgentBox run, as
// the RunOnChip beside its composer's To: chooses (ChatMessageRequest.runOn,
// internal/daemon/sharemachines.go): on the computer that shares the project
// ('owner', the default) or on this one ('peer'). Each chat remembers its own,
// on this computer, by its ref, and sends it with every message, so the
// owner's lead hears it each time it changes.
import { useSyncExternalStore } from 'react';

export type RunOn = 'owner' | 'peer';

const key = 'agentbox.composer.runOn';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

const storage = (): Store | undefined => {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
};

function read(s = storage()): Record<string, RunOn> {
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

// useChosenRunOn is what the chat ref chose, 'owner' until it chooses.
export function useChosenRunOn(ref: string): RunOn {
  return useSyncExternalStore(subscribe, () => chosen[ref] ?? 'owner');
}

// chooseRunOn keeps the chat ref's choice; 'owner' is no entry.
export function chooseRunOn(ref: string, runOn: RunOn): void {
  if ((chosen[ref] ?? 'owner') === runOn) return;
  const { [ref]: _, ...rest } = chosen;
  chosen = runOn === 'peer' ? { ...rest, [ref]: runOn } : rest;
  try {
    storage()?.setItem(key, JSON.stringify(chosen));
  } catch {
    // a full or blocked store: the choice lasts this window only
  }
  for (const l of listeners) l();
}

// sentRunOn is what a message carries: this computer only while it can take
// the agents (here), whatever was chosen, so a choice it can no longer honour
// never reaches the owner's lead.
export function sentRunOn(chosen: RunOn, here: boolean | undefined): RunOn {
  return chosen === 'peer' && here ? 'peer' : 'owner';
}

// For the tests: start over from what s holds.
export function resetRunOnForTest(s: Store): void {
  chosen = read(s);
  for (const l of listeners) l();
}
