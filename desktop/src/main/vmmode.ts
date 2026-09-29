// Whether this Linux machine runs AgentBox itself (host setup: Incus on the
// machine) or in a VM of its own (`agentbox vm init`: the daemon, Incus and
// every agent in one Cloud Hypervisor VM, with this app and the command-line
// tool as its front end, the way a Mac runs it in Lima). The user chooses at
// setup, on the Setup page (hostsetup.ts).
//
// In VM mode:
//   - The host has no Incus, so nothing here asks for host setup: the daemon's
//     own checks come from the daemon in the VM.
//   - `agentbox daemon start` goes through the front end, which boots the VM
//     first when it's off: the app starts it that way when it finds no daemon
//     at launch, as it does on a Mac (daemon.ts). Once the app has reached a
//     daemon, a VM that went off or was paused (the top bar's Free resources,
//     main/vmpower.ts) stays that way until the user starts it again: the app
//     doesn't boot it behind their back to reconnect its event stream.
//   - Quitting the app leaves the VM running, as quitting leaves the daemon
//     and the agents running on a machine that runs AgentBox itself; on a Mac
//     quitting stops the VM (index.ts).
import { execFile } from 'node:child_process';
import http from 'node:http';
import { join } from 'node:path';
import type { VMStatus } from '../shared/api';
import { agentboxBin } from './cli';
import { dataDir } from './paths';

const onLinux = process.platform === 'linux';

// vmSocket is where the VM's supervisor serves its state while it runs
// (paths.VMSocket).
export const vmSocket = join(dataDir, 'run', 'vm.sock');

let mode: 'host' | 'vm' | undefined;

// linuxVM reports whether this is a Linux machine in VM mode, as last learned.
export function linuxVM(): boolean {
  return onLinux && mode === 'vm';
}

// learnMode asks the command-line tool which mode this machine is in: once
// when the app starts, and again after a setup run, which may have switched
// it. A tool that can't say (one from before VM mode) is host mode.
export function learnMode(): Promise<void> {
  if (!onLinux) return Promise.resolve();
  return new Promise((resolve) => {
    execFile(agentboxBin(), ['vm', 'status', '--json'], { timeout: 15_000 }, (_err, stdout) => {
      try {
        mode = (JSON.parse(stdout) as VMStatus).mode === 'vm' ? 'vm' : 'host';
      } catch {
        mode ??= 'host';
      }
      resolve();
    });
  });
}

// vmState is the VM's state (api.VMStatus's), as its supervisor says:
// "off" when no supervisor answers, which is a VM that isn't running.
export function vmState(): Promise<string> {
  return new Promise((resolve) => {
    const req = http.get({ socketPath: vmSocket, path: '/v1/vm', timeout: 2_000 }, (res) => {
      let body = '';
      res.setEncoding('utf8');
      res.on('data', (chunk: string) => (body += chunk));
      res.on('end', () => {
        try {
          resolve(res.statusCode === 200 ? (JSON.parse(body) as VMStatus).state : 'off');
        } catch {
          resolve('off');
        }
      });
      res.on('error', () => resolve('off'));
    });
    req.on('timeout', () => req.destroy(new Error('timed out')));
    req.on('error', () => resolve('off'));
  });
}
