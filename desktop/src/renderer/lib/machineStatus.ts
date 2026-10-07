// What the top bar says about the machine the agents run on — AgentBox's VM,
// or this computer in host mode — in one word: the worst of its power state,
// the app's connection to the daemon in it, and its memory. Worked out apart
// from the components so it can be tested without a DOM.
import type { ConnectionState, VMPower } from '../../preload';
import type * as T from '../../shared/api';

export type MachineKind =
  | 'running' // up, connected, with room
  | 'memory' // up, connected, its memory nearly full
  | 'connecting' // up, the daemon not answering yet
  | 'offline' // up, the connection to the daemon lost
  | 'paused'
  | 'off'
  | 'busy'; // starting, pausing, resuming or stopping

export type MachineTone = 'ok' | 'warn' | 'bad' | 'busy' | 'off';

export interface MachineStatus {
  kind: MachineKind;
  tone: MachineTone;
  // memory is what's in use against what the VM is granted now (it grows by
  // virtio-mem up to its cap as agents start; the cap itself where nothing is
  // granted, and on Lima/WSL where the two are equal), or this computer's
  // memory in host mode. cap is the most the VM can grow to, 0 in host mode.
  // Null while it isn't known.
  memory: { used: number; total: number; cap: number; percent: number } | null;
}

// memoryFull is the share of memory past which the top bar calls it tight,
// the same as its meters' rose.
export const memoryFull = 85;

export function machineStatus(vm: VMPower | null, connection: ConnectionState, host?: T.HostUsage): MachineStatus {
  const memory = memoryOf(vm, host);
  const of = (kind: MachineKind, tone: MachineTone): MachineStatus => ({ kind, tone, memory });
  if (vm) {
    if (vm.state === 'off') return of('off', vm.error ? 'bad' : 'off');
    if (vm.state === 'paused') return of('paused', 'warn');
    if (vm.state !== 'running') return of('busy', 'busy');
  }
  if (connection.state === 'disconnected') return of('offline', 'bad');
  if (connection.state === 'connecting') return of('connecting', 'busy');
  if (memory && memory.percent > memoryFull) return of('memory', 'warn');
  return of('running', 'ok');
}

function memoryOf(vm: VMPower | null, host?: T.HostUsage): MachineStatus['memory'] {
  const cap = vm ? Math.max(vm.memoryCap, vm.memoryGranted) : 0;
  const [used, total] = vm ? [vm.memoryUsed, vm.memoryGranted > 0 ? vm.memoryGranted : cap] : [host?.memUsed ?? 0, host?.memTotal ?? 0];
  if (total <= 0 || (vm && vm.state !== 'running')) return null;
  return { used, total, cap, percent: Math.max(0, Math.min(1, used / total)) * 100 };
}
