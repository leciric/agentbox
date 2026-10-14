// What tells you an agent just landed in "Ready to review": a chime, an OS
// notification while the window isn't in front, and a "new" mark on the
// button's count and the row until the list is opened. Whether to chime and
// how loud is how this app sounds on this computer, so it lives in
// localStorage beside the other display settings (pageStackMode.ts).
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

// A review item is newly ready once per finish: the fingerprint changes when
// the agent finishes again.
export const readyKey = (item: Pick<T.ReviewItem, 'ref' | 'fingerprint'>) => `${item.ref}:${item.fingerprint}`;

// detectNewlyReady compares the list with the finishes already known. The
// first list is the baseline: agents already waiting when the app starts are
// not news. known is returned grown, never pruned, so an agent that drops
// off and comes back with the same finish isn't announced twice.
export function detectNewlyReady(known: ReadonlySet<string> | null, items: readonly T.ReviewItem[]): { fresh: T.ReviewItem[]; known: Set<string> } {
  const next = new Set(known ?? []);
  const fresh = known ? items.filter((i) => !known.has(readyKey(i))) : [];
  for (const i of items) next.add(readyKey(i));
  return { fresh, known: next };
}

// batch collects what arrives within ms of each other and hands it over
// once, so several agents finishing together make one chime and one notice.
export function batch<V>(ms: number, fn: (all: V[]) => void) {
  let pending: V[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  return {
    add(items: readonly V[]) {
      if (!items.length) return;
      pending.push(...items);
      clearTimeout(timer);
      timer = setTimeout(() => {
        const all = pending;
        pending = [];
        timer = undefined;
        fn(all);
      }, ms);
    },
    cancel() {
      clearTimeout(timer);
      pending = [];
    },
  };
}

// A tiny observable value, persisted.
function stored<V>(key: string, fallback: V, parse: (raw: string) => V | undefined) {
  const listeners = new Set<() => void>();
  let current: V | undefined;
  const get = (): V => {
    if (current === undefined) {
      try {
        const raw = localStorage.getItem(key);
        current = (raw === null ? undefined : parse(raw)) ?? fallback;
      } catch {
        current = fallback;
      }
    }
    return current;
  };
  const set = (v: V) => {
    current = v;
    try {
      localStorage.setItem(key, String(v));
    } catch {
      // Kept for this session, if not for the next.
    }
    for (const l of listeners) l();
  };
  const use = () =>
    useSyncExternalStore(
      (l) => {
        listeners.add(l);
        return () => listeners.delete(l);
      },
      get,
    );
  return { get, set, use };
}

export const defaultVolume = 60;
const soundOn = stored<boolean>('agentbox.review.sound', true, (r) => (r === 'false' ? false : r === 'true' ? true : undefined));
const volume = stored<number>('agentbox.review.volume', defaultVolume, (r) => {
  const n = Number(r);
  return Number.isFinite(n) ? Math.min(100, Math.max(0, Math.round(n))) : undefined;
});
export const reviewSoundOn = soundOn.get;
export const setReviewSound = soundOn.set;
export const useReviewSound = soundOn.use;
export const reviewVolume = volume.get;
export const setReviewVolume = volume.set;
export const useReviewVolume = volume.use;

let audio: AudioContext | undefined;

// playReadySound is a short two-note chime made with WebAudio, so there's no
// file to license or ship. volume is 0–100.
export function playReadySound(vol: number = reviewVolume()) {
  const level = Math.min(100, Math.max(0, vol)) / 100;
  if (level === 0 || typeof AudioContext === 'undefined') return;
  try {
    audio ??= new AudioContext();
    const ctx = audio;
    void ctx.resume();
    const t0 = ctx.currentTime + 0.02;
    // E5 then A5: a rising fourth, soft attack, a bell-like decay.
    [
      [659.25, 0],
      [880, 0.13],
    ].forEach(([freq, at]) => {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = 'sine';
      osc.frequency.value = freq;
      const start = t0 + at;
      gain.gain.setValueAtTime(0.0001, start);
      gain.gain.exponentialRampToValueAtTime(0.3 * level * level + 0.0002, start + 0.015);
      gain.gain.exponentialRampToValueAtTime(0.0001, start + 0.55);
      osc.connect(gain).connect(ctx.destination);
      osc.start(start);
      osc.stop(start + 0.6);
    });
  } catch {
    // No audio output: the visual mark still says it.
  }
}

// The finishes announced and not yet looked at, for the "new" marks. Opening
// the list clears them.
let unseen: ReadonlySet<string> = new Set();
const unseenListeners = new Set<() => void>();
const emitUnseen = () => {
  for (const l of unseenListeners) l();
};
export function markUnseen(items: readonly T.ReviewItem[]) {
  if (!items.length) return;
  unseen = new Set([...unseen, ...items.map(readyKey)]);
  emitUnseen();
}
export function markAllSeen() {
  if (!unseen.size) return;
  unseen = new Set();
  emitUnseen();
}
export const isUnseen = (set: ReadonlySet<string>, item: T.ReviewItem) => set.has(readyKey(item));
export function useUnseen(): ReadonlySet<string> {
  return useSyncExternalStore(
    (l) => {
      unseenListeners.add(l);
      return () => unseenListeners.delete(l);
    },
    () => unseen,
  );
}
