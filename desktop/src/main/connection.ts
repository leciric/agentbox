// Where the app's requests go: this machine's daemon over its unix socket, or
// an environment on a hub, over HTTPS with the hub session's token. Everything
// that talks to a daemon (API calls, the event stream, WebSocket streams, media)
// asks this module for the way there, so switching environments is one place.
import { app, safeStorage } from 'electron';
import { mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import { dirname, join } from 'node:path';
import { socketPath } from './paths';
import { onWindows, relaySocket } from './relay';

export interface SavedHub {
  url: string; // like https://hub.example.com
  email: string;
  token: string;
}

export type Target = { kind: 'local' } | { kind: 'hub'; hub: SavedHub; environmentId: string; environmentName: string } | { kind: 'url'; url: string };

// normalizeServerURL is a daemon's own address as this app should keep it: a
// scheme (https when none is given), no trailing slash, and a path when
// tailscale serve publishes it under one.
export function normalizeServerURL(input: string): string {
  let text = input.trim();
  if (!/^https?:\/\//i.test(text)) text = `https://${text}`;
  const url = new URL(text);
  if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new Error(`not an http or https address: ${input.trim()}`);
  if (url.username || url.password) throw new Error('the address should carry no user or password: tailscale says who is asking');
  return url.toString().replace(/\/+$/, '');
}

// savedServer is the daemon this app last connected over its own address, or
// '' when it never has. It is kept beside the hubs, so the switcher can offer
// it again after a restart.
const serversFile = () => join(app.getPath('userData'), 'servers.json');

export function savedServer(): string {
  try {
    const saved = JSON.parse(readFileSync(serversFile(), 'utf8')) as { url?: string };
    return saved.url ? normalizeServerURL(saved.url) : '';
  } catch {
    return '';
  }
}

export function saveServer(url: string): void {
  if (!url) {
    rmSync(serversFile(), { force: true });
    return;
  }
  mkdirSync(dirname(serversFile()), { recursive: true });
  writeFileSync(`${serversFile()}.tmp`, JSON.stringify({ url }, null, 2), { mode: 0o600 });
  renameSync(`${serversFile()}.tmp`, serversFile());
}

const firstServer = savedServer();
let target: Target = firstServer ? { kind: 'url', url: firstServer } : { kind: 'local' };
const listeners = new Set<(target: Target) => void>();

export function currentTarget(): Target {
  return target;
}

// setTarget switches every later request to another environment.
export function setTarget(next: Target): void {
  target = next;
  for (const fn of listeners) fn(next);
}

export function onTargetChange(fn: (target: Target) => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function isLocal(t: Target = target): t is { kind: 'local' } {
  return t.kind === 'local';
}

// localSocket is where this machine's daemon is reached: its unix socket, or on
// Windows the relay's named pipe into AgentBox's WSL distro (relay.ts), which
// node:http and ws connect to the same way.
export function localSocket(): string {
  return onWindows ? relaySocket() : socketPath;
}

// requestOptions are node:http(s) options for an API path on the current target.
export function requestOptions(path: string, t: Target = target): { module: typeof http | typeof https; options: http.RequestOptions } {
  if (t.kind === 'local') return { module: http, options: { socketPath: localSocket(), path } };
  if (t.kind === 'url') {
    const url = new URL(t.url + path);
    return {
      module: url.protocol === 'https:' ? https : http,
      options: { protocol: url.protocol, hostname: url.hostname, port: url.port, path: url.pathname + url.search },
    };
  }
  const url = new URL(`${t.hub.url}/v1/environments/${encodeURIComponent(t.environmentId)}/api${path}`);
  return {
    module: url.protocol === 'https:' ? https : http,
    options: {
      protocol: url.protocol,
      hostname: url.hostname,
      port: url.port,
      path: url.pathname + url.search,
      headers: { Authorization: `Bearer ${t.hub.token}` },
    },
  };
}

// webSocketAddress is the address and headers of a WebSocket endpoint on the
// current target. A named pipe's path doesn't fit in a ws+unix: URL, so on
// Windows the socket is given as an option instead.
export function webSocketAddress(path: string, t: Target = target): { url: string; headers: Record<string, string>; socketPath?: string } {
  if (t.kind === 'local' && onWindows) return { url: `ws://agentbox${path}`, headers: {}, socketPath: relaySocket() };
  if (t.kind === 'local') return { url: `ws+unix://${socketPath}:${path}`, headers: {} };
  if (t.kind === 'url') {
    const url = new URL(t.url + path);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    return { url: url.toString(), headers: {} };
  }
  const base = t.hub.url.replace(/^http/, 'ws');
  return { url: `${base}/v1/environments/${encodeURIComponent(t.environmentId)}/api${path}`, headers: { Authorization: `Bearer ${t.hub.token}` } };
}

// Hub sessions are kept in the app's data directory, with the tokens encrypted
// by the OS keyring when one is available.
const hubsFile = () => join(app.getPath('userData'), 'hubs.json');

interface StoredHub {
  url: string;
  email: string;
  token: string; // base64 of the encrypted token, or the token itself
  encrypted: boolean;
}

export function savedHubs(): SavedHub[] {
  try {
    const stored = JSON.parse(readFileSync(hubsFile(), 'utf8')) as StoredHub[];
    return stored.map((h) => ({
      url: h.url,
      email: h.email,
      token: h.encrypted ? safeStorage.decryptString(Buffer.from(h.token, 'base64')) : h.token,
    }));
  } catch {
    return [];
  }
}

export function saveHubs(hubs: SavedHub[]): void {
  const encrypted = safeStorage.isEncryptionAvailable();
  const stored: StoredHub[] = hubs.map((h) => ({
    url: h.url,
    email: h.email,
    token: encrypted ? safeStorage.encryptString(h.token).toString('base64') : h.token,
    encrypted,
  }));
  mkdirSync(dirname(hubsFile()), { recursive: true });
  writeFileSync(`${hubsFile()}.tmp`, JSON.stringify(stored, null, 2), { mode: 0o600 });
  renameSync(`${hubsFile()}.tmp`, hubsFile());
}
