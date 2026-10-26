// The only surface the renderer gets: the daemon's API, events and WebSocket
// streams, relayed by the main process, plus the command-line tool, a folder
// picker, links and the clipboard.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron';
import type * as T from '../shared/api';
import type { AppUpdateProgress, AppUpdateResult, AppUpdateSupport } from '../shared/appupdate';
import type { TraySettings } from '../main/tray';
import type { TrayTarget } from '../main/traymodel';

export interface ApiResponse {
  status: number;
  body: string;
  contentType: string;
}

export interface ConnectionState {
  state: 'connecting' | 'connected' | 'disconnected';
  error?: string;
  starting?: { since: number; step: string }; // the daemon is being started (main/daemon.ts StartProgress)
}

export interface HostSetupStatus {
  pkexec: string | null;
  user: string;
  running: boolean;
  resizing: boolean; // `agentbox vm resize` is running
  // On a Mac, `agentbox vm krunkit --remake` is running: the VM is gone for
  // a while, which isn't a VM to set up.
  remaking?: boolean;
  // On a Mac, AgentBox runs in a Linux VM, and "host setup" is `agentbox vm
  // init`: what `agentbox vm status --json` says about that VM. null elsewhere.
  vm: VMStatus | null;
  // On Windows, AgentBox runs in a WSL distro of its own, and "host setup" is
  // `agentbox wsl init`: what `agentbox wsl status --json` says about that
  // distro. null elsewhere.
  wsl: WSLStatus | null;
  // On Linux, where AgentBox runs: in a VM of its own (`agentbox vm init`),
  // or, on a machine set up before that, on the machine itself until it moves
  // (`agentbox vm migrate`); and whether it can run the VM. null elsewhere;
  // missing from the web app.
  linux?: LinuxSetup | null;
  // On Linux in VM mode, AgentBox's Cloud Hypervisor VM as `agentbox vm
  // status --json` says: its size, and what it can be resized to, with and
  // without a restart, or state "missing" before vm init made it. null
  // elsewhere; missing from the web app.
  chv?: T.VMStatus | null;
}

// VMMigration is `agentbox vm migrate --status --json`: on a Linux machine
// that runs AgentBox itself, what there is to move into AgentBox's VM, and how
// far a move got. main/hostsetup.ts has the same.
export interface VMMigration {
  state: 'none' | 'available' | 'started' | 'verified' | 'removed';
  projects?: string[];
  agents?: string[];
  oldMachines?: string[]; // what removing the old machines removes, once the move is checked
  backup?: string; // the state.db from before the move
  found?: string[]; // what the check found in the VM
}

export interface LinuxSetup {
  mode: 'host' | 'vm';
  kvm: boolean; // /dev/kvm is there for this user, which the VM needs
  // Why it isn't (main/kvm.ts): "denied" and "module" are what grantKVM
  // fixes, "novirt" is the firmware's. Missing when kvm is true.
  kvmProblem?: 'denied' | 'module' | 'novirt';
  cores: number; // the machine's, the most CPUs the VM can have
  memory: number; // bytes, the machine's
  // The size `agentbox vm init` gives the VM unless it's told otherwise.
  defaultCpus: number;
  defaultMemoryCap: number; // bytes
}

export interface VMStatus {
  // "vz" when the VM is run by Apple's Virtualization framework through
  // AgentBox itself, without Lima (experimental); missing for Lima's.
  driver?: 'vz';
  lima: string; // the limactl in use, "" when Lima isn't installed
  problem?: string; // why there's no VM to use, and what to run
  name: string;
  exists: boolean;
  status?: string; // Running, Stopped, Broken
  cpus?: number;
  memory?: number; // bytes
  disk?: number; // bytes
  limits?: VMLimits; // what `agentbox vm resize` takes on this Mac
  vmType?: string; // vz, krunkit
  // Whether `agentbox vm init` would make the VM with krunkit, which gives
  // memory back to the Mac and has a GPU for agents: on Apple Silicon, before
  // there's a VM and for one made without krunkit. brew: Homebrew can install
  // what's missing.
  krunkit?: { available: boolean; missing?: 'krunkit' | 'driver'; brew?: boolean };
  swap?: T.VMSwap; // what `agentbox vm swap` set (size only)
}

// VMPower is AgentBox's VM in VM mode (the daemon, Incus and every agent in
// one Cloud Hypervisor VM on Linux): its state and its memory, as the host's
// command-line tool reports them (main/vmpower.ts).
export interface VMPower {
  state: VMPowerState;
  memoryUsed: number; // bytes in use inside the VM
  memoryGranted: number; // bytes the VM holds of the host's memory right now
  memoryCap: number; // bytes the VM may grow to at most
  // memoryResident is what the VM holds of the host's memory, its page cache
  // included, and memoryHostCache what the host caches of its disks besides.
  memoryResident?: number;
  memoryHostCache?: number;
  cpus: number;
  error?: string; // why the VM can't be reached or controlled, if it can't
  // pausedForDisk: the supervisor paused it because the host's disk that holds
  // its disk images is nearly full; hostFree is what that disk has free.
  pausedForDisk?: boolean;
  hostFree?: number;
  // disk is its disk images as the host sees them: what they take on the
  // host's disk (allocated) against the size the VM was given (size).
  disk?: T.VMDisk;
  // driver is 'wsl' for AgentBox's WSL distro on Windows (internal/hostwsl):
  // memoryGranted and memoryCap are both what WSL lets it have, and it can't
  // be paused.
  driver?: 'wsl';
}

// TrayOpenTarget is where a tray menu entry points (main/traymodel.ts),
// a pull request aside, which the main process opens in the browser itself.
export type TrayOpenTarget = Exclude<TrayTarget, { kind: 'url' }>;
export type { TraySettings };

export type VMPowerState = 'off' | 'starting' | 'running' | 'pausing' | 'paused' | 'resuming' | 'stopping';
export type VMPowerAction = 'start' | 'pause' | 'resume' | 'stop';

export interface VMLimits {
  minCpus: number;
  maxCpus: number;
  minMemory: number; // bytes
  maxMemory: number; // bytes
}

export interface WSLStatus {
  wsl: string; // WSL's version, "" when there's no WSL that will do
  problem?: string; // why there's no distro to use, and what to do
  name: string;
  user: string;
  exists: boolean;
  state?: string; // Running, Stopped
  version?: number; // 1 or 2
}

export interface CliStatus {
  linkPath: string;
  linked: boolean;
  path: string | null;
  version: string | null;
  onPath: boolean;
  bundled: boolean;
  binary: string | null;
}

// The environment the app talks to: this machine, or one on a hub.
export interface EnvironmentTarget {
  kind: 'local' | 'hub';
  hub?: string;
  email?: string;
  environmentId?: string;
  environmentName?: string;
}

export interface HubAccount {
  url: string;
  email: string;
}

export interface HubEnvironment {
  id: string;
  name: string;
  online: boolean;
  connectedAt?: string;
  lastSeenAt?: string;
  version?: string;
  hostname?: string;
  createdAt: string;
}

function listen<A extends unknown[]>(channel: string, fn: (...args: A) => void): () => void {
  const listener = (_event: IpcRendererEvent, ...args: unknown[]) => fn(...(args as A));
  ipcRenderer.on(channel, listener);
  return () => ipcRenderer.removeListener(channel, listener);
}

const bridge = {
  // Holds the system awake (not the display) while on: agents are at work.
  keepAwake: (on: boolean): Promise<void> => ipcRenderer.invoke('keepawake:set', on),
  // The tray icon (main/tray.ts): whether closing the window keeps AgentBox
  // running there (null where there's no tray, the web app), and whether
  // this computer reviews finished agents, which its count leaves out when
  // not. onOpen gets where a tray menu entry points, including one clicked
  // before this window was made.
  tray: {
    settings: (): Promise<TraySettings | null> => ipcRenderer.invoke('tray:settings'),
    set: (next: Partial<TraySettings>): Promise<TraySettings> => ipcRenderer.invoke('tray:set', next),
    reviewing: (on: boolean) => ipcRenderer.send('tray:reviewing', on),
    onOpen: (fn: (target: TrayOpenTarget) => void) => {
      const off = listen('tray:open', fn);
      void ipcRenderer.invoke('tray:take').then((target: TrayOpenTarget | null) => target && fn(target));
      return off;
    },
  },
  request: (method: string, path: string, body?: unknown): Promise<ApiResponse> =>
    ipcRenderer.invoke('api:request', method, path, body),
  connection: (): Promise<ConnectionState> => ipcRenderer.invoke('daemon:connection'),
  onConnection: (fn: (state: ConnectionState) => void) => listen('daemon:connection', fn),
  onEvent: (fn: (event: unknown) => void) => listen('daemon:event', fn),
  info: (): Promise<{ socket: string; version: string; electron: string; packaged: boolean; platform: string }> => ipcRenderer.invoke('app:info'),
  // WebSocket endpoints of the API: bytes go as binary messages, strings as text messages.
  stream: {
    open: (path: string): Promise<number> => ipcRenderer.invoke('stream:open', path),
    write: (id: number, data: Uint8Array | string) => ipcRenderer.send('stream:write', id, data),
    close: (id: number) => ipcRenderer.send('stream:close', id),
    onOpened: (fn: (id: number) => void) => listen('stream:opened', fn),
    onData: (fn: (id: number, data: Uint8Array) => void) => listen('stream:data', fn),
    onExited: (fn: (id: number, reason: string) => void) => listen('stream:exited', fn),
  },
  cli: {
    status: (): Promise<CliStatus> => ipcRenderer.invoke('cli:status'),
    install: (): Promise<CliStatus> => ipcRenderer.invoke('cli:install'),
  },
  // `agentbox host setup` as root, through the desktop's own password dialog;
  // on a Mac, `agentbox vm init`, which needs no password. run resolves when
  // the setup succeeded; its output arrives on onOutput as it is printed.
  hostSetup: {
    status: (): Promise<HostSetupStatus> => ipcRenderer.invoke('hostsetup:status'),
    // { vm: true } runs AgentBox in a VM instead, on a Linux machine (`agentbox
    // vm init`, no password), stopping the daemon it ran itself first; cpus
    // and memoryCap (like 12GiB) size it. On a Mac, { driver: 'vz' } makes the
    // experimental vz driver's VM instead of Lima's, and { krunkit: true }
    // installs krunkit with Homebrew first, so the VM is made with it.
    run: (options?: { vm?: boolean; cpus?: number; memoryCap?: string; driver?: 'vz'; krunkit?: boolean }): Promise<{ restarted: boolean }> =>
      ipcRenderer.invoke('hostsetup:run', options),
    // On Linux, /dev/kvm for this user, which AgentBox's VM needs: `agentbox
    // host kvm` through the password dialog, with no new login. Its output
    // arrives on onOutput too.
    grantKVM: (): Promise<void> => ipcRenderer.invoke('hostsetup:kvm'),
    onOutput: (fn: (text: string) => void) => listen('hostsetup:output', fn),
  },
  // On Linux, `agentbox vm migrate`: this machine's own AgentBox moved into
  // AgentBox's VM, and afterwards its old machines removed from its Incus.
  // status is null where there's nothing to say; run resolves when it's done,
  // and its output arrives on onOutput as it is printed.
  vmMigrate: {
    status: (): Promise<VMMigration | null> => ipcRenderer.invoke('vmmigrate:status'),
    run: (): Promise<void> => ipcRenderer.invoke('vmmigrate:run', false),
    removeOld: (): Promise<void> => ipcRenderer.invoke('vmmigrate:run', true),
    onOutput: (fn: (text: string) => void) => listen('vmmigrate:output', fn),
  },
  // `agentbox vm resize`: new CPUs and memory (like 12GiB) for AgentBox's VM.
  // On a Mac that restarts it and stops every agent; on Linux memory is the
  // memory cap, and the VM changes while it runs when it can (chv.live), or
  // restarts with restart; disk (like 200GiB), on Linux, grows its disk. resize
  // resolves when the VM has its new size; its output arrives on onOutput as
  // it is printed.
  vm: {
    resize: (cpus: number, memory: string, restart?: boolean, disk?: string): Promise<void> =>
      ipcRenderer.invoke('vm:resize', cpus, memory, restart, disk),
    // `agentbox vm swap`: a swapfile of size (like 8GiB) in the running VM,
    // or none with null. Every agent keeps running; its output arrives on
    // onSwapOutput, apart from a resize's.
    swap: (size: string | null): Promise<void> => ipcRenderer.invoke('vm:swap', size),
    onSwapOutput: (fn: (text: string) => void) => listen('vm:swap-output', fn),
    onOutput: (fn: (text: string) => void) => listen('vm:output', fn),
    // In VM mode, the VM's power and memory; null when AgentBox isn't in VM
    // mode. act resolves once the VM is in its new state, and after start or
    // resume, once the daemon inside answers.
    power: (): Promise<VMPower | null> => ipcRenderer.invoke('vm:power'),
    act: (action: VMPowerAction): Promise<VMPower> => ipcRenderer.invoke('vm:act', action),
    // What the worktrees and media in the host's home take on its disk,
    // outside the VM's disk images; null when AgentBox isn't in VM mode. It
    // walks both, so it's asked for on demand rather than polled.
    disk: (): Promise<T.VMHomeDisk | null> => ipcRenderer.invoke('vm:disk'),
    // On a Mac whose VM wasn't made with krunkit, `agentbox vm krunkit
    // --remake --yes`: krunkit installed with Homebrew, then the VM deleted,
    // agents' machines and all, and made again with it. Its output arrives on
    // onKrunkitOutput.
    remakeWithKrunkit: (): Promise<void> => ipcRenderer.invoke('vm:krunkit'),
    onKrunkitOutput: (fn: (text: string) => void) => listen('vm:krunkit-output', fn),
  },
  // Hubs you signed in to, and their environments.
  hubs: {
    list: (): Promise<HubAccount[]> => ipcRenderer.invoke('hubs:list'),
    login: (url: string, email: string, password: string): Promise<HubAccount> => ipcRenderer.invoke('hubs:login', url, email, password),
    logout: (url: string): Promise<void> => ipcRenderer.invoke('hubs:logout', url),
    environments: (url: string): Promise<HubEnvironment[]> => ipcRenderer.invoke('hubs:environments', url),
    addEnvironment: (url: string, name: string): Promise<{ environment: HubEnvironment; token: string }> =>
      ipcRenderer.invoke('hubs:addEnvironment', url, name),
  },
  target: {
    get: (): Promise<EnvironmentTarget> => ipcRenderer.invoke('target:get'),
    set: (target: EnvironmentTarget): Promise<EnvironmentTarget> => ipcRenderer.invoke('target:set', target),
    onChange: (fn: (target: EnvironmentTarget) => void) => listen('target:changed', fn),
  },
  // mediaUrl is where the renderer loads a media item's file from; main/media.ts serves it.
  mediaUrl: (id: string, path?: string): string =>
    `agentbox-media://media/${encodeURIComponent(id)}${path ? '/' + path.split('/').map(encodeURIComponent).join('/') : ''}`,
  // Updating the app in place (main/updater.ts): whether this install can,
  // and the update itself, which restarts the app when it succeeds.
  appUpdate: {
    support: (): Promise<AppUpdateSupport> => ipcRenderer.invoke('appUpdate:support'),
    start: (): Promise<AppUpdateResult> => ipcRenderer.invoke('appUpdate:start'),
    onProgress: (fn: (progress: AppUpdateProgress) => void) => listen('appUpdate:progress', fn),
  },
  // chatImageUrl is where the renderer loads a picture sent in a chat, given
  // its daemon path (.../chat/images/<id>), or frames a Hatch page
  // (.../artifacts/<id>/page); main/media.ts serves it too.
  chatImageUrl: (path: string): string => `agentbox-media://api${path}`,
  // pageThumb is a Hatch page's thumbnail as a data: URL, drawn offscreen from
  // its daemon path and kept by key (<id>@<version>), or null when its HTML
  // can't be had (main/pagethumbs.ts).
  pageThumb: (path: string, key: string): Promise<string | null> => ipcRenderer.invoke('pageThumb:get', path, key),
  // renderUrl is where to frame a page a chat rendered from (main/renders.ts),
  // served with a policy of its own, with or without network.
  renderUrl: (html: string, network: boolean): Promise<string | null> => ipcRenderer.invoke('render:register', html, network),
  // Problem reports: the app's own sections of one (main/applog.ts), a
  // window's uncaught error to keep with them, the main process's uncaught
  // errors as they happen, and the folder the app's logs are in.
  report: {
    sections: (): Promise<T.ReportSection[]> => ipcRenderer.invoke('report:sections'),
    windowError: (err: { name: string; message: string; stack?: string }) => ipcRenderer.send('report:windowError', err),
    onAppError: (fn: (err: { at: string; where: 'main' | 'window'; name: string; message: string; stack?: string }) => void) => listen('app:error', fn),
    openLogs: (): Promise<string> => ipcRenderer.invoke('report:openLogs'),
    // send sends an automatic error report through the main process, which
    // keeps it for later when the daemon can't be reached (main/reportqueue.ts).
    send: (req: T.ReportRequest): Promise<'sent' | 'refused' | 'unreachable' | 'capped'> => ipcRenderer.invoke('report:send', req),
  },
  pickDirectory: (): Promise<string | null> => ipcRenderer.invoke('dialog:directory'),
  openPath: (path: string): Promise<string> => ipcRenderer.invoke('shell:openPath', path),
  // openChatFile opens a file sent in a chat, given its daemon path
  // (.../chat/files/<id>), with the system's default app: main/chatfiles.ts
  // copies it out of the daemon first. It answers an error, or ''.
  openChatFile: (path: string, name: string): Promise<string> => ipcRenderer.invoke('chatFile:open', path, name),
  // saveShare saves an agent's run as one page (.../share), where the user
  // picks: main/share.ts. It answers where, or '' when they didn't pick.
  // showShare shows a page saved there in the file manager.
  saveShare: (path: string, name: string): Promise<string> => ipcRenderer.invoke('share:save', path, name),
  showShare: (file: string): Promise<void> => ipcRenderer.invoke('share:show', file),
  showItem: (path: string): Promise<void> => ipcRenderer.invoke('shell:showItem', path),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('shell:openExternal', url),
  // An OS notification, shown only while the window isn't in front (main/notify.ts);
  // onNotificationClick gets its ID when it's clicked.
  notify: (notice: { id: string; title: string; body: string }): Promise<boolean> => ipcRenderer.invoke('notify:show', notice),
  onNotificationClick: (fn: (id: string) => void) => listen('notify:click', fn),
  // The passphrase a Chromium browser seals its cookies with, read from the
  // host's keyring (main/browserkeys.ts) for a browser-cookie import, since
  // the daemon in the VM can't reach the keyring. '' when there's none to
  // fetch. The value is passed straight to the import and never kept.
  browserKeyringSecret: (keyring: string): Promise<string> => ipcRenderer.invoke('browserkeys:get', keyring),
  copyText: (text: string) => ipcRenderer.send('clipboard:write', text),
  // setLanguage tells the main process the app's language, for its dialogs.
  setLanguage: (tag: string) => ipcRenderer.send('app:language', tag),
  readText: (): Promise<string> => ipcRenderer.invoke('clipboard:read'),
  // copyImage puts a PNG on the clipboard; saveImage asks where to save one,
  // named name, and answers whether it was saved.
  copyImage: (png: Uint8Array) => ipcRenderer.send('clipboard:writeImage', png),
  saveImage: (name: string, png: Uint8Array): Promise<boolean> => ipcRenderer.invoke('image:save', name, png),
};

contextBridge.exposeInMainWorld('agentbox', bridge);

export type Bridge = typeof bridge;
