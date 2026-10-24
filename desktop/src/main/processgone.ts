// Chromium's processes that die: a window's renderer (webContents'
// render-process-gone), whose page goes blank with no JavaScript left to
// report it, and the GPU process or another child (app's child-process-gone).
// The main process sees each, logs it, sends it as an error report
// (reportqueue.ts) and, for a window or the GPU, offers to reload the window.
import type * as T from '../shared/api';

export interface GoneProcess {
  // 'window' for a window's renderer, else Electron's type of child process:
  // 'GPU', 'Utility', 'Zygote' and others.
  process: string;
  reason: string; // Electron's: 'crashed', 'oom', 'killed', 'abnormal-exit', 'launch-failed', ...
  exitCode: number;
  name?: string; // a utility process's, like 'Network Service'
}

// worthReporting leaves out a process that ended as it should, and every one
// that goes while the app quits, when Chromium stops them all.
export function worthReporting(gone: GoneProcess, quitting: boolean): boolean {
  return !quitting && gone.reason !== 'clean-exit';
}

// offersReload is whether the window is offered a reload: its own page is
// gone, or may be drawn by nobody.
export function offersReload(gone: GoneProcess): boolean {
  return gone.process === 'window' || gone.process === 'GPU';
}

// describeGone is one line about it, for the log and the report.
export function describeGone(gone: GoneProcess): string {
  const what = gone.process === 'window' ? "The window's renderer process" : `Chromium's ${gone.process} process${gone.name ? ` (${gone.name})` : ''}`;
  return `${what} is gone: ${gone.reason}, exit code ${gone.exitCode}`;
}

// goneSignature tells them apart: the same process gone the same way is one.
export function goneSignature(gone: GoneProcess): string {
  return `${gone.process}|${gone.name ?? ''}|${gone.reason}|${gone.exitCode}`;
}

// goneReport is the error report of it, with the app's own description
// beside it, like an uncaught error's (the renderer's lib/errorReports.ts).
export function goneReport(gone: GoneProcess, app?: T.ReportSection): T.ReportRequest {
  const line = describeGone(gone);
  const detail = [
    `Process:   ${gone.process}${gone.name ? ` (${gone.name})` : ''}`,
    `Reason:    ${gone.reason}`,
    `Exit code: ${gone.exitCode}`,
  ].join('\n');
  return {
    kind: 'error',
    message: line,
    sections: [{ id: 'error', title: gone.process === 'window' ? "The window's renderer, gone" : `Chromium's ${gone.process} process, gone`.slice(0, 80), content: `${line}\n\n${detail}` }, ...(app ? [app] : [])],
  };
}
