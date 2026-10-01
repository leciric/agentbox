// The only surface the renderer gets: the daemon's API, events and WebSocket
// streams, relayed by the main process, plus the command-line tool, a folder
// picker, links and the clipboard.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron';
import type * as T from '../shared/api';

export interface ApiResponse {
  status: number;
  body: string;
  contentType: string;
}

export interface ConnectionState {
  state: 'connecting' | 'connected' | 'disconnected';
  error?: string;
}

export interface HostSetupStatus {
  pkexec: string | null;
  user: string;
  running: boolean;
  resizing: boolean; // `agentbox vm resize` is running
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
  // memory back to the Mac: only before there's a VM, on Apple Silicon.
  krunkit?: { available: boolean; missing?: 'krunkit' | 'driver' };
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
  cpus: number;
  error?: string; // why the VM can't be reached or controlled, if it can't
  // pausedForDisk: the supervisor paused it because the host's disk that holds
  // its disk images is nearly full; hostFree is what that disk has free.
  pausedForDisk?: boolean;
  hostFree?: number;
}

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
  request: (method: string, path: string, body?: unknown): Promise<ApiResponse> =>
    ipcRenderer.invoke('api:request', method, path, body),
  connection: (): Promise<ConnectionState> => ipcRenderer.invoke('daemon:connection'),
  onConnection: (fn: (state: ConnectionState) => void) => listen('daemon:connection', fn),
  onEvent: (fn: (event: unknown) => void) => listen('daemon:event', fn),
  info: (): Promise<{ socket: string; version: string; electron: string; packaged: boolean; platform: string }> => ipcRenderer.invoke('app:info'),
  // Whether push-to-talk may put Chromium on Vulkan (main/voicegpu.ts): saved
  // is the choice, running what this run started with.
  voiceGPU: (): Promise<{ saved: { vulkan: boolean }; running: { vulkan: boolean }; platform: string }> => ipcRenderer.invoke('voice:gpu'),
  setVoiceGPU: (settings: { vulkan: boolean }): Promise<void> => ipcRenderer.invoke('voice:set-gpu', settings),
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
    // experimental vz driver's VM instead of Lima's.
    run: (options?: { vm?: boolean; cpus?: number; memoryCap?: string; driver?: 'vz' }): Promise<{ restarted: boolean }> =>
      ipcRenderer.invoke('hostsetup:run', options),
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
  // chatImageUrl is where the renderer loads a picture sent in a chat, given
  // its daemon path (.../chat/images/<id>); main/media.ts serves it too.
  chatImageUrl: (path: string): string => `agentbox-media://api${path}`,
  // Problem reports: the app's own sections of one (main/applog.ts), a
  // window's uncaught error to keep with them, the main process's uncaught
  // errors as they happen, and the folder the app's logs are in.
  report: {
    sections: (): Promise<T.ReportSection[]> => ipcRenderer.invoke('report:sections'),
    windowError: (err: { name: string; message: string; stack?: string }) => ipcRenderer.send('report:windowError', err),
    onAppError: (fn: (err: { at: string; where: 'main' | 'window'; name: string; message: string; stack?: string }) => void) => listen('app:error', fn),
    openLogs: (): Promise<string> => ipcRenderer.invoke('report:openLogs'),
  },
  pickDirectory: (): Promise<string | null> => ipcRenderer.invoke('dialog:directory'),
  openPath: (path: string): Promise<string> => ipcRenderer.invoke('shell:openPath', path),
  showItem: (path: string): Promise<void> => ipcRenderer.invoke('shell:showItem', path),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('shell:openExternal', url),
  copyText: (text: string) => ipcRenderer.send('clipboard:write', text),
  readText: (): Promise<string> => ipcRenderer.invoke('clipboard:read'),
};

contextBridge.exposeInMainWorld('agentbox', bridge);

export type Bridge = typeof bridge;
