// How long a request to the daemon may go without a byte either way before
// the app gives up on it. Without one, a daemon stuck behind a VM short of
// memory or a disk the builds keep busy left every view that asked waiting
// for good: the window looked frozen though only the daemon was. A timeout
// answers 504 daemon_timeout instead; the views keep showing what they had,
// with the top bar's busy mark (renderer/lib/daemonBusy.ts), and ask again on
// their next poll. It is an idle timeout, not a deadline: a large download
// that keeps sending goes on.

// readIdleMs is for reads, which the daemon answers from its database or
// memory: anything this slow is stuck. writeIdleMs is for everything else,
// which may do real work before it answers (a merge, a snapshot), and was
// never meant to wait forever either.
export const readIdleMs = 30_000;
export const writeIdleMs = 5 * 60_000;

export function idleTimeout(method: string): number {
  return method === 'GET' || method === 'HEAD' ? readIdleMs : writeIdleMs;
}

// DaemonTimeoutError is a request that hit its idle timeout.
export class DaemonTimeoutError extends Error {
  readonly code = 'ETIMEDOUT';
  constructor(method: string, path: string, ms: number) {
    super(`the AgentBox daemon didn't answer ${method} ${path.split('?')[0]} within ${Math.round(ms / 1000)} s`);
    this.name = 'DaemonTimeoutError';
  }
}

// timedOut is the answer to a request that timed out, as the daemon gives
// its own errors: the renderer tells it from a refusal by its code.
export function timedOut(err: DaemonTimeoutError): { status: number; body: string; contentType: string } {
  return { status: 504, body: JSON.stringify({ error: err.message, code: 'daemon_timeout' }), contentType: 'application/json' };
}
