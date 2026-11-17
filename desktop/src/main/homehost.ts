// The Home chat's AI tool runs on this computer, as the user, rather than in
// AgentBox's VM with the daemon (internal/homehost): `agentbox home-host`
// connects to the daemon over its socket and starts the tool here when the
// daemon asks. It is a child of the app and ends with it (its stdin closes);
// while the app is closed the Home chat runs in the VM, and says so. It is
// started again if it exits on its own.
import { type ChildProcess, spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { agentboxBin } from './cli';
import { socketPath } from './paths';
import { onWindows, startRelay } from './relay';

let child: ChildProcess | undefined;
let stopped = false;
let retry = 1_000;

export async function startHomeHost(): Promise<void> {
  if (child || stopped || process.env.AGENTBOX_NO_HOME_HOST) return;
  const bin = agentboxBin();
  // On Windows the daemon is reached through the app's relay pipe (relay.ts).
  const socket = onWindows ? await startRelay(bin).catch(() => undefined) : socketPath;
  if (!socket) return again();
  const proc = spawn(bin, ['home-host', '--with-app'], {
    stdio: ['pipe', 'ignore', 'pipe'],
    windowsHide: true,
    env: { ...process.env, AGENTBOX_HOST_SOCKET: socket },
  });
  child = proc;
  createInterface({ input: proc.stderr! }).on('line', (line) => console.log(line));
  proc.on('error', (err) => console.error('agentbox home-host:', err.message));
  proc.on('exit', (code) => {
    if (child !== proc) return;
    child = undefined;
    console.error(`agentbox home-host exited (${code ?? 'signal'})`);
    again();
  });
}

function again(): void {
  if (stopped) return;
  setTimeout(() => void startHomeHost(), retry);
  retry = Math.min(retry * 2, 60_000);
}

// stopHomeHost ends it, for when the app quits.
export function stopHomeHost(): void {
  stopped = true;
  child?.stdin?.end();
  child = undefined;
}
