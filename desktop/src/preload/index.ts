// The only surface the renderer gets: the daemon's API, events and WebSocket
// streams, relayed by the main process, plus the command-line tool, a folder
// picker, links and the clipboard.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron';

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
}

export interface VMStatus {
  lima: string; // the limactl in use, "" when Lima isn't installed
  problem?: string; // why there's no VM to use, and what to run
  name: string;
  exists: boolean;
  status?: string; // Running, Stopped, Broken
  cpus?: number;
  memory?: number; // bytes
  disk?: number; // bytes
  limits?: VMLimits; // what `agentbox vm resize` takes on this Mac
}

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
    run: (): Promise<{ restarted: boolean }> => ipcRenderer.invoke('hostsetup:run'),
    onOutput: (fn: (text: string) => void) => listen('hostsetup:output', fn),
  },
  // On a Mac, `agentbox vm resize`: new CPUs and memory (like 12GiB) for
  // AgentBox's VM, which restarts it and stops every agent. resize resolves
  // when the VM is back; its output arrives on onOutput as it is printed.
  vm: {
    resize: (cpus: number, memory: string): Promise<void> => ipcRenderer.invoke('vm:resize', cpus, memory),
    onOutput: (fn: (text: string) => void) => listen('vm:output', fn),
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
  pickDirectory: (): Promise<string | null> => ipcRenderer.invoke('dialog:directory'),
  openPath: (path: string): Promise<string> => ipcRenderer.invoke('shell:openPath', path),
  showItem: (path: string): Promise<void> => ipcRenderer.invoke('shell:showItem', path),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('shell:openExternal', url),
  copyText: (text: string) => ipcRenderer.send('clipboard:write', text),
  readText: (): Promise<string> => ipcRenderer.invoke('clipboard:read'),
};

contextBridge.exposeInMainWorld('agentbox', bridge);

export type Bridge = typeof bridge;
