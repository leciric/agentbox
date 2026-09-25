// On Windows the daemon's unix socket is inside AgentBox's WSL distro, where
// Windows can't open it (WSL 2 carries files and TCP across, not AF_UNIX). The
// app reaches it through `agentbox.exe relay` (internal/hostwsl/relay.go): a
// named pipe with a random name that only this Windows user may open, carried
// over one wsl.exe into the distro and on to the socket, as the distro's user.
// Every request and stream connects to that pipe the way it would connect to
// the socket on Linux, so the rest of the app doesn't care which it is (D94).
//
// The relay is a child of the app, and ends when the app does: it serves until
// its stdin closes.
import { type ChildProcess, spawn } from 'node:child_process';
import { createInterface } from 'node:readline';

export const onWindows = process.platform === 'win32';

// The header the relay marks its own answers with, when no daemon saw the
// request: the distro isn't there or the daemon isn't listening in it.
const relayHeader = 'x-agentbox-relay';

let child: ChildProcess | undefined;
let pipe: string | undefined;
let starting: Promise<string> | undefined;

// relaySocket is where requests connect: the relay's pipe once it runs. Before
// then it is a pipe nobody listens on, so a request fails with ENOENT, which
// the app takes for a daemon that isn't running: ensureDaemon then starts the
// relay (startRelay) and the daemon, and the request is sent again.
export function relaySocket(): string {
  return pipe ?? '\\\\.\\pipe\\agentbox-relay-not-started';
}

// startRelay starts the relay unless it runs, and resolves to its pipe.
export function startRelay(bin: string): Promise<string> {
  if (pipe && child && child.exitCode === null) return Promise.resolve(pipe);
  starting ??= spawnRelay(bin).finally(() => {
    starting = undefined;
  });
  return starting;
}

function spawnRelay(bin: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const proc = spawn(bin, ['relay'], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
    let stderr = '';
    proc.stderr?.on('data', (chunk: Buffer) => {
      stderr = (stderr + chunk.toString('utf8')).slice(-2_000);
    });
    const lines = createInterface({ input: proc.stdout! });
    lines.once('line', (line) => {
      const m = /^listening (.+)$/.exec(line.trim());
      if (!m) return reject(new Error(`the relay said ${JSON.stringify(line)}`));
      child = proc;
      pipe = m[1];
      resolve(pipe);
    });
    proc.on('error', (err) => reject(new Error(`couldn't run ${bin} relay: ${err.message}`)));
    proc.on('exit', (code) => {
      if (child === proc) {
        child = undefined;
        pipe = undefined;
      }
      const last = stderr.trim().split('\n').filter(Boolean).at(-1)?.replace(/^error: /, '');
      reject(new Error(`the relay to AgentBox's WSL distro exited (${code ?? 'signal'})${last ? `: ${last}` : ''}`));
    });
  });
}

// stopRelay ends the relay, for when the app quits: closing its stdin is what
// it waits for.
export function stopRelay(): void {
  child?.stdin?.end();
  child = undefined;
  pipe = undefined;
}

// relayRefused tells whether an answer came from the relay rather than the
// daemon: HTTP 503 with the relay's header.
export function relayRefused(status: number, headers: Record<string, string | string[] | undefined>): boolean {
  return status === 503 && headers[relayHeader] === 'not-listening';
}

// NotListeningError is what a request the relay refused rejects with: the
// same code a unix socket with no daemon on it gives, so the app starts the
// daemon and sends the request again, as it does on Linux.
export class NotListeningError extends Error {
  code = 'ECONNREFUSED';
}
