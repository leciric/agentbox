// The power of AgentBox's VM, in VM mode: the daemon, Incus and every agent
// inside one Cloud Hypervisor VM on Linux, with this app as a front end. The
// daemon can't turn off the machine it runs in, or answer while that machine
// is off or paused, so the app asks the command-line tool on the host, the
// way it asks it to set the host up (hostsetup.ts).
//
// The contract with the command-line tool, until VM mode lands:
//
//   agentbox vm power --json    prints a VMPower (preload.ts) and exits 0, or
//                               {"mode":"host"} when AgentBox isn't in VM mode
//   agentbox vm start           each returns once the VM is in its new state;
//   agentbox vm pause           start and resume only once the daemon inside
//   agentbox vm resume          answers. On failure, exit non-zero with why as
//   agentbox vm stop            the last line of stderr
//
// A tool without `vm power` (every one before VM mode) is host mode. The mode
// is asked once and kept, until a setup run switches the machine to a VM.
//
// AGENTBOX_FAKE_VM=1 stands a made-up VM in for the real one, to see the top
// bar's VM controls working in `npm start` before VM mode exists; the daemon
// stays up whatever the fake VM's state says.
import { execFile } from 'node:child_process';
import { ipcMain } from 'electron';
import type { VMPower, VMPowerAction, VMPowerState } from '../preload';
import { agentboxBin } from './cli';
import { linuxVM } from './vmmode';

const onLinux = process.platform === 'linux';

// mode is what the tool said about VM mode: unknown until it has answered.
// Only a VM the tool has already reported can be shown as broken; a tool that
// fails before ever answering (none installed, one from before VM mode) is
// host mode, so a normal Linux install never shows a VM it doesn't have.
let mode: 'unknown' | 'host' | 'vm' = onLinux ? 'unknown' : 'host';
// pending is the action in flight, if any: while it runs, the state is the
// transition it makes (starting, stopping…), whatever the tool last said.
let pending: VMPowerAction | undefined;

const transition: Record<VMPowerAction, VMPowerState> = {
  start: 'starting',
  pause: 'pausing',
  resume: 'resuming',
  stop: 'stopping',
};

export async function vmPower(): Promise<VMPower | null> {
  if (process.env.AGENTBOX_FAKE_VM) return fake.power();
  // Setup's "Run in a VM" switches a Linux machine to VM mode while the app
  // runs: vmmode.ts learns it again after a setup run.
  if (mode === 'host' && !linuxVM()) return null;
  let parsed: VMPower | { mode: 'host' };
  try {
    parsed = JSON.parse(await run(['vm', 'power', '--json'])) as VMPower | { mode: 'host' };
  } catch (err) {
    if (mode === 'unknown') {
      mode = 'host';
      return null;
    }
    return { state: 'off', memoryUsed: 0, memoryGranted: 0, memoryCap: 0, cpus: 0, error: err instanceof Error ? err.message : String(err) };
  }
  if ('mode' in parsed && parsed.mode === 'host') {
    mode = 'host';
    return null;
  }
  mode = 'vm';
  const power = parsed as VMPower;
  return pending ? { ...power, state: transition[pending] } : power;
}

export async function actOnVM(action: VMPowerAction): Promise<VMPower> {
  if (process.env.AGENTBOX_FAKE_VM) return fake.act(action);
  if (pending) throw new Error(`The VM is already ${transition[pending]}`);
  pending = action;
  try {
    await run(['vm', action]);
  } finally {
    pending = undefined;
  }
  const power = await vmPower();
  if (!power) throw new Error('AgentBox is not in VM mode');
  return power;
}

function run(args: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(agentboxBin(), args, { timeout: 5 * 60_000 }, (err, stdout, stderr) => {
      if (err) {
        const last = stderr.trim().split('\n').pop()?.replace(/^error: /, '');
        reject(new Error(last || err.message));
        return;
      }
      resolve(stdout);
    });
  });
}

ipcMain.handle('vm:power', () => vmPower());
ipcMain.handle('vm:act', (_event, action: VMPowerAction) => actOnVM(action));

// fake is AGENTBOX_FAKE_VM's VM: 24 GiB at most, and a few seconds for each
// transition, so every state shows long enough to see.
const fake = (() => {
  const GiB = 1024 ** 3;
  let state: VMPowerState = 'running';
  let granted = 12 * GiB;
  const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
  const power = (): VMPower => {
    const up = state !== 'off' && state !== 'starting';
    return {
      state,
      memoryUsed: up ? Math.round(granted * 0.62) : 0,
      memoryGranted: up ? granted : 0,
      memoryCap: 24 * GiB,
      cpus: 8,
    };
  };
  const act = async (action: VMPowerAction): Promise<VMPower> => {
    state = transition[action];
    await sleep(action === 'pause' || action === 'resume' ? 800 : 2500);
    state = action === 'pause' ? 'paused' : action === 'stop' ? 'off' : 'running';
    if (action === 'start') granted = 8 * GiB;
    return power();
  };
  return { power, act };
})();
