import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

// The Running view (components/RunningProcesses.tsx): what each agent's
// machine still has running, as the daemon groups it in trees (a dev server
// with what it started is one), with a Kill on each the daemon doesn't
// protect. It opens from the sidebar's Running button and the top bar's
// machine popovers, on whichever project was open, and each agent's Resources
// panel shows its own.

type Request = { open: boolean; project?: string };

let current: Request = { open: false };
const listeners = new Set<() => void>();

// openRunning opens the Running view, on a project's agents or, with none,
// on every project's.
export function openRunning(project?: string) {
  current = { open: true, project };
  for (const listener of listeners) listener();
}

export function closeRunning() {
  current = { ...current, open: false };
  for (const listener of listeners) listener();
}

export function useRunningRequest(): Request {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => current,
  );
}

// smallTree is how little a tree holds to be folded away under "N small
// ones": a shell, a sleep, a watcher's helper. A flagged one never is.
export const smallTree = 32 << 20;

// splitTrees puts the trees worth a row first, the biggest first as the
// daemon sorted them, and the small ones apart. The machine's own tree comes
// last, whatever it holds: it can't be killed, so it's context, not a choice.
export function splitTrees(trees: T.ProcessTree[]): { shown: T.ProcessTree[]; small: T.ProcessTree[] } {
  const shown: T.ProcessTree[] = [];
  const small: T.ProcessTree[] = [];
  let system: T.ProcessTree | undefined;
  for (const tree of trees) {
    if (tree.kind === 'system') system = tree;
    else if (tree.memory < smallTree && !tree.heavy && !tree.idle && !tree.protected) small.push(tree);
    else shown.push(tree);
  }
  if (system) shown.push(system);
  return { shown, small };
}

// span is a stretch of time the way the Running view says it: 45s, 12m,
// 2h 14m, 3d.
export function span(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 48 * 3600) {
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return m ? `${h}h ${m}m` : `${h}h`;
  }
  return `${Math.floor(s / 86400)}d`;
}

// upFor is how long a tree has run, from when it started.
export function upFor(started: string, now = Date.now()): string | undefined {
  const at = new Date(started).getTime();
  if (!started || Number.isNaN(at) || at <= 0) return undefined;
  return span((now - at) / 1000);
}

// withoutTree is a list as it is once a tree was killed, before the next
// reading says so: the tree gone, and its agent's totals down by it.
export function withoutTree(list: T.ProcessList, ref: string, pid: number): T.ProcessList {
  return { ...list, agents: list.agents.map((a) => (a.ref === ref ? agentWithoutTree(a, pid) : a)) };
}

export function agentWithoutTree(a: T.AgentProcesses, pid: number): T.AgentProcesses {
  const tree = a.trees.find((t) => t.pid === pid);
  if (!tree) return a;
  return {
    ...a,
    trees: a.trees.filter((t) => t !== tree),
    memory: a.memory - tree.memory,
    killable: a.killable - (tree.protected ? 0 : tree.memory),
    cpu: a.cpu - tree.cpu,
    processes: a.processes - tree.processes,
  };
}

// runningCount is how many things a list shows that can be killed: the trees
// worth a row (splitTrees) the daemon doesn't protect, across its agents. It's
// the badge on the project's Running tab, so it counts what the tab lists
// above its "small ones", and never the AI tool's own or the machine's.
export function runningCount(list: T.ProcessList): number {
  return list.agents.reduce((n, a) => n + (a.running && !a.error ? splitTrees(a.trees).shown.filter((tr) => !tr.protected).length : 0), 0);
}
