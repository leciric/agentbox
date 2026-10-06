// The app's bridge in a browser: the same surface as the desktop app's preload
// script, over HTTP with a cookie. Two things serve the app this way.
//
// A hub: its API with the session cookie. There's one hub (this page's
// origin); the environment you pick is remembered in this browser.
//
// A daemon, to a phone on its local network (internal/daemon/lan.go), which
// marks the page it serves with <meta name="agentbox-lan">: its API is under
// /api, for a phone paired with it, and there are no environments to pick.
//
// What needs your own machine (installing the command-line tool, its folder
// picker, opening local folders) isn't available either way.
import type { ApiResponse, Bridge, CliStatus, ConnectionState, EnvironmentTarget, HostSetupStatus, HubAccount, HubEnvironment } from '../../preload';
import * as T from '../../shared/api.ts';
import { t, type MessageKey } from '../../shared/i18n/index.ts';
import { webStreams } from './streams.ts';

// lan says a daemon serves this page to a phone, not a hub.
export const lan = document.querySelector('meta[name="agentbox-lan"]') !== null;

const storageKey = 'agentbox.environment';

function loadTarget(): EnvironmentTarget {
  if (lan) return { kind: 'local' };
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
const apiBase = () => (lan ? '/api' : `/v1/environments/${encodeURIComponent(target.environmentId ?? '')}/api`);

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
const noticeClickListeners = new Set<(id: string) => void>();

function setConnection(next: ConnectionState) {
  connection = next;
  for (const fn of connectionListeners) fn(next);
}

function followEvents() {
  source?.close();
  source = undefined;
  if (!lan && !target.environmentId) return;
  setConnection({ state: 'connecting' });
  const es = new EventSource(`${apiBase()}/v1/events`, { withCredentials: true });
  source = es;
  es.onopen = () => setConnection({ state: 'connected' });
  es.onerror = () => {
    if (source !== es) return;
    if (lan) {
      // EventSource retries by itself. A phone that was unpaired meanwhile
      // is sent back to pairing.
      setConnection({ state: 'disconnected', error: t('web.phone.unreachable') });
      void fetch('/lan/session', { credentials: 'same-origin' })
        .then((res) => {
          if (res.status === 401) location.reload();
        })
        .catch(() => {});
      return;
    }
    // EventSource retries by itself; say why when the hub can tell.
    setConnection({ state: 'disconnected', error: target.environmentName ? t('web.bridge.envUnreachable', { name: target.environmentName }) : t('web.bridge.theEnvUnreachable') });
    void hubCall<HubEnvironment[]>('GET', '/v1/environments')
      .then((envs) => {
        const env = envs.find((e) => e.id === target.environmentId);
        if (env && !env.online && source === es) setConnection({ state: 'disconnected', error: t('web.bridge.envOffline', { name: env.name }) });
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
  const types = [T.EventJob, T.EventJobLog, T.EventAgent, T.EventUsage, T.EventProject, T.EventMedia, T.EventPulls, T.EventTheme, T.EventUpdate, T.EventLAN, T.EventChat, T.EventChatCache, T.EventQuestion, T.EventAgentEvent];
  for (const type of types) es.addEventListener(type, deliver as EventListener);
}

function listen<F>(set: Set<F>, fn: F): () => void {
  set.add(fn);
  return () => {
    set.delete(fn);
  };
}

// WebSocket streams: terminals, and the browser and Android views.
const { stream, closeAll: closeStreams } = webStreams(
  (path) => `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${apiBase()}${path}`,
);

const unavailable = (message: MessageKey) => () => Promise.reject(new Error(t(message)));

export const webBridge: Bridge & { web: true; lan: boolean } = {
  web: true,
  lan,
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
  // The browser's own Chromium, whose switches the app can't choose.
  voiceGPU: () => Promise.resolve({ saved: { vulkan: false }, running: { vulkan: false }, platform: 'web' }),
  setVoiceGPU: () => Promise.resolve(),
  stream,
  cli: {
    status: () =>
      Promise.resolve<CliStatus>({ linkPath: '', linked: false, path: null, version: null, onPath: false, bundled: false, binary: null }),
    install: unavailable('web.bridge.installCli'),
  },
  hostSetup: {
    status: () => Promise.resolve<HostSetupStatus>({ pkexec: null, user: '', running: false, resizing: false, vm: null, wsl: null }),
    run: unavailable('web.bridge.hostSetup'),
    onOutput: () => () => {},
  },
  vmMigrate: {
    status: () => Promise.resolve(null),
    run: unavailable('web.bridge.vmMigrate'),
    removeOld: unavailable('web.bridge.vmRemoveOld'),
    onOutput: () => () => {},
  },
  vm: {
    resize: unavailable('web.bridge.vmResize'),
    swap: unavailable('web.bridge.vmSwap'),
    onSwapOutput: () => () => {},
    onOutput: () => () => {},
    power: () => Promise.resolve(null),
    act: unavailable('web.bridge.vmPower'),
    disk: () => Promise.resolve(null),
  },
  hubs: {
    list: async () => {
      if (lan) return [];
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
  // The web app has no main process: a report carries only the daemon's
  // sections and the page's own errors.
  report: {
    sections: () => Promise.resolve([]),
    windowError: () => {},
    onAppError: () => () => {},
    openLogs: unavailable('web.bridge.openLogs'),
  },
  pickDirectory: () => Promise.resolve(null),
  setLanguage: () => {},
  openPath: unavailable('web.bridge.openPath'),
  showItem: unavailable('web.bridge.showItem'),
  openExternal: (url) => {
    window.open(url, '_blank', 'noopener');
    return Promise.resolve();
  },
  // The browser's own notifications, while the tab is hidden, once it's
  // allowed them (lib/notifications.ts asks on the first one).
  notify: async (notice) => {
    if (typeof Notification === 'undefined' || document.visibilityState === 'visible') return false;
    if (Notification.permission === 'default') await Notification.requestPermission();
    if (Notification.permission !== 'granted') return false;
    const n = new Notification(notice.title, { body: notice.body, tag: notice.id });
    n.onclick = () => {
      window.focus();
      n.close();
      for (const fn of noticeClickListeners) fn(notice.id);
    };
    return true;
  },
  onNotificationClick: (fn) => listen(noticeClickListeners, fn),
  copyText: (text) => void navigator.clipboard?.writeText(text),
  readText: () => navigator.clipboard?.readText() ?? Promise.resolve(''),
};

// installWebBridge makes the bridge the app uses, and follows the chosen
// environment's events; a phone's once it's paired (followPhoneEvents).
export function installWebBridge(): void {
  (window as unknown as { agentbox: typeof webBridge }).agentbox = webBridge;
  if (!lan) followEvents();
}

export function followPhoneEvents(): void {
  followEvents();
}

export function currentWebTarget(): EnvironmentTarget {
  return target;
}
