// Crash on start, noticed at the next one, so the app can't crash in a loop.
// Each launch counts itself unfinished (bootRules.ts) until it has stayed up
// a while or gone to the background. A launch that finds the last one
// unfinished clears what the phone keeps of the computer's data — expo-image's
// disk cache of screenshots, and any key in syncKeys — before it syncs, and
// says so on the home screen; after two in a row it waits on a screen that
// shows the error and offers to try again or unpair (app/_layout.tsx). The
// pairing itself stays: it is how the phone reaches the computer at all.
//
// The error itself is kept when the app sees it: a fatal JavaScript error
// (React Native's global handler) or one the root error boundary caught.
import { Image } from 'expo-image';
import * as SecureStore from 'expo-secure-store';
import { AppState, Platform } from 'react-native';
import { begin, healthyAfterMs, parseBoot, type BootMode, type BootRecord } from './bootRules.ts';

const key = 'agentbox.boot';

// What the phone keeps of the computer's data, besides images, cleared after
// a crash on start. The query cache lives in memory only, so it is empty;
// a persisted cache added later belongs here.
const syncKeys: string[] = [];

const store = {
  get: async (k: string) => (Platform.OS === 'web' ? globalThis.localStorage?.getItem(k) ?? null : SecureStore.getItemAsync(k)),
  set: async (k: string, v: string) => (Platform.OS === 'web' ? globalThis.localStorage?.setItem(k, v) : SecureStore.setItemAsync(k, v)),
  remove: async (k: string) => (Platform.OS === 'web' ? globalThis.localStorage?.removeItem(k) : SecureStore.deleteItemAsync(k)),
};

export interface Boot {
  mode: BootMode;
  crashes: number;
  error?: string;
}

let record: BootRecord = { starts: 1 };
let finished = false;
// failed is a start the root error boundary had to catch: it isn't counted
// as finished, however long it stays up showing the error.
let failed = false;

const save = () => store.set(key, JSON.stringify(record)).catch(() => {});

let boot: Boot = { mode: 'normal', crashes: 0 };

// bootState is how the last start ended, once startBoot has read it.
export const bootState = (): Boot => boot;

// startBoot runs once, before anything is read from the computer. It never
// fails: storage it can't read is a start like any other.
export async function startBoot(): Promise<Boot> {
  try {
    const prev = parseBoot(await store.get(key).catch(() => null));
    const next = begin(prev);
    record = next.record;
    boot = { mode: next.mode, crashes: next.crashes, error: next.error };
    await save();
    if (next.mode !== 'normal') await clearSyncCache();
    watch();
  } catch {
    // keep going
  }
  return boot;
}

async function clearSyncCache(): Promise<void> {
  await Promise.all([
    Image.clearDiskCache().catch(() => false),
    Image.clearMemoryCache().catch(() => false),
    ...syncKeys.map((k) => store.remove(k).catch(() => {})),
  ]);
}

// finishBoot says this start went well.
export function finishBoot(): void {
  if (finished || failed) return;
  finished = true;
  record = { starts: 0 };
  void save();
}

// recordCrash keeps the error that is ending this start, for the next one.
export function recordCrash(error: unknown): void {
  if (finished) return;
  failed = true;
  const text = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
  record = { ...record, error: text.slice(0, 500) };
  void save();
}

function watch(): void {
  setTimeout(finishBoot, healthyAfterMs);
  // Put away rather than crashed: the user left it.
  const sub = AppState.addEventListener('change', (state) => {
    if (state === 'background') {
      finishBoot();
      sub.remove();
    }
  });
  // A fatal JavaScript error outside rendering (a timer, a promise's
  // handler) ends the app: keep it first.
  const errorUtils = (globalThis as { ErrorUtils?: { getGlobalHandler(): (e: unknown, fatal?: boolean) => void; setGlobalHandler(h: (e: unknown, fatal?: boolean) => void): void } }).ErrorUtils;
  const previous = errorUtils?.getGlobalHandler();
  errorUtils?.setGlobalHandler((e, fatal) => {
    if (fatal) recordCrash(e);
    previous?.(e, fatal);
  });
}
