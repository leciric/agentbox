// Reading a Windows browser's cookies on the host, for a browser-cookie
// import. Chrome, Edge and Brave on Windows seal their cookies with app-bound
// encryption (v20): the key is bound to the browser and the Windows user, so
// only the browser itself can open them, and the Go daemon in AgentBox's VM
// can't (internal/cookieimport lists these as app-bound profiles but refuses
// to read them). This module is the one piece the host must do: it launches
// the browser's own exe, headless, against a throwaway copy of the profile,
// and reads the decrypted cookies over the DevTools protocol. We speak CDP over
// a pipe (--remote-debugging-pipe: the browser reads commands on fd 3 and
// writes replies on fd 4), not a loopback debugging port, because a port is
// open to every other process on the machine while the browser holds every
// cookie decrypted; the pipe is private to this process. This is Puppeteer's
// `pipe: true` transport. The cookies are handed straight to the daemon's
// decrypted-import endpoint and never stored or logged here.
//
// It runs only on win32 (elsewhere the daemon reads cookies itself); on any
// other platform, and whenever anything goes wrong, extractBrowserCookies
// returns an error code the renderer turns into a message, never a value.
import { execFile, spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { copyFile, mkdir, mkdtemp, open, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';
import type { Readable, Writable } from 'node:stream';
import type { IpcMain } from 'electron';
import type { ImportedCookie } from '../shared/api';

// ExtractResult is what the host hands back: the decrypted cookies, or a code
// the renderer maps to a translated message. 'running' means the browser is
// open (often in the background) or its profile couldn't be copied; 'notInstalled' that the
// browser's exe or user data wasn't found; 'failed' anything else.
export type ExtractResult = { cookies: ImportedCookie[] } | { error: 'running' | 'notInstalled' | 'failed' };

// A browser's user-data directory and the places its exe may be installed, on
// Windows. env() reads a Windows environment variable, defaulting to ''.
function env(name: string): string {
  return process.env[name] ?? '';
}

export interface BrowserSpec {
  userDataDir: string;
  exes: string[];
}

function browserSpecs(): Record<string, BrowserSpec> {
  const local = env('LOCALAPPDATA');
  const pf = env('ProgramFiles');
  const pf86 = env('ProgramFiles(x86)');
  return {
    chrome: {
      userDataDir: join(local, 'Google', 'Chrome', 'User Data'),
      exes: [
        join(pf, 'Google', 'Chrome', 'Application', 'chrome.exe'),
        join(pf86, 'Google', 'Chrome', 'Application', 'chrome.exe'),
        join(local, 'Google', 'Chrome', 'Application', 'chrome.exe'),
      ],
    },
    edge: {
      userDataDir: join(local, 'Microsoft', 'Edge', 'User Data'),
      exes: [
        join(pf86, 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
        join(pf, 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
      ],
    },
    brave: {
      userDataDir: join(local, 'BraveSoftware', 'Brave-Browser', 'User Data'),
      exes: [
        join(pf, 'BraveSoftware', 'Brave-Browser', 'Application', 'brave.exe'),
        join(pf86, 'BraveSoftware', 'Brave-Browser', 'Application', 'brave.exe'),
        join(local, 'BraveSoftware', 'Brave-Browser', 'Application', 'brave.exe'),
      ],
    },
  };
}

// browserRunning reports whether the browser is open, which must gate the
// read: a running browser holds its cookie store mid-write, and a copy of it
// can come out empty or torn, which then reads as "no cookies". Chrome, Edge
// and Brave on Windows keep running in the background after their last window
// closes (their "continue running background apps" setting), so "closed" is
// often not. Two signals, either enough: the user-data directory's lockfile,
// which the running browser holds open without write sharing (Chromium's
// process singleton; it's deleted on exit, so one we can open is a crash's
// leftover), and a process running from this browser's own exe, so Chrome
// Canary or another browser never blocks it.
export async function browserRunning(spec: BrowserSpec, paths: () => Promise<string[]> = () => processPaths(basename(spec.exes[0]))): Promise<boolean> {
  if (await lockHeld(spec.userDataDir)) return true;
  return exeRunning(await paths(), spec.exes);
}

// lockHeld reports whether a browser holds the user-data directory's lockfile:
// it exists and can't be opened for writing.
export async function lockHeld(udd: string, openFile: typeof open = open): Promise<boolean> {
  const lock = join(udd, 'lockfile');
  if (!existsSync(lock)) return false;
  try {
    await (await openFile(lock, 'r+')).close();
    return false;
  } catch (err) {
    return ['EBUSY', 'EPERM', 'EACCES'].includes((err as NodeJS.ErrnoException).code ?? '');
  }
}

// processPaths lists the exe path of every running process with this image
// name (chrome.exe), '' where Windows won't say (another user's, elevated).
// It answers [] when it can't tell: the lockfile is the main signal.
function processPaths(image: string): Promise<string[]> {
  const cmd = `Get-CimInstance Win32_Process -Filter "Name='${image}'" | ForEach-Object { $_.ExecutablePath }`;
  return new Promise((resolve) => {
    execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', cmd], { windowsHide: true, timeout: 10_000 }, (err, stdout) =>
      resolve(err ? [] : stdout.split(/\r?\n/).map((l) => l.trim())),
    );
  });
}

// exeRunning reports whether any of the running paths is one of the browser's
// exes, compared as Windows does: case and slash direction aside.
export function exeRunning(running: string[], exes: string[]): boolean {
  const norm = (p: string) => p.replaceAll('/', '\\').toLowerCase();
  const ours = new Set(exes.map(norm));
  return running.some((p) => p !== '' && ours.has(norm(p)));
}

// copyProfile copies the one profile's cookie store and the Local State that
// holds its (wrapped) key into a throwaway user-data directory, so the browser
// we launch reads a copy and never touches the user's own files. It returns
// 'running' if a file can't be read (the browser likely holds it open), and
// otherwise how many unexpired cookies the copy holds, when SQLite could count
// them.
async function copyProfile(udd: string, temp: string, profileDir: string): Promise<{ rows?: number } | 'running'> {
  try {
    await copyFile(join(udd, 'Local State'), join(temp, 'Local State'));
    const dst = join(temp, profileDir, 'Network');
    await mkdir(dst, { recursive: true });
    // The cookie store, newer Network/Cookies first.
    for (const dir of [join(udd, profileDir, 'Network'), join(udd, profileDir)]) {
      const src = join(dir, 'Cookies');
      if (existsSync(src)) return await snapshotCookies(src, join(dst, 'Cookies'), join(temp, '.staging'));
    }
    return 'running'; // no cookie store to read
  } catch {
    // EBUSY/EPERM/ENOENT/SQLITE_BUSY here almost always means the browser is open.
    return 'running';
  }
}

// snapshotCookies writes a consistent copy of the cookie store at src to dst.
// A byte copy of a database and its -wal/-journal taken at different moments
// can disagree, so SQLite makes the copy (VACUUM INTO): from the store itself,
// read-only, or when that can't open it, from a byte copy in staging, which
// SQLite opens writable and so first replays the WAL into or rolls the journal
// back from. Where node:sqlite is missing it falls back to the byte copy alone.
// It throws when the store can't be read, which the caller takes as running.
export async function snapshotCookies(src: string, dst: string, staging: string): Promise<{ rows?: number }> {
  let sqlite: typeof import('node:sqlite');
  try {
    sqlite = await import('node:sqlite');
  } catch {
    await copyStore(src, dst);
    return {};
  }
  const vacuumInto = (from: string, readOnly: boolean) => {
    const db = new sqlite.DatabaseSync(from, { readOnly });
    try {
      db.prepare('VACUUM INTO ?').run(dst);
    } finally {
      db.close();
    }
  };
  try {
    vacuumInto(src, true);
  } catch {
    await rm(dst, { force: true });
    await mkdir(staging, { recursive: true });
    try {
      const copy = join(staging, 'Cookies');
      await copyStore(src, copy);
      vacuumInto(copy, false);
    } finally {
      await rm(staging, { recursive: true, force: true });
    }
  }
  const db = new sqlite.DatabaseSync(dst, { readOnly: true });
  try {
    // Chrome's expires_utc is microseconds since 1601; 0 is a session cookie.
    const now = BigInt(Date.now()) * 1000n + 11_644_473_600_000_000n;
    const row = db.prepare('SELECT count(*) AS n FROM cookies WHERE expires_utc = 0 OR expires_utc > ?').get(now) as { n: number };
    return { rows: Number(row.n) };
  } catch {
    return {}; // a schema we don't know: no count, the browser still reads it
  } finally {
    db.close();
  }
}

// copyStore byte-copies a SQLite database with its sidecar files.
async function copyStore(src: string, dst: string): Promise<void> {
  await copyFile(src, dst);
  for (const suffix of ['-wal', '-shm', '-journal']) {
    if (existsSync(src + suffix)) await copyFile(src + suffix, dst + suffix);
  }
}

// cdpGetCookies asks the browser for every cookie (Storage.getCookies, which
// returns them decrypted) over the --remote-debugging-pipe transport: the
// browser reads commands on fd 3 (write) and writes replies on fd 4 (read),
// each message a JSON object terminated by a NUL byte. Nothing but this
// process can reach the pipe, so the decrypted cookies never cross the machine
// on a loopback port.
export function cdpGetCookies(write: Writable, read: Readable): Promise<CDPCookie[]> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timed out reading cookies')), 20_000);
    const done = (err: Error | null, cookies?: CDPCookie[]) => {
      clearTimeout(timer);
      read.off('data', onData);
      if (err) reject(err);
      else resolve(cookies ?? []);
    };
    let buf = Buffer.alloc(0);
    const onData = (chunk: Buffer) => {
      buf = Buffer.concat([buf, chunk]);
      let nul: number;
      while ((nul = buf.indexOf(0)) !== -1) {
        const frame = buf.subarray(0, nul);
        buf = buf.subarray(nul + 1);
        let msg: { id?: number; result?: { cookies?: CDPCookie[] }; error?: { message?: string } };
        try {
          msg = JSON.parse(frame.toString('utf8'));
        } catch {
          continue;
        }
        if (msg.id !== 1) continue; // ignore events and other replies
        if (msg.error) return done(new Error(msg.error.message || 'DevTools error'));
        return done(null, msg.result?.cookies ?? []);
      }
    };
    read.on('data', onData);
    read.on('error', (err) => done(err));
    write.on('error', (err) => done(err));
    write.write(JSON.stringify({ id: 1, method: 'Storage.getCookies' }) + '\0');
  });
}

// CDPCookie is the shape Storage.getCookies returns (the fields we use).
interface CDPCookie {
  name: string;
  value: string;
  domain: string;
  path: string;
  expires: number; // seconds since epoch, -1 for a session cookie
  secure: boolean;
  httpOnly: boolean;
  session: boolean;
  sameSite?: 'Strict' | 'Lax' | 'None';
}

export function toImported(c: CDPCookie): ImportedCookie {
  return {
    domain: c.domain,
    name: c.name,
    value: c.value,
    path: c.path || '/',
    expires: c.session || !c.expires || c.expires < 0 ? 0 : Math.floor(c.expires),
    secure: c.secure,
    httpOnly: c.httpOnly,
    sameSite: c.sameSite ?? '',
  };
}

// extractBrowserCookies reads one Windows browser profile's cookies by
// launching the browser itself against a copy of the profile. browser is the
// id the daemon reported (chrome, edge, brave); profileDir is the profile's
// sub-directory (Default, Profile 1, …).
export async function extractBrowserCookies(browser: string, profileDir: string): Promise<ExtractResult> {
  if (process.platform !== 'win32') return { error: 'failed' };
  const spec = browserSpecs()[browser];
  if (!spec || !existsSync(spec.userDataDir)) return { error: 'notInstalled' };
  const exe = spec.exes.find((p) => existsSync(p));
  if (!exe) return { error: 'notInstalled' };

  const temp = await mkdtemp(join(tmpdir(), 'agentbox-cookies-'));
  let proc: ReturnType<typeof spawn> | undefined;
  try {
    if (await browserRunning(spec)) return { error: 'running' };
    const copied = await copyProfile(spec.userDataDir, temp, profileDir || 'Default');
    if (copied === 'running') return { error: 'running' };

    proc = spawn(
      exe,
      [
        '--headless=new',
        `--user-data-dir=${temp}`,
        `--profile-directory=${profileDir || 'Default'}`,
        '--remote-debugging-pipe',
        '--no-first-run',
        '--no-default-browser-check',
        '--disable-extensions',
        '--disable-sync',
        '--disable-background-networking',
        '--disable-gpu',
      ],
      // stdin/out/err ignored; fd 3 is the pipe we write CDP commands to and
      // fd 4 the pipe the browser writes replies on (Puppeteer's `pipe: true`).
      { windowsHide: true, stdio: ['ignore', 'ignore', 'ignore', 'pipe', 'pipe'] },
    );
    proc.on('error', () => {}); // surfaced by the read timeout below

    const write = proc.stdio[3] as Writable | null;
    const read = proc.stdio[4] as Readable | null;
    if (!write || !read) throw new Error('the browser did not open its DevTools pipe');
    const cookies = await cdpGetCookies(write, read);
    if (cookies.length === 0) {
      // None may be genuine, or the browser started while we read and the copy
      // came out empty: only the profile's lockfile can say, since our own
      // headless browser runs from the same exe.
      return emptyResult(await lockHeld(spec.userDataDir), copied.rows);
    }
    return { cookies: cookies.map(toImported) };
  } catch {
    return { error: 'failed' };
  } finally {
    if (proc?.pid) {
      // Chrome spawns child processes; kill the whole tree on Windows.
      try {
        spawn('taskkill', ['/pid', String(proc.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
      } catch {
        proc.kill();
      }
    }
    await rm(temp, { recursive: true, force: true }).catch(() => {});
  }
}

// emptyResult is what reading no cookies means: the browser holding the
// profile, or a copy that had cookies the browser didn't hand back, isn't "no
// cookies", which is left for a profile that truly has none (or uncounted).
export function emptyResult(running: boolean, rows: number | undefined): ExtractResult {
  if (running) return { error: 'running' };
  if (rows) return { error: 'failed' };
  return { cookies: [] };
}

export function registerBrowserCookies(ipcMain: IpcMain): void {
  ipcMain.handle('browsercookies:extract', (_event, browser: string, profileDir: string) =>
    extractBrowserCookies(browser, profileDir),
  );
}
