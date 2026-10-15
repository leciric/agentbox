// The daemon this phone is paired with, and its token. Pairing posts the QR
// code's secret to /lan/pair asking for the token (LANPairRequest.bearer),
// which the web page never gets (it keeps an HttpOnly cookie): the app keeps it
// in the phone's secure storage (Keychain, Keystore) and sends it as
// "Authorization: Bearer" on every request, the stream of events included.
// Its scopes are the phone's, set in AgentBox on the computer (#294).
// After t3code's apps/mobile/src/persistence/mobile-secure-storage.ts.
import { useSyncExternalStore } from 'react';
import { Platform } from 'react-native';
import * as SecureStore from 'expo-secure-store';
import Constants from 'expo-constants';
import type * as T from '../shared/api.ts';
import { parsePairingLink } from './pairing.ts';

export interface Session {
  origin: string;
  token: string;
  phone: T.LANPhone;
}

const key = 'agentbox.session';

// Secure storage on a phone; the browser's storage for Expo's web build, which
// is for development (scripts/lan-forward.mjs).
const storage = {
  get: async () => (Platform.OS === 'web' ? globalThis.localStorage?.getItem(key) ?? null : SecureStore.getItemAsync(key)),
  set: async (value: string) => (Platform.OS === 'web' ? globalThis.localStorage?.setItem(key, value) : SecureStore.setItemAsync(key, value)),
  remove: async () => (Platform.OS === 'web' ? globalThis.localStorage?.removeItem(key) : SecureStore.deleteItemAsync(key)),
};

// undefined while it is read from storage at start, null when not paired.
let current: Session | null | undefined;
const listeners = new Set<() => void>();

function set(next: Session | null) {
  current = next;
  for (const fn of listeners) fn();
}

export function session(): Session | null | undefined {
  return current;
}

export function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useSession(): Session | null | undefined {
  return useSyncExternalStore(subscribe, session, session);
}

export async function loadSession(): Promise<void> {
  try {
    const raw = await storage.get();
    set(raw ? (JSON.parse(raw) as Session) : null);
  } catch {
    set(null);
  }
}

// phoneName is what AgentBox on the computer lists this phone as.
export function phoneName(): string {
  return Constants.deviceName || (Platform.OS === 'ios' ? 'iPhone' : Platform.OS === 'android' ? 'Android phone' : 'Phone app');
}

async function errorOf(res: Response): Promise<string> {
  const text = await res.text().catch(() => '');
  try {
    return (JSON.parse(text) as T.Error).error || `HTTP ${res.status}`;
  } catch {
    return text.trim() || `HTTP ${res.status}`;
  }
}

// pair pairs with the daemon a QR code or pasted link points at.
export async function pair(link: string): Promise<Session> {
  const parsed = parsePairingLink(link);
  if (!parsed) throw new Error('not a pairing link');
  const res = await fetch(`${parsed.origin}/lan/pair`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret: parsed.secret, name: phoneName(), bearer: true } satisfies T.LANPairRequest),
  });
  if (!res.ok) throw new Error(await errorOf(res));
  const out = (await res.json()) as T.LANSession;
  if (!out.token) throw new Error('this AgentBox is too old for the phone app: update it on your computer');
  const next = { origin: parsed.origin, token: out.token, phone: out.phone };
  await storage.set(JSON.stringify(next));
  set(next);
  return next;
}

// forget drops the pairing on this phone only: the daemon revoked it, or it
// is being unpaired.
export async function forget(): Promise<void> {
  await storage.remove();
  set(null);
}

// unpair revokes this phone in AgentBox, then forgets it.
export async function unpair(): Promise<void> {
  const s = current;
  if (s) await fetch(`${s.origin}/lan/unpair`, { method: 'POST', headers: { Authorization: `Bearer ${s.token}` } }).catch(() => {});
  await forget();
}

// refreshPhone reads the phone's scopes again, which the computer may have
// changed, and notices a pairing revoked meanwhile.
export async function refreshPhone(): Promise<void> {
  const s = current;
  if (!s) return;
  const res = await fetch(`${s.origin}/lan/session`, { headers: { Authorization: `Bearer ${s.token}` } });
  if (res.status === 401) return forget();
  if (!res.ok) return;
  const { phone } = (await res.json()) as T.LANSession;
  if (JSON.stringify(phone.scopes) === JSON.stringify(s.phone.scopes) && phone.name === s.phone.name) return;
  const next = { ...s, phone };
  await storage.set(JSON.stringify(next));
  set(next);
}

// can says whether the phone has a scope: "read", "chat", "answer", "merge".
export const can = (s: Session | null | undefined, scope: string) => !!s?.phone.scopes?.includes(scope);
