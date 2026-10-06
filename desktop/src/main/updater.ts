// The main process's half of updating the app in place (appupdate.ts): what
// the window asks for, the release the daemon finds, the download through
// Chromium's network stack (the system's proxy settings), and the restart.
import { execFile, spawn } from 'node:child_process';
import { accessSync, constants, existsSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { app, ipcMain, net } from 'electron';
import { AppImageUpdater } from 'electron-updater';
import type { AppUpdateProgress, AppUpdateResult } from '../shared/appupdate.ts';
import { AppUpdates } from './appimageupdate';
import { cleanUp, detectInstall, update, UpdateError, type Release, type Run } from './appupdate';
import { isLocal } from './connection';
import { request } from './daemon';

const where = () =>
  detectInstall({
    platform: process.platform,
    execPath: process.execPath,
    env: process.env,
    packaged: app.isPackaged,
    writable: (dir) => {
      try {
        accessSync(dir, constants.W_OK);
        return true;
      } catch {
        return false;
      }
    },
    exists: existsSync,
  });

// The app's own folder for downloads that don't go beside the app: the Mac's
// .zip and the Windows installer.
const downloads = () => join(app.getPath('userData'), 'updates');

const run: Run = (cmd, args) =>
  new Promise((resolve, reject) =>
    execFile(cmd, args, { timeout: 300_000 }, (err, stdout, stderr) => {
      const out = `${stdout}${stderr}`.trim();
      if (err) reject(new Error(out || err.message));
      else resolve(out);
    }),
  );

let restarting = false;

// restartingForUpdate says the app is quitting to restart into a new version:
// on a Mac, its VM stays up for the new app rather than stop and boot again.
export function restartingForUpdate(): boolean {
  return restarting;
}

let running: Promise<AppUpdateResult> | undefined;

// installAppUpdates handles the window's update requests, and removes what an
// earlier update left behind.
export function installAppUpdates(send: (channel: string, ...args: unknown[]) => void): void {
  const { install } = where();
  if (install) cleanUp(install);
  rmSync(downloads(), { recursive: true, force: true });

  ipcMain.handle('appUpdate:support', () => where().support);
  ipcMain.handle('appUpdate:start', () => {
    running ??= start((p) => send('appUpdate:progress', p)).finally(() => {
      running = undefined;
    });
    return running;
  });
}

async function start(onProgress: (p: AppUpdateProgress) => void): Promise<AppUpdateResult> {
  const { support, install } = where();
  if (!install) return { ok: false, code: 'unsupported', detail: support.inPlace ? '' : support.reason };
  let release: Release | undefined;
  try {
    // A restart cuts a job short, and the new app leaves a busy daemon of the
    // old version running: the update waits for the jobs instead.
    if (isLocal()) {
      const jobs = await request('GET', '/v1/jobs').catch(() => undefined);
      const busy = jobs?.status === 200 ? (JSON.parse(jobs.body) as { status: string }[]).filter((j) => j.status === 'running').length : 0;
      if (busy > 0) return { ok: false, code: 'busy', detail: String(busy) };
    }
    const res = await request('GET', '/v1/update/release');
    if (res.status !== 200) return { ok: false, code: 'noRelease', detail: errorOf(res.body) };
    release = JSON.parse(res.body) as Release;
    if (release.version === app.getVersion()) return { ok: false, code: 'upToDate', detail: release.version, url: release.url };
    if (install.kind === 'appimage') {
      await updateAppImage(release, onProgress);
      return { ok: true };
    }
    const next = await update({
      release,
      install,
      arch: process.arch,
      tmp: downloads(),
      fetch: (url) => net.fetch(url),
      run,
      onProgress,
    });
    onProgress({ phase: 'restart', version: release.version });
    restarting = true;
    if ('installer' in next) {
      // The per-user installer, silently: it closes this app if it's still
      // running, installs, and starts the new one (--force-run).
      spawn(next.installer, ['--updated', '/S', '--force-run'], { detached: true, stdio: 'ignore' }).unref();
    } else {
      // The same arguments; for a portable .exe, the .exe itself rather than
      // the binary it unpacked to.
      app.relaunch({ execPath: install.kind === 'mac' ? undefined : next.relaunch, args: process.argv.slice(1) });
    }
    setTimeout(() => app.quit(), 300); // for the window to show it's restarting
    return { ok: true };
  } catch (err) {
    console.error('updating the app:', err);
    const code = err instanceof UpdateError ? err.code : 'install';
    return { ok: false, code, detail: err instanceof Error ? err.message : String(err), url: release?.url };
  }
}

// updateAppImage updates a running AppImage with electron-updater
// (appimageupdate.ts), which checks the download against the release's
// latest-linux.yml and fetches only the blocks that changed, then quits,
// swaps the new AppImage in and starts it. Its percent is reported as the
// download's progress.
async function updateAppImage(release: Release, onProgress: (p: AppUpdateProgress) => void): Promise<void> {
  const version = release.version.replace(/^v/, '');
  const updater = new AppImageUpdater();
  updater.logger = console;
  const updates = new AppUpdates(updater, app.getVersion(), (s) => {
    if (s.state === 'downloading') onProgress({ phase: 'download', version, received: s.percent, total: 100 });
  });
  const downloaded = await updates.download(release);
  if (downloaded.state === 'failed') throw new UpdateError('download', downloaded.error);
  onProgress({ phase: 'restart', version });
  restarting = true;
  const installed = updates.install();
  if (installed.state === 'failed') {
    restarting = false;
    throw new UpdateError('install', installed.error);
  }
}

function errorOf(body: string): string {
  try {
    return (JSON.parse(body) as { error?: string }).error ?? body;
  } catch {
    return body;
  }
}
