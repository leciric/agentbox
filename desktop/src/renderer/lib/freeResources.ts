// "Free resources", the top bar's one click that stops every running agent
// and, in VM mode, turns AgentBox's VM off; and "Start", the one click back.
// What to stop, what the button offers, and what was freed are worked out
// here, apart from the components, so they can be tested without a DOM.
import type { VMPower, VMPowerState } from '../../preload';
import type * as T from '../../shared/api';
import { formatList, formatNumber, t } from '../../shared/i18n/index.ts';

// FreeTarget is one agent Free resources would stop, and what it's up to:
// working (mid-turn) and asking (waiting on you) lose what they were doing,
// so the confirmation names them.
export interface FreeTarget {
  ref: string;
  label: string;
  doing: 'working' | 'asking' | 'idle' | 'paused';
}

// freeTargets is every agent holding memory: running or paused. Working and
// asking ones come first, since those are the ones the user is about to cut
// off.
export function freeTargets(agents: T.Agent[]): FreeTarget[] {
  const order = { working: 0, asking: 1, idle: 2, paused: 3 };
  return agents
    .filter((a) => a.state === 'running' || a.state === 'paused')
    .map((a): FreeTarget => ({
      ref: a.ref,
      label: a.title || a.name,
      doing: a.state === 'paused' ? 'paused' : a.chat === 'running' ? 'working' : a.chat === 'waiting' ? 'asking' : 'idle',
    }))
    .sort((a, b) => order[a.doing] - order[b.doing]);
}

// FreeMode is what the top bar's button offers:
//   free    stop the running agents (and the VM, in VM mode)
//   start   everything is off: bring back the VM and the agents Free
//           resources stopped
//   busy    the VM is on its way up or down; nothing to click
//   idle    host mode with nothing running and nothing to bring back
export type FreeMode = 'free' | 'start' | 'busy' | 'idle';

export const vmTransitions: VMPowerState[] = ['starting', 'pausing', 'resuming', 'stopping'];

export function freeMode(agents: T.Agent[], vm: VMPower | null, restorable: string[]): FreeMode {
  if (vm) {
    if (vmTransitions.includes(vm.state)) return 'busy';
    return vm.state === 'off' ? 'start' : 'free';
  }
  if (freeTargets(agents).length > 0) return 'free';
  return restartable(agents, restorable).length > 0 ? 'start' : 'idle';
}

// restartable is the agents Free resources stopped that are still there and
// still stopped: what Start brings back. One destroyed since, or started by
// hand, isn't.
export function restartable(agents: T.Agent[], restorable: string[]): string[] {
  return restorable.filter((ref) => agents.some((a) => a.ref === ref && a.state === 'stopped'));
}

// progress is how far a stop has got: of the targets, how many the daemon
// now reports stopped. Its events move the agents list as each one goes.
export function progress(targets: FreeTarget[], agents: T.Agent[]): { done: number; total: number } {
  const stopped = targets.filter((t) => {
    const a = agents.find((x) => x.ref === t.ref);
    return !a || a.state === 'stopped';
  });
  return { done: stopped.length, total: targets.length };
}

// Freed is what Free resources gave back to the host, as the result dialog
// shows it. In VM mode, the host gets back all the memory the VM held, not
// only what the agents inside it did, so that's the figure.
export interface Freed {
  memory: number; // bytes
  cpu: number; // percent; 100 is one full core
  hostBefore?: number;
  hostAfter?: number;
  vm?: number; // bytes the VM held and handed back
}

export function freed(result: T.StopAgentsResult | undefined, vmBefore: VMPower | null, vmStopped: boolean): Freed {
  const out: Freed = { memory: result?.freedMemory ?? 0, cpu: result?.freedCPU ?? 0 };
  if (result?.hostMemoryBefore && result.hostMemoryAfter) {
    out.hostBefore = result.hostMemoryBefore;
    out.hostAfter = result.hostMemoryAfter;
  }
  if (vmBefore && vmStopped) {
    out.vm = vmBefore.memoryGranted;
    out.memory = Math.max(out.memory, vmBefore.memoryGranted);
    // The host figures are the VM's own, inside it: not what the host got.
    delete out.hostBefore;
    delete out.hostAfter;
  }
  return out;
}

// coresText says a CPU figure (100 is one full core) the way the result
// reads it: "3.2 cores", "1 core", "0.4 of a core".
export function coresText(cpu: number): string {
  const cores = cpu / 100;
  if (cores < 0.05) return t('shell.free.noCpu');
  if (cores < 0.95) return t('shell.free.partCore', { n: formatNumber(cores, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) });
  const rounded = Math.round(cores * 10) / 10;
  return t('shell.free.cores', { count: rounded });
}

// whoText is the confirmation's line about what's being cut off: "2 are
// working and 1 is waiting on you", or null when nothing is.
export function whoText(targets: FreeTarget[]): string | null {
  const working = targets.filter((t) => t.doing === 'working').length;
  const asking = targets.filter((t) => t.doing === 'asking').length;
  const parts = [];
  if (working) parts.push(t('shell.free.working', { count: working }));
  if (asking) parts.push(t('shell.free.asking', { count: asking }));
  return parts.length ? formatList(parts) : null;
}

// The agents the last Free resources stopped, kept so Start can bring them
// back after the app restarts too.
const restorableKey = 'agentbox.freed';

export function loadRestorable(): string[] {
  try {
    const refs = JSON.parse(localStorage.getItem(restorableKey) ?? '[]') as unknown;
    return Array.isArray(refs) ? refs.filter((r): r is string => typeof r === 'string') : [];
  } catch {
    return [];
  }
}

export function saveRestorable(refs: string[]): void {
  if (refs.length) localStorage.setItem(restorableKey, JSON.stringify(refs));
  else localStorage.removeItem(restorableKey);
}
