// Automatic error reports: what the app does with an uncaught error, of a
// window or of the main process. Each one goes to the main process's log
// (main/applog.ts), where a problem report finds it. Beyond that, nothing is
// sent unless the user said so (Settings.ErrorReports): the first time an
// error happens and they haven't been asked, the app asks
// (components/ErrorReports.tsx); once they said yes, each new error is sent
// by itself, as a report of kind "error" through the daemon, which redacts it
// like any report (internal/report).
import type * as T from '../../shared/api';
import { describeError, frames, type DescribedError } from '../../shared/errors.ts';

export interface UncaughtError extends DescribedError {
  where: 'main' | 'window';
}

// noise are errors that say nothing is wrong: Chrome's ResizeObserver
// warnings, which reach window.onerror though nothing threw.
const noise = [/^ResizeObserver loop/];

// The most error reports one run of the app sends: a broken loop shouldn't
// send a report a second.
export const maxPerSession = 5;

// fromEvent is a window's uncaught error, from window.onerror's event or
// an unhandled rejection's.
export function fromEvent(event: ErrorEvent | PromiseRejectionEvent): UncaughtError | null {
  if ('reason' in event) return { where: 'window', ...describeError(event.reason) };
  if (event.error !== undefined && event.error !== null) return { where: 'window', ...describeError(event.error) };
  // A script error with no Error, like one from another origin.
  const at = event.filename ? `\n  at ${event.filename}:${event.lineno}:${event.colno}` : '';
  return { where: 'window', name: 'Error', message: event.message || 'unknown error', stack: frames(at) };
}

export type Decision = 'send' | 'ask' | 'ignore';

// ErrorReporter decides what each uncaught error leads to: sent, the user
// asked, or nothing, because it's noise, was already handled in this run, or
// this run sent enough.
export class ErrorReporter {
  private seen = new Set<string>();
  private sent = 0;
  private asked = false;

  decide(err: UncaughtError, settings: Pick<T.Settings, 'errorReports' | 'errorReportsAsked'> | undefined): Decision {
    if (noise.some((re) => re.test(err.message))) return 'ignore';
    const key = signature(err);
    if (this.seen.has(key)) return 'ignore';
    this.seen.add(key);
    if (!settings) return 'ignore'; // no daemon to send it through, or to ask about it
    if (settings.errorReports) {
      if (this.sent >= maxPerSession) return 'ignore';
      this.sent++;
      return 'send';
    }
    if (settings.errorReportsAsked || this.asked) return 'ignore';
    this.asked = true;
    return 'ask';
  }
}

// signature tells errors apart: the same error from the same place is one.
export function signature(err: UncaughtError): string {
  return `${err.where}|${err.name}|${err.message}|${err.stack?.split('\n')[0] ?? ''}`;
}

// errorReport is the report an error sends: the error and its stack, and
// the app's own description (the "app" section), nothing more: no logs.
export function errorReport(err: UncaughtError, app?: T.ReportSection): T.ReportRequest {
  const where = err.where === 'main' ? 'the main process' : 'the window';
  return {
    kind: 'error',
    message: `${err.name}: ${err.message}`.slice(0, 1000),
    sections: [
      { id: 'error', title: `The error, in ${where}`, content: `${err.name}: ${err.message}${err.stack ? `\n${err.stack}` : ''}` },
      ...(app ? [app] : []),
    ],
  };
}
