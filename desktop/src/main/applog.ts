// The app's own log, for problem reports: what the main process writes with
// console, and every uncaught error, of the main process and of its windows,
// in a capped file under the app's logs folder (main.log, rotated to
// main.log.1 at maxBytes), the way t3code keeps its desktop logs. Before this
// the main process only wrote to stdout, which nobody keeps.
//
// A report reads the end of it (reportSections), with the recent uncaught
// errors as a section of their own. The daemon redacts both, with the rest of
// the report (internal/report): nothing here needs to.
import { appendFileSync, existsSync, mkdirSync, readFileSync, renameSync, statSync } from 'node:fs';
import { release } from 'node:os';
import { dirname, join } from 'node:path';
import { format } from 'node:util';
import { describeError } from '../shared/errors.ts';

export interface AppError {
  at: string; // ISO time
  where: 'main' | 'window';
  name: string;
  message: string;
  stack?: string;
}

export interface ReportSection {
  id: string;
  title: string;
  content: string;
}

export const maxBytes = 1 << 20;
const maxErrors = 20;
// What a report keeps of the log's end: the daemon cuts each section to its
// own limit anyway (report.MaxSection).
const reportTail = 48 << 10;

export class AppLog {
  readonly file: string;
  readonly errors: AppError[] = [];
  private size = -1;

  constructor(dir: string) {
    this.file = join(dir, 'main.log');
  }

  // write appends one line, rotating the file first when it would grow past
  // maxBytes. Logging never throws: a disk that's full or gone loses lines.
  write(level: string, text: string): void {
    const line = `${new Date().toISOString()} ${level} ${text.replace(/\n(?!$)/g, '\n  ')}\n`;
    try {
      if (this.size < 0) {
        mkdirSync(dirname(this.file), { recursive: true });
        this.size = existsSync(this.file) ? statSync(this.file).size : 0;
      }
      if (this.size > 0 && this.size + line.length > maxBytes) {
        renameSync(this.file, `${this.file}.1`);
        this.size = 0;
      }
      appendFileSync(this.file, line);
      this.size += Buffer.byteLength(line);
    } catch {
      // nowhere to say so
    }
  }

  // error records an uncaught error of the main process, and logs it.
  error(where: AppError['where'], err: unknown): AppError {
    const entry: AppError = { at: new Date().toISOString(), where, ...describeError(err) };
    this.record(entry);
    return entry;
  }

  // windowError records an uncaught error a window reported, already
  // described there (the renderer's lib/errorReports.ts).
  windowError(e: Pick<AppError, 'name' | 'message' | 'stack'>): void {
    this.record({ at: new Date().toISOString(), where: 'window', name: String(e.name), message: String(e.message), stack: e.stack ? String(e.stack) : undefined });
  }

  private record(e: AppError): void {
    this.errors.push(e);
    if (this.errors.length > maxErrors) this.errors.shift();
    this.write('UNCAUGHT', `${e.where}: ${e.name}: ${e.message}${e.stack ? `\n${e.stack}` : ''}`);
  }

  // tail is the last bytes of the log, from main.log.1 too when main.log
  // alone is shorter.
  tail(bytes = reportTail): string {
    const read = (f: string) => {
      try {
        return readFileSync(f, 'utf8');
      } catch {
        return '';
      }
    };
    let text = read(this.file);
    if (text.length < bytes) text = read(`${this.file}.1`) + text;
    return text.length > bytes ? text.slice(text.length - bytes) : text;
  }
}

// errorsText is the recent uncaught errors, newest last, as a report shows them.
export function errorsText(errors: AppError[]): string {
  return errors
    .map((e) => `${e.at} in the ${e.where === 'main' ? 'main process' : 'window'}: ${e.name}: ${e.message}${e.stack ? `\n  ${e.stack.split('\n').join('\n  ')}` : ''}`)
    .join('\n\n');
}

export interface AppInfo {
  version: string;
  electron: string;
  chrome: string;
  platform: string;
  arch: string;
  target: string; // "this machine" or a hub
  vmMode: boolean;
}

// reportSections are the app's sections of a problem report.
export function reportSections(log: AppLog, info: AppInfo): ReportSection[] {
  const app = [
    `App:       ${info.version}`,
    `Electron:  ${info.electron} (Chrome ${info.chrome})`,
    `OS:        ${info.platform}/${info.arch} ${release()}`,
    `Connected: ${info.target}`,
    ...(info.platform === 'linux' ? [`Linux VM:  ${info.vmMode ? 'yes, AgentBox runs in its VM' : 'no'}`] : []),
  ].join('\n');
  const sections: ReportSection[] = [{ id: 'app', title: 'The desktop app', content: app }];
  if (log.errors.length > 0) sections.push({ id: 'app-errors', title: "The app's recent uncaught errors", content: errorsText(log.errors) });
  sections.push({ id: 'app-log', title: "The app's log (the end of main.log)", content: log.tail() || '(empty)' });
  return sections;
}

// captureConsole copies what the main process writes with console into log,
// as well as to stdout and stderr.
export function captureConsole(log: AppLog, c: Console = console): void {
  for (const level of ['log', 'info', 'warn', 'error'] as const) {
    const original = c[level].bind(c);
    c[level] = (...args: unknown[]) => {
      log.write(level.toUpperCase(), format(...args));
      original(...args);
    };
  }
}
