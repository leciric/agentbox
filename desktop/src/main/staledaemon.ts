// A daemon of another version than this app, still running after the app was
// updated. daemon.ts's restartIfStale restarts it, but not while it runs jobs,
// which a restart would cut short: until then the window says so
// (StaleDaemonNotice) rather than leaving the user with the 404s of a daemon
// that has never heard of the app's newer requests. It used to be looked at
// only as the app started, so an update that landed while a job ran (a lead
// creating agents, an image build) left the old daemon answering for good.
//
// This file holds no Electron, so its tests run under node.

export interface StaleDaemon {
  daemon: string; // the daemon's version
  app: string; // this app's
  jobs: number; // running jobs, which the restart waits for; 0 when it restarted and is still that version
}

// How often a stale daemon busy with jobs is asked again whether they're done.
export const staleRetryMs = 15_000;

// isOutdated says whether a daemon of version theirs is another version than
// this app's (ours, undefined when the app isn't packaged and so has none).
export function isOutdated(theirs: string | undefined, ours: string | undefined): boolean {
  return ours !== undefined && theirs !== undefined && theirs !== 'dev' && theirs !== ours;
}

export interface StaleDeps {
  version(): Promise<string | undefined>; // the running daemon's
  runningJobs(): Promise<number>;
  restart(): Promise<void>; // stop the daemon, jobs or not, and start this app's
  publish(stale: StaleDaemon | null): void;
  later(fn: () => void, ms: number): () => void; // answers how to cancel
}

export class StaleTracker {
  private stale: StaleDaemon | null = null;
  private cancel?: () => void;
  // failedFrom is the version of a daemon that was restarted and came back
  // the same (its binary wasn't replaced): restarting it again would only loop.
  private failedFrom?: string;

  private readonly app: string;
  private readonly deps: StaleDeps;
  private readonly again: () => void; // looks again, as restartIfStale does

  constructor(app: string, deps: StaleDeps, again: () => void) {
    this.app = app;
    this.deps = deps;
    this.again = again;
  }

  get current(): StaleDaemon | null {
    return this.stale;
  }

  // clear is a daemon of the app's version.
  clear(): void {
    this.stopRetry();
    this.failedFrom = undefined;
    this.set(null);
  }

  // handle is a daemon of version daemon, another than the app's: it's
  // restarted at once when it has no jobs, and otherwise looked at again in
  // staleRetryMs, shown as stale meanwhile.
  async handle(daemon: string): Promise<void> {
    this.stopRetry();
    if (this.failedFrom === daemon) return this.set({ daemon, app: this.app, jobs: 0 });
    const jobs = await this.deps.runningJobs();
    if (jobs > 0) {
      this.set({ daemon, app: this.app, jobs });
      this.cancel = this.deps.later(this.again, staleRetryMs);
      return;
    }
    await this.restart();
  }

  // restartNow restarts a stale daemon whatever it's doing: the window's
  // "Restart now", for a job that won't end or a user who won't wait.
  async restartNow(): Promise<void> {
    if (!this.stale) return;
    this.stopRetry();
    this.failedFrom = undefined;
    await this.restart();
  }

  private async restart(): Promise<void> {
    await this.deps.restart();
    const now = await this.deps.version().catch(() => undefined);
    if (isOutdated(now, this.app)) {
      this.failedFrom = now;
      this.set({ daemon: now!, app: this.app, jobs: 0 });
    } else {
      this.failedFrom = undefined;
      this.set(null);
    }
  }

  private stopRetry(): void {
    this.cancel?.();
    this.cancel = undefined;
  }

  private set(next: StaleDaemon | null): void {
    if (JSON.stringify(next) === JSON.stringify(this.stale)) return;
    this.stale = next;
    this.deps.publish(next);
  }
}
