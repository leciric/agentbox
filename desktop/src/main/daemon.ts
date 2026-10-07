// Talks to the AgentBox daemon over its unix socket, and starts the daemon
// through the CLI when it isn't running.
import { app } from 'electron';
import { execFile } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { agentboxBin } from './cli';
import { isLocal, requestOptions } from './connection';
import { canUseIncus, vmInitDone } from './hostsetup';
import { socketPath } from './paths';
import { t } from '../shared/i18n/index.ts';
import { NotListeningError, onWindows, relayRefused, startRelay } from './relay';
import { linuxVM, vmState } from './vmmode';
import { wslStoppedByUser } from './vmpower';

export { socketPath };

export interface ApiResponse {
  status: number;
  body: string;
  contentType: string;
}

// request sends an API request to the current environment: this machine's
// daemon, or one on a hub.
export function request(method: string, path: string, body?: unknown): Promise<ApiResponse> {
  const payload = body === undefined ? undefined : JSON.stringify(body);
  const { module, options } = requestOptions(path);
  const headers: Record<string, string | number> = {
    ...(options.headers as Record<string, string>),
    ...(payload === undefined ? {} : { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) }),
  };
  return new Promise((resolve, reject) => {
    const req = module.request({ ...options, method, headers }, (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (chunk: Buffer) => chunks.push(chunk));
      res.on('error', reject);
      res.on('end', () => {
        // On Windows the relay answers for a daemon it couldn't reach.
        if (onWindows && isLocal() && relayRefused(res.statusCode ?? 0, res.headers)) {
          let reason = "the AgentBox daemon isn't running";
          try {
            reason = JSON.parse(Buffer.concat(chunks).toString('utf8')).error ?? reason;
          } catch {
            // not JSON
          }
          return reject(new NotListeningError(reason));
        }
        resolve({
          status: res.statusCode ?? 0,
          body: Buffer.concat(chunks).toString('utf8'),
          contentType: res.headers['content-type'] ?? '',
        });
      });
    });
    req.on('error', reject);
    req.end(payload);
  });
}

// unreachable is the answer to a request that never reached a daemon: the
// error's message, as the daemon's own refusals give theirs.
export function unreachable(err: unknown): ApiResponse {
  const error = err instanceof Error ? err.message : String(err);
  return { status: 503, body: JSON.stringify({ error, code: 'daemon_unreachable' }), contentType: 'application/json' };
}

// notListening tells whether a request failed before it reached a daemon, so
// it's safe to start one and send the request again.
export function notListening(err: unknown): boolean {
  const code = (err as NodeJS.ErrnoException).code;
  return code === 'ENOENT' || code === 'ECONNREFUSED';
}

// reached is set once the app has talked to a daemon: from then on, in
// Linux VM mode, a VM that went off stays off until the user starts it.
let reached = false;

async function answers(): Promise<boolean> {
  try {
    const ok = (await request('GET', '/v1/version')).status === 200;
    reached ||= ok;
    return ok;
  } catch {
    return false;
  }
}

let starting: Promise<void> | undefined;
let quitting = false;

// ensureDaemon starts the daemon unless one already answers. AGENTBOX_BIN picks
// the agentbox binary; AGENTBOX_NO_AUTOSTART turns this off.
export function ensureDaemon(): Promise<void> {
  // An environment on a hub runs its own daemon; there's nothing to start here.
  if (!isLocal()) return Promise.resolve();
  // Starting it now would boot a VM the app is about to stop.
  if (quitting) return Promise.reject(new Error(t('web.main.quitting')));
  starting ??= start().finally(() => {
    starting = undefined;
  });
  return starting;
}

// stopStartingDaemon is for when the app quits: it starts no daemon from then
// on, and settles once a start already under way has finished.
export async function stopStartingDaemon(): Promise<void> {
  quitting = true;
  await starting?.catch(() => {});
}

// restartIfStale restarts a running daemon when this app would start a
// different one: a daemon that started before you joined incus-admin or kvm
// (a process keeps the groups it started with, and host setup adds them), one
// that started when this machine had no Incus to reach, or one of another
// version, after the app was updated. A daemon running jobs is left alone.
export async function restartIfStale(): Promise<void> {
  let info: { version?: string; groups?: number[]; incus?: boolean };
  try {
    info = JSON.parse((await request('GET', '/v1/version')).body);
  } catch {
    return; // not running: the first request starts the current one
  }
  const mine = new Set(process.getgroups?.() ?? []);
  // In Linux VM mode the daemon's groups and Incus are the VM's, not this
  // machine's: nothing here says whether it's stale.
  const vm = linuxVM();
  const missing = info.groups && !vm ? neededGroups().filter((g) => mine.has(g.gid) && !info.groups!.includes(g.gid)) : [];
  const ours = app.isPackaged ? app.getVersion() : undefined;
  const outdated = ours !== undefined && info.version !== undefined && info.version !== 'dev' && info.version !== ours;
  // Host setup's ACL on the Incus socket reaches processes already running, so
  // a daemon that still says no found nothing when it started, and a restart
  // is what it takes. A daemon too old to report this is left alone.
  const blind = info.incus === false && !vm && canUseIncus();
  if (missing.length === 0 && !outdated && !blind) return;
  await restart();
}

// restartDaemon restarts the daemon whatever it reports, for after host setup:
// the setup it ran is exactly what a daemon started earlier can't see. It
// answers false when the daemon is busy with jobs, which are left alone.
export async function restartDaemon(): Promise<boolean> {
  if (!isLocal()) return false;
  if (!(await answers())) {
    await ensureDaemon();
    return true;
  }
  return restart();
}

// stopHostDaemon stops this Linux machine's own daemon, for `agentbox vm init`
// to switch the machine to a VM: the VM's daemon answers on the same socket,
// and vm init won't switch while this one does. A daemon running jobs is left
// alone, and the switch waits.
export async function stopHostDaemon(onOutput: (text: string) => void): Promise<void> {
  if (!(await answers())) return;
  const jobs = JSON.parse((await request('GET', '/v1/jobs')).body) as { status: string }[];
  if (jobs.some((job) => job.status === 'running')) {
    throw new Error(t('web.main.daemonRunningJobs'));
  }
  onOutput("$ agentbox daemon stop    # this machine's own daemon, for the VM's\n");
  await request('POST', '/v1/shutdown');
  for (let i = 0; i < 300 && (await answers()); i++) await new Promise((resolve) => setTimeout(resolve, 100));
  if (await answers()) throw new Error(t('web.main.daemonDidntStop'));
}

// restart stops the running daemon and starts this app's own, unless it has
// jobs going.
async function restart(): Promise<boolean> {
  const jobs = JSON.parse((await request('GET', '/v1/jobs')).body) as { status: string }[];
  if (jobs.some((job) => job.status === 'running')) return false;
  await request('POST', '/v1/shutdown');
  for (let i = 0; i < 300 && (await answers()); i++) await new Promise((resolve) => setTimeout(resolve, 100));
  await ensureDaemon();
  return true;
}

function neededGroups(): { name: string; gid: number }[] {
  try {
    return readFileSync('/etc/group', 'utf8')
      .split('\n')
      .map((line) => line.split(':'))
      .filter((fields) => ['incus-admin', 'incus', 'kvm'].includes(fields[0]))
      .map((fields) => ({ name: fields[0], gid: Number(fields[2]) }));
  } catch {
    return [];
  }
}

// start runs `agentbox daemon start`, which starts the daemon in its own session
// without this process's file descriptors. A daemon started straight from here
// would inherit Chromium's pipes and keep the app from exiting.
//
// On a Mac, and on Linux in VM mode (vmmode.ts), that command is the front end
// of AgentBox's Linux VM: it boots the VM when it's stopped and brings the
// VM's agentbox up to date first, which takes longer than starting a daemon
// does. On Linux, a VM the user stopped or paused once the app had reached
// its daemon is left so: the error says why there's no daemon.
async function start(): Promise<void> {
  // `agentbox vm init` from the Setup page stopped this machine's own daemon
  // to switch it to a VM: the daemon to start is the VM's, when it's done.
  await vmInitDone();
  const bin = agentboxBin();
  if (onWindows) await startRelay(bin);
  if (await answers()) return;
  if (process.env.AGENTBOX_NO_AUTOSTART) {
    throw new Error(t('web.main.daemonNotRunning', { socket: socketPath }));
  }
  // Free resources stopped AgentBox's WSL distro: only the user starts it
  // again, from the top bar, or the app's next launch does.
  if (wslStoppedByUser()) throw new Error(t('web.main.wslStopped'));
  const vm = linuxVM();
  if (vm) {
    const state = await vmState();
    if (state === 'paused') throw new Error(t('web.main.vmPaused'));
    // Once the app has had a daemon, only the user brings the VM back: a VM
    // on its way down (Free resources, `agentbox vm stop`) is stopping, not
    // off, and `daemon start` would boot it again the moment it's off.
    if (reached && state !== 'running' && state !== 'starting') {
      throw new Error(t('web.main.vmNotRunning', { state }));
    }
  }
  // On Windows, agentbox.exe starts the daemon in AgentBox's WSL distro, which
  // may have to boot first, systemd and Incus with it.
  await new Promise<void>((resolve, reject) => {
    const timeout = process.platform === 'darwin' || vm ? 300_000 : onWindows ? 180_000 : 30_000;
    execFile(bin, ['daemon', 'start'], { timeout, windowsHide: true }, (err, _stdout, stderr) => {
      if (!err) return resolve();
      const notFound = (err as NodeJS.ErrnoException).code === 'ENOENT';
      reject(new Error(notFound ? t('web.main.binaryNotFound', { bin }) : stderr.trim().replace(/^error: /, '') || err.message));
    });
  });
}
