// The app's bridge in a browser, when the hub serves the app: the same surface
// as the desktop app's preload script, over the hub's HTTP API with the session
// cookie. There's one hub (this page's origin); the environment you pick is
// remembered in this browser. What needs your own machine (installing the
// command-line tool, its folder picker, opening local folders) isn't available.
import type { ApiResponse, Bridge, CliStatus, ConnectionState, EnvironmentTarget, HostSetupStatus, HubAccount, HubEnvironment } from '../../preload';

const storageKey = 'agentbox.environment';

function loadTarget(): EnvironmentTarget {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? 'null') as EnvironmentTarget | null;
    if (saved?.kind === 'hub' && saved.environmentId) return { ...saved, hub: location.origin };
  } catch {
    // nothing saved
  }
  return { kind: 'hub', hub: location.origin };
}

let target = loadTarget();
const targetListeners = new Set<(t: EnvironmentTarget) => void>();
const apiBase = () => `/v1/environments/${encodeURIComponent(target.environmentId ?? '')}/api`;

async function hubCall<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
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

// The event stream of the current environment.
let connection: ConnectionState = { state: 'connecting' };
let source: EventSource | undefined;
const connectionListeners = new Set<(s: ConnectionState) => void>();
const eventListeners = new Set<(e: unknown) => void>();

function setConnection(next: ConnectionState) {
  connection = next;
  for (const fn of connectionListeners) fn(next);
}

function followEvents() {
  source?.close();
  source = undefined;
  if (!target.environmentId) return;
  setConnection({ state: 'connecting' });
  const es = new EventSource(`${apiBase()}/v1/events`, { withCredentials: true });
  source = es;
  es.onopen = () => setConnection({ state: 'connected' });
  es.onerror = () => {
    if (source !== es) return;
    // EventSource retries by itself; say why when the hub can tell.
    setConnection({ state: 'disconnected', error: `${target.environmentName ?? 'the environment'} isn't reachable` });
    void hubCall<HubEnvironment[]>('GET', '/v1/environments')
      .then((envs) => {
        const env = envs.find((e) => e.id === target.environmentId);
        if (env && !env.online && source === es) setConnection({ state: 'disconnected', error: `${env.name} is offline` });
      })
      .catch(() => {});
  };
  const deliver = (message: MessageEvent<string>) => {
    try {
      const event = JSON.parse(message.data);
      for (const fn of eventListeners) fn(event);
    } catch {
      // not an event we understand
    }
  };
  for (const type of ['job', 'job.log', 'agent', 'usage', 'project', 'media', 'chat']) es.addEventListener(type, deliver as EventListener);
}

// WebSocket streams: terminals, and the browser and Android views.
let nextStream = 1;
const sockets = new Map<number, WebSocket>();
const opened = new Set<(id: number) => void>();
const data = new Set<(id: number, bytes: Uint8Array) => void>();
const exited = new Set<(id: number, reason: string) => void>();

function listen<F>(set: Set<F>, fn: F): () => void {
  set.add(fn);
  return () => {
    set.delete(fn);
  };
}

function closeStreams() {
  for (const ws of sockets.values()) ws.close();
  sockets.clear();
}

const unavailable = (what: string) => () => Promise.reject(new Error(`${what} works in the desktop app, on the machine itself`));

export const webBridge: Bridge & { web: true } = {
  web: true,
  request: async (method: string, path: string, body?: unknown): Promise<ApiResponse> => {
    const res = await fetch(apiBase() + path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return { status: res.status, body: await res.text(), contentType: res.headers.get('content-type') ?? '' };
  },
  connection: () => Promise.resolve(connection),
  onConnection: (fn) => listen(connectionListeners, fn),
  onEvent: (fn) => listen(eventListeners, fn),
  info: () => Promise.resolve({ socket: location.origin, version: 'web', electron: '', packaged: false, platform: 'web' }),
  stream: {
    open: (path: string) => {
      const id = nextStream++;
      const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${apiBase()}${path}`);
      ws.binaryType = 'arraybuffer';
      sockets.set(id, ws);
      let done = false;
      const exit = (reason: string) => {
        if (done) return;
        done = true;
        sockets.delete(id);
        for (const fn of exited) fn(id, reason);
      };
      ws.onopen = () => {
        for (const fn of opened) fn(id);
      };
      ws.onmessage = (message) => {
        if (message.data instanceof ArrayBuffer) for (const fn of data) fn(id, new Uint8Array(message.data));
      };
      ws.onerror = () => exit('the connection failed');
      ws.onclose = (event) => exit(event.reason || (event.code === 1000 ? 'the session ended' : `the connection closed (${event.code})`));
      return Promise.resolve(id);
    },
    write: (id, payload) => {
      const ws = sockets.get(id);
      if (ws?.readyState === WebSocket.OPEN) ws.send(typeof payload === 'string' ? payload : payload.slice());
    },
    close: (id) => {
      sockets.get(id)?.close();
      sockets.delete(id);
    },
    onOpened: (fn) => listen(opened, fn),
    onData: (fn) => listen(data, fn),
    onExited: (fn) => listen(exited, fn),
  },
  cli: {
    status: () =>
      Promise.resolve<CliStatus>({ linkPath: '', linked: false, path: null, version: null, onPath: false, bundled: false, binary: null }),
    install: unavailable('Installing the command-line tool'),
  },
  hostSetup: {
    status: () => Promise.resolve<HostSetupStatus>({ pkexec: null, user: '', running: false, resizing: false, vm: null, wsl: null }),
    run: unavailable('Setting the host up'),
    onOutput: () => () => {},
    budget: unavailable('Setting the shared budget up'),
  },
  vm: {
    resize: unavailable("Resizing AgentBox's VM"),
    onOutput: () => () => {},
  },
  hubs: {
    list: async () => {
      const me = await hubCall<{ email: string }>('GET', '/v1/me');
      return [{ url: location.origin, email: me.email }] satisfies HubAccount[];
    },
    login: async (_url, email, password) => {
      const session = await hubCall<{ user: { email: string } }>('POST', '/v1/auth/login', { email, password, label: 'browser' });
      return { url: location.origin, email: session.user.email };
    },
    logout: async () => {
      await hubCall('POST', '/v1/auth/logout').catch(() => {});
      localStorage.removeItem(storageKey);
      location.reload();
    },
    environments: () => hubCall<HubEnvironment[]>('GET', '/v1/environments'),
    addEnvironment: (_url, name) => hubCall('POST', '/v1/environments', { name }),
  },
  target: {
    get: () => Promise.resolve(target),
    set: (next) => {
      target = { kind: 'hub', hub: location.origin, environmentId: next.environmentId, environmentName: next.environmentName };
      localStorage.setItem(storageKey, JSON.stringify(target));
      closeStreams();
      followEvents();
      for (const fn of targetListeners) fn(target);
      return Promise.resolve(target);
    },
    onChange: (fn) => listen(targetListeners, fn),
  },
  mediaUrl: (id: string, path?: string) =>
    `${apiBase()}/v1/media/${encodeURIComponent(id)}/file${path ? `?path=${encodeURIComponent(path)}` : ''}`,
  chatImageUrl: (path: string) => `${apiBase()}${path}`,
  pickDirectory: () => Promise.resolve(null),
  openPath: unavailable('Opening a folder'),
  showItem: unavailable('Showing a file'),
  openExternal: (url) => {
    window.open(url, '_blank', 'noopener');
    return Promise.resolve();
  },
  copyText: (text) => void navigator.clipboard?.writeText(text),
  readText: () => navigator.clipboard?.readText() ?? Promise.resolve(''),
};

// installWebBridge makes the bridge the app uses, and follows the chosen environment's events.
export function installWebBridge(): void {
  (window as unknown as { agentbox: typeof webBridge }).agentbox = webBridge;
  followEvents();
}

export function currentWebTarget(): EnvironmentTarget {
  return target;
}
