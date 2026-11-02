// Automatic error reports leave the app here, whoever made them: a window's
// uncaught errors (the renderer's lib/errorReports.ts), the main process's,
// and Chromium's processes that died (processgone.ts). Each goes to the
// daemon, which checks the user has them on, redacts it and sends it
// (internal/daemon/report.go). One the daemon couldn't be reached for, while
// the VM starts or the daemon restarts, waits in a file under the app's data
// (error-reports.json, the newest maxQueued) and goes once it answers again,
// in this run or the next. One the daemon refused, because reports are off
// or it found the report wrong, is dropped: there's nobody to tell.
import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import type * as T from '../shared/api';
import { maxErrorReportsPerRun } from '../shared/errors.ts';

// The most reports waiting for the daemon: the oldest go first.
export const maxQueued = 20;
// The most error reports one run of the app sends, of every kind together.
// The daemon has its own for its crashes (internal/daemon/crashes.go).
export const maxPerRun = maxErrorReportsPerRun;

export type Outcome = 'sent' | 'refused' | 'unreachable';

// outcome is what the daemon's answer to POST /v1/reports means here. The
// main process answers a request that never reached a daemon with its own
// 503 (daemon.ts unreachable); a daemon's 503 is a refusal like any other.
export function outcome(res: { status: number; body: string }): Outcome {
  if (res.status >= 200 && res.status < 300) return 'sent';
  if (res.status === 503) {
    try {
      if ((JSON.parse(res.body) as { code?: string }).code === 'daemon_unreachable') return 'unreachable';
    } catch {
      // not JSON: a daemon's
    }
  }
  return 'refused';
}

interface Queued {
  id: string;
  at: string; // ISO time it was queued
  report: T.ReportRequest;
}

// ReportQueue is error-reports.json. Nothing here throws: a disk that's full
// or gone loses reports, as it would have without the queue.
export class ReportQueue {
  readonly file: string;

  constructor(file: string) {
    this.file = file;
  }

  read(): Queued[] {
    try {
      const q: unknown = JSON.parse(readFileSync(this.file, 'utf8'));
      return Array.isArray(q) ? q.filter((e: Queued) => typeof e?.id === 'string' && typeof e.report?.kind === 'string') : [];
    } catch {
      return [];
    }
  }

  private write(q: Queued[]): void {
    try {
      mkdirSync(dirname(this.file), { recursive: true });
      writeFileSync(`${this.file}.tmp`, JSON.stringify(q));
      renameSync(`${this.file}.tmp`, this.file);
    } catch {
      // nowhere to say so
    }
  }

  add(report: T.ReportRequest): void {
    const entry = { id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`, at: new Date().toISOString(), report };
    this.write([...this.read(), entry].slice(-maxQueued));
  }

  remove(id: string): void {
    this.write(this.read().filter((e) => e.id !== id));
  }
}

// ErrorReportSender sends each report, up to maxPerRun, queueing those the
// daemon can't be reached for.
export class ErrorReportSender {
  private sent = 0;
  private flushing?: Promise<void>;
  private queue: ReportQueue;
  private post: (report: T.ReportRequest) => Promise<Outcome>;

  constructor(queue: ReportQueue, post: (report: T.ReportRequest) => Promise<Outcome>) {
    this.queue = queue;
    this.post = post;
  }

  async send(report: T.ReportRequest): Promise<Outcome | 'capped'> {
    if (this.sent >= maxPerRun) return 'capped';
    this.sent++;
    const result = await this.post(report).catch((): Outcome => 'unreachable');
    if (result === 'unreachable') this.queue.add(report);
    return result;
  }

  // flush sends what waits, oldest first, until the daemon can't be reached
  // again. Reports queued in an earlier run don't count against this one's
  // cap: they did against theirs.
  flush(): Promise<void> {
    this.flushing ??= this.drain().finally(() => (this.flushing = undefined));
    return this.flushing;
  }

  private async drain(): Promise<void> {
    for (const entry of this.queue.read()) {
      const result = await this.post(entry.report).catch((): Outcome => 'unreachable');
      if (result === 'unreachable') return;
      this.queue.remove(entry.id);
    }
  }
}
