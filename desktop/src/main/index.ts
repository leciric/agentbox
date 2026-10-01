// The Electron main process. It holds no AgentBox logic: it proxies API calls,
// the event stream, WebSocket streams and media files between the renderer and
// the current environment (this machine's daemon, or one on a hub), signs in to
// hubs, and installs the command-line tool.
import { app, BrowserWindow, clipboard, dialog, ipcMain, shell } from 'electron';
import { arch, hostname } from 'node:os';
import { join } from 'node:path';
import { AppLog, captureConsole, reportSections, type AppError } from './applog';
import { agentboxBin, cliStatus, installCli } from './cli';
import { currentTarget, isLocal, localSocket, savedHubs, saveHubs, setTarget, type SavedHub, type Target } from './connection';
import { type ApiResponse, ensureDaemon, notListening, request, restartDaemon, restartIfStale, socketPath, stopHostDaemon, stopStartingDaemon, unreachable } from './daemon';
import { EventStream } from './events';
import { hostSetupStatus, onMac, runHostSetup, runVMMigration, stopVM, vmMigration, type HostSetupOptions } from './hostsetup';
import { handleMedia, registerMediaScheme } from './media';
import { installPhoneWeb } from './phoneweb';
import { onWindows, startRelay, stopRelay } from './relay';
import { guardStdio } from './stdio';
import { Streams } from './streams';
import { learnMode, linuxVM } from './vmmode';
import { allowMicrophone, enableWebGPU } from './voice';
import { distro, linuxPath, windowsPath } from './wslpaths';
import './vmpower';

guardStdio();
// The app's log, for problem reports (applog.ts): console and every uncaught
// error. The monitor only watches: Electron still shows its dialog for an
// uncaught exception, and the window is told, to offer or send an error
// report (the renderer's lib/errorReports.ts).
const appLog = new AppLog(app.getPath('logs'));
captureConsole(appLog);
process.on('uncaughtExceptionMonitor', (err) => send('app:error', appLog.error('main', err)));
registerMediaScheme();
enableWebGPU();

let win: BrowserWindow | undefined;

const send = (channel: string, ...args: unknown[]) => {
  if (win && !win.isDestroyed()) win.webContents.send(channel, ...args);
};
const streams = new Streams(send);
const events = new EventStream(
  (event) => send('daemon:event', event),
  (state) => {
    send('daemon:connection', state);
    // Each time the daemon is back, it may be one that hasn't the web
    // version phones are served (phoneweb.ts).
    if (state.state === 'connected')
      void installPhoneWeb(app.getVersion()).catch((err: unknown) => console.error('phones:', err));
  },
);

// api:request answers a request the daemon never saw, because there was no
// daemon to reach, as the daemon would refuse it: a 503 saying why. That's an
// expected state, not an error, while the VM stops, starts or is paused (the
// forward to its daemon closes each connection then): the renderer shows it
// from the event stream's connection state, and a rejection here would be
// logged by Electron for every call.
ipcMain.handle('api:request', async (_event, method: string, path: string, body?: unknown): Promise<ApiResponse> => {
  try {
    return await request(method, path, body);
  } catch (err) {
    if (!isLocal() || !notListening(err)) return unreachable(err);
  }
  try {
    await ensureDaemon();
    return await request(method, path, body);
  } catch (err) {
    return unreachable(err);
  }
});
ipcMain.handle('daemon:connection', () => events.state);
ipcMain.handle('app:info', () => ({
  socket: onWindows ? localSocket() : socketPath,
  version: app.getVersion(),
  electron: process.versions.electron,
  packaged: app.isPackaged,
  platform: process.platform,
}));

// Problem reports: the app's own sections, which the daemon redacts and
// sends with its own (internal/daemon/report.go), and the window's uncaught
// errors, kept with the main process's.
ipcMain.handle('report:sections', () =>
  reportSections(appLog, {
    version: app.getVersion(),
    electron: process.versions.electron,
    chrome: process.versions.chrome,
    platform: process.platform,
    arch: arch(),
    target: currentTarget().kind === 'local' ? 'this machine' : 'an environment on a hub',
    vmMode: linuxVM(),
  }),
);
ipcMain.on('report:windowError', (_event, err: Pick<AppError, 'name' | 'message' | 'stack'>) => appLog.windowError(err));
ipcMain.handle('report:openLogs', () => shell.openPath(app.getPath('logs')));

ipcMain.handle('stream:open', (_event, path: string) => {
  if (!path.startsWith('/v1/')) throw new Error(`not an API path: ${path}`);
  return streams.open(path);
});
ipcMain.on('stream:write', (_event, id: number, data: Uint8Array | string) => streams.write(id, data));
ipcMain.on('stream:close', (_event, id: number) => streams.close(id));

// Hubs and environments.

export interface EnvironmentTarget {
  kind: 'local' | 'hub';
  hub?: string;
  email?: string;
  environmentId?: string;
  environmentName?: string;
}

function describe(t: Target): EnvironmentTarget {
  return t.kind === 'local' ? { kind: 'local' } : { kind: 'hub', hub: t.hub.url, email: t.hub.email, environmentId: t.environmentId, environmentName: t.environmentName };
}

// hubCall calls a hub's own API, and throws its error message.
async function hubCall<T>(hub: string, method: string, path: string, token?: string, body?: unknown): Promise<T> {
  const res = await fetch(`${hub}${path}`, {
    method,
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) {
    let message = text.trim() || `HTTP ${res.status}`;
    try {
      message = JSON.parse(text).error ?? message;
    } catch {
      // not JSON
    }
    throw new Error(message);
  }
  return (text ? JSON.parse(text) : undefined) as T;
}

function savedHub(url: string): SavedHub {
  const hub = savedHubs().find((h) => h.url === url);
  if (!hub) throw new Error(`not signed in to ${url}`);
  return hub;
}

ipcMain.handle('hubs:list', () => savedHubs().map(({ url, email }) => ({ url, email })));
ipcMain.handle('hubs:login', async (_event, url: string, email: string, password: string) => {
  url = url.trim().replace(/\/+$/, '');
  if (!/^https?:\/\/[^/]+$/.test(url)) throw new Error(`${url} isn't a hub address: use https://hub.example.com`);
  const session = await hubCall<{ token: string; user: { email: string } }>(url, 'POST', '/v1/auth/login', undefined, {
    email,
    password,
    label: `desktop app on ${hostname()}`,
  });
  saveHubs([{ url, email: session.user.email, token: session.token }, ...savedHubs().filter((h) => h.url !== url)]);
  return { url, email: session.user.email };
});
ipcMain.handle('hubs:logout', async (_event, url: string) => {
  const hub = savedHub(url);
  await hubCall(url, 'POST', '/v1/auth/logout', hub.token).catch(() => {});
  saveHubs(savedHubs().filter((h) => h.url !== url));
  const t = currentTarget();
  if (t.kind === 'hub' && t.hub.url === url) switchTo({ kind: 'local' });
});
ipcMain.handle('hubs:environments', (_event, url: string) => hubCall(url, 'GET', '/v1/environments', savedHub(url).token));
ipcMain.handle('hubs:addEnvironment', (_event, url: string, name: string) => hubCall(url, 'POST', '/v1/environments', savedHub(url).token, { name }));

function switchTo(next: Target): void {
  streams.closeAll();
  setTarget(next);
  events.restart();
  send('target:changed', describe(next));
}

ipcMain.handle('target:get', () => describe(currentTarget()));
ipcMain.handle('target:set', (_event, next: EnvironmentTarget) => {
  if (next.kind === 'local') switchTo({ kind: 'local' });
  else switchTo({ kind: 'hub', hub: savedHub(next.hub ?? ''), environmentId: next.environmentId ?? '', environmentName: next.environmentName ?? '' });
  return describe(currentTarget());
});

ipcMain.handle('cli:status', () => cliStatus());
ipcMain.handle('cli:install', () => installCli());

// Host setup runs the command-line tool as root through pkexec, not the
// daemon: the daemon runs as you, and root is the point. Its output goes to
// the Setup page as it comes, like a job's log.
ipcMain.handle('hostsetup:status', () => hostSetupStatus());
// On Linux, { vm: true } runs AgentBox in a VM instead (vmmode.ts), after
// stopping the daemon this machine ran itself. On a Mac, { driver: 'vz' }
// makes the experimental vz driver's VM instead of Lima's.
ipcMain.handle('hostsetup:run', async (_event, options?: Pick<HostSetupOptions, 'vm' | 'cpus' | 'memoryCap' | 'driver'>) => {
  await runHostSetup((text) => send('hostsetup:output', text), {
    vm: options?.vm === true,
    driver: options?.driver === 'vz' ? 'vz' : undefined,
    cpus: typeof options?.cpus === 'number' ? options.cpus : undefined,
    memoryCap: typeof options?.memoryCap === 'string' ? options.memoryCap : undefined,
    before: stopHostDaemon,
  });
  // A daemon that started before this ran found no Incus; the one started now
  // does, and the Setup page turns green without anyone logging out.
  const restarted = await restartDaemon().catch(() => false);
  return { restarted };
});

// Moving this Linux machine's own AgentBox into AgentBox's VM (`agentbox vm
// migrate`), and removing its old machines afterwards: no password, like vm
// init. Every page's data is the VM's daemon's afterwards.
ipcMain.handle('vmmigrate:status', () => vmMigration());
ipcMain.handle('vmmigrate:run', (_event, removeOld?: boolean) =>
  runVMMigration((text) => send('vmmigrate:output', text), removeOld === true),
);

// On Windows the daemon only knows the WSL distro's paths: the picker starts in
// the distro, where projects belong, and what it picks is given in Linux terms.
// A folder on a Windows drive becomes /mnt/<drive>/..., which Add project then
// offers to copy into the distro, since the daemon won't add it as it is.
ipcMain.handle('dialog:directory', async () => {
  const options = {
    title: 'Add a project',
    properties: ['openDirectory' as const],
    ...(onWindows ? { defaultPath: `\\\\wsl.localhost\\${distro}\\home` } : {}),
  };
  const result = win ? await dialog.showOpenDialog(win, options) : await dialog.showOpenDialog(options);
  if (result.canceled) return null;
  const picked = result.filePaths[0];
  if (!onWindows) return picked;
  const path = linuxPath(picked);
  if (!path) throw new Error(`${picked} isn't in AgentBox's WSL distro (${distro}): clone the project there, under \\\\wsl.localhost\\${distro}\\home`);
  return path;
});
// The daemon's paths are the distro's; Explorer opens them through \\wsl.localhost.
const hostPath = (path: string) => (onWindows ? windowsPath(path) : path);
ipcMain.handle('shell:openPath', (_event, path: string) => shell.openPath(hostPath(path)));
ipcMain.handle('shell:showItem', (_event, path: string) => shell.showItemInFolder(hostPath(path)));
ipcMain.handle('shell:openExternal', async (_event, url: string) => {
  if (!/^https?:\/\//.test(url)) throw new Error(`not a web address: ${url}`);
  await shell.openExternal(url);
});
ipcMain.on('clipboard:write', (_event, text: string) => clipboard.writeText(text));
ipcMain.handle('clipboard:read', () => clipboard.readText());

function createWindow(): void {
  win = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 1024,
    minHeight: 640,
    title: 'AgentBox',
    backgroundColor: '#07070b',
    autoHideMenuBar: true,
    webPreferences: {
      preload: join(__dirname, '../preload/index.cjs'),
      contextIsolation: true,
      sandbox: true,
    },
  });
  void win.loadFile(join(__dirname, '../renderer/index.html'));
  win.on('closed', () => {
    streams.closeAll();
    win = undefined;
  });
}

void app.whenReady().then(async () => {
  handleMedia();
  allowMicrophone();
  createWindow();
  // The relay answers at once, whether or not WSL does: it only reaches into
  // the distro on the first request.
  if (onWindows) await startRelay(agentboxBin()).catch(() => {});
  await learnMode();
  await restartIfStale().catch(() => {});
  events.start();
});

// On Linux, closing the app leaves the daemon and every agent running: they're
// this machine's own, and the command-line tool keeps using them. In VM mode
// that includes the VM: stopping it is the top bar's Free resources, not
// quitting (vmmode.ts).
app.on('window-all-closed', () => {
  events.stop();
  stopRelay();
  app.quit();
});

// On a Mac they run in AgentBox's VM, and quitting the app, by closing its
// window or with Cmd+Q, stops the VM too. The quit waits for the stop, with the
// window hidden, but not for longer than vmStopTimeout: after that the app quits
// and the stop carries on without it. The next launch starts the VM again, the
// way it starts a daemon that isn't running (ensureDaemon). An app that starts
// no daemon (AGENTBOX_NO_AUTOSTART) never started the VM, and leaves it be.
const vmStopTimeout = 60_000;
let vmStop: 'pending' | 'stopping' | 'done' = onMac && !process.env.AGENTBOX_NO_AUTOSTART ? 'pending' : 'done';

app.on('before-quit', (event) => {
  if (vmStop === 'done') return;
  event.preventDefault();
  if (vmStop === 'stopping') return;
  vmStop = 'stopping';
  events.stop();
  for (const window of BrowserWindow.getAllWindows()) window.hide();
  const stopped = stopStartingDaemon().then(stopVM);
  const timedOut = new Promise<void>((resolve) => setTimeout(resolve, vmStopTimeout));
  void Promise.race([stopped, timedOut])
    .catch((err: unknown) => console.error("stopping AgentBox's VM:", err))
    .finally(() => {
      vmStop = 'done';
      app.quit();
    });
});
