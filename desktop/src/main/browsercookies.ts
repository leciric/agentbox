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
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { copyFile, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { Readable, Writable } from 'node:stream';
import type { IpcMain } from 'electron';
import type { ImportedCookie } from '../shared/api';

// ExtractResult is what the host hands back: the decrypted cookies, or a code
// the renderer maps to a translated message. 'running' means the profile
// couldn't be copied (the browser is likely open); 'notInstalled' that the
// browser's exe or user data wasn't found; 'failed' anything else.
export type ExtractResult = { cookies: ImportedCookie[] } | { error: 'running' | 'notInstalled' | 'failed' };

// A browser's user-data directory and the places its exe may be installed, on
// Windows. env() reads a Windows environment variable, defaulting to ''.
function env(name: string): string {
  return process.env[name] ?? '';
}

interface BrowserSpec {
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

// copyProfile copies the one profile's cookie store and the Local State that
// holds its (wrapped) key into a throwaway user-data directory, so the browser
// we launch reads a copy and never touches the user's own files. It returns
// 'running' if a file can't be read (the browser likely holds it open).
async function copyProfile(udd: string, temp: string, profileDir: string): Promise<'ok' | 'running'> {
  try {
    await copyFile(join(udd, 'Local State'), join(temp, 'Local State'));
    const dst = join(temp, profileDir, 'Network');
    await mkdir(dst, { recursive: true });
    // The cookie store, newer Network/Cookies first, with its sidecar files.
    let copied = false;
    for (const [dir, name] of [
      [join(udd, profileDir, 'Network'), 'Cookies'],
      [join(udd, profileDir), 'Cookies'],
    ] as const) {
      const src = join(dir, name);
      if (!existsSync(src)) continue;
      await copyFile(src, join(dst, 'Cookies'));
      for (const suffix of ['-wal', '-shm', '-journal']) {
        if (existsSync(src + suffix)) await copyFile(src + suffix, join(dst, 'Cookies' + suffix));
      }
      copied = true;
      break;
    }
    if (!copied) return 'running'; // no cookie store to read
    return 'ok';
  } catch {
    // EBUSY/EPERM/ENOENT here almost always means the browser is open.
    return 'running';
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

export function registerBrowserCookies(ipcMain: IpcMain): void {
  ipcMain.handle('browsercookies:extract', (_event, browser: string, profileDir: string) =>
    extractBrowserCookies(browser, profileDir),
  );
}
