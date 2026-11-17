import { useSyncExternalStore } from 'react';
import type { MachineRun } from './firstRun';
import { appendOutput, errorMessage } from './utils';

// The run of `agentbox vm init` (`agentbox wsl init` on Windows) that makes
// AgentBox's machine, kept here rather than in the card that started it: the
// first run moves on as soon as it starts, so the card is gone while it runs,
// and its progress and failure are the indicators' (FirstRun's rail, the top
// bar's MachineChip) to show and retry, wherever the app is.

export type MachineOptions = Parameters<typeof window.agentbox.hostSetup.run>[0];

export type MachineRunSnapshot = MachineRun & {
  // options are what it was started with: a retry runs the same.
  options?: MachineOptions;
};

// What a run keeps of its output: plenty for the log, never the whole of a
// long download's.
const keepLines = 2_000;

let run: MachineRunSnapshot = { state: 'idle', lines: [] };
let done: (() => void) | undefined;
const listeners = new Set<() => void>();

function set(next: MachineRunSnapshot) {
  run = next;
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// startMachine starts the run, unless one is under way, and resolves when it
// ends either way: how it ended is in the snapshot. onDone runs after it
// succeeds, whatever is on screen by then.
export function startMachine(options?: MachineOptions, onDone?: () => void): Promise<void> {
  if (run.state === 'running') return Promise.resolve();
  done = onDone;
  set({ state: 'running', lines: [], since: Date.now(), options });
  const stop = window.agentbox.hostSetup.onOutput((text) => {
    if (run.state === 'running') set({ ...run, lines: appendOutput(run.lines, text).slice(-keepLines) });
  });
  return window.agentbox.hostSetup
    .run(options)
    .then(
      () => {
        set({ ...run, state: 'done' });
        done?.();
      },
      (err: unknown) => set({ ...run, state: 'failed', error: errorMessage(err) }),
    )
    .finally(stop);
}

// retryMachine runs it again as it was last started (vm init is safe to run
// again, and picks up a VM half made).
export function retryMachine(): Promise<void> {
  return startMachine(run.options, done);
}

export function useMachineRun(): MachineRunSnapshot {
  return useSyncExternalStore(subscribe, () => run);
}
