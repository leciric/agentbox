// Why `agentbox daemon start` failed, for the banner the app shows while it
// retries. Node's own message is "Command failed: <command>" and whatever was
// on stderr, which says nothing when the command said nothing there: wsl.exe
// prints its own errors to stdout, and a command the app gave up on (the
// timeout) said nothing at all (#433). So: stderr when there is any, and
// otherwise what happened to the command, with stdout's last line.
import type { ExecFileException } from 'node:child_process';
import { t } from '../shared/i18n/index.ts';

export function startFailure(err: ExecFileException, stdout: string, stderr: string, bin: string, timeout: number): string {
  if (err.code === 'ENOENT') return t('web.main.binaryNotFound', { bin });
  // stderr is progress ("Starting the AgentBox daemon…") above the error.
  const lines = stderr.trim().split(/\r?\n/);
  const at = lines.findLastIndex((line) => line.startsWith('error: '));
  if (at >= 0) return lines.slice(at).join('\n').replace(/^error: /, '').trim();
  const said = stderr.trim();
  if (said && !err.killed && typeof err.code !== 'number') return said;
  const last = lastLine(said) || lastLine(stdout);
  const why = last ? `: ${last}` : '';
  const command = `${bin} daemon start`;
  if (err.killed) return t('web.main.startTimedOut', { command, seconds: Math.round(timeout / 1000) }) + why;
  if (typeof err.code === 'number') return t('web.main.startExited', { command, code: exitCode(err.code) }) + why;
  if (err.signal) return t('web.main.startKilled', { command, signal: err.signal }) + why;
  return err.message.trim() + why;
}

// progressStep is the last thing a start said it's doing: the last line of
// its stderr that isn't an error.
export function progressStep(stderr: string): string {
  return lastLine(
    stderr
      .split(/\r?\n/)
      .filter((line) => !line.trim().startsWith('error:'))
      .join('\n'),
  );
}

function lastLine(s: string): string {
  return (
    s
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .pop() ?? ''
  );
}

// exitCode shows a Windows error code (wsl.exe's, or a crash's, such as
// 0xC0000005) the way Windows writes them, and a plain status as itself.
function exitCode(code: number): string {
  if (code >= 0 && code <= 255) return String(code);
  return `0x${(code >>> 0).toString(16).toUpperCase().padStart(8, '0')}`;
}
