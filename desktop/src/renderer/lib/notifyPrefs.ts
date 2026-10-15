// What may make a sound or a system notification, and when. Like the chime's
// volume (reviewAlert.ts) this is how this app behaves on this computer, so it
// lives in localStorage beside the other display settings. The daemon doesn't
// notify anyone yet (the bell, toasts and OS notices are all the app's); when
// something of its own does, such as the phone app's push, these move to the
// daemon's settings (PATCH /v1/settings) for it to read.
//
// The in-app toast and the bell are never silenced: they sit in the window and
// make no noise. Do not disturb and the per-kind switches decide only the
// sound and the notification the system shows.
import { useEffect, useSyncExternalStore } from 'react';

// review: an agent lands in Ready to review; question: something asks for
// your answer or permission; error: an agent stopped on an error.
export const kinds = ['review', 'question', 'error'] as const;
export type Kind = (typeof kinds)[number];
export type Channel = 'sound' | 'system';
export type KindPrefs = Record<Channel, boolean>;

export interface Prefs {
  kinds: Record<Kind, KindPrefs>;
  // dndUntil is when Do not disturb ends, as epoch milliseconds: 0 is off and
  // forever is until it's turned off.
  dndUntil: number;
}

export const forever = Number.MAX_SAFE_INTEGER;
export const storageKey = 'agentbox.notifications';
const legacySoundKey = 'agentbox.review.sound';

export const defaults = (): Prefs => ({
  kinds: {
    review: { sound: true, system: true },
    question: { sound: true, system: true },
    error: { sound: true, system: true },
  },
  dndUntil: 0,
});

// parsePrefs reads what was stored, keeping whatever is valid and falling back
// to the default for the rest. What was stored for "finished" (before it was
// only Ready to review) carries over to review, and with nothing stored the
// chime's old switch (before it was one of these) still decides its sound.
export function parsePrefs(raw: string | null, legacySound?: string | null): Prefs {
  const out = defaults();
  if (raw === null) {
    if (legacySound === 'false') out.kinds.review.sound = false;
    return out;
  }
  try {
    const data = JSON.parse(raw) as { kinds?: Record<string, Partial<KindPrefs>>; dndUntil?: unknown };
    for (const k of kinds) {
      const p = data.kinds?.[k] ?? (k === 'review' ? data.kinds?.finished : undefined);
      if (typeof p?.sound === 'boolean') out.kinds[k].sound = p.sound;
      if (typeof p?.system === 'boolean') out.kinds[k].system = p.system;
    }
    if (typeof data.dndUntil === 'number' && Number.isFinite(data.dndUntil)) out.dndUntil = Math.max(0, data.dndUntil);
  } catch {
    // Not ours: the defaults.
  }
  return out;
}

export const dndActive = (p: Pick<Prefs, 'dndUntil'>, now = Date.now()) => p.dndUntil > now;

export type Duration = 'hour' | 'tomorrow' | 'forever';

// dndEnd is when a Do not disturb started now ends. "Until tomorrow" is the
// next 8:00 in the morning.
export function dndEnd(d: Duration, now = Date.now()): number {
  if (d === 'forever') return forever;
  if (d === 'hour') return now + 60 * 60 * 1000;
  const at = new Date(now);
  at.setHours(8, 0, 0, 0);
  if (at.getTime() <= now) at.setDate(at.getDate() + 1);
  return at.getTime();
}

// allowed is whether a kind may use a channel right now.
export function allowed(p: Prefs, kind: Kind, channel: Channel, now = Date.now()): boolean {
  return !dndActive(p, now) && p.kinds[kind][channel];
}

// The first run's one question, which sets every kind at once.
export type Mode = 'on' | 'sound' | 'silent';
export const modes: readonly Mode[] = ['on', 'sound', 'silent'];

export function applyMode(p: Prefs, mode: Mode): Prefs {
  const next = defaults();
  next.dndUntil = p.dndUntil;
  if (mode === 'on') return next;
  for (const k of kinds) next.kinds[k] = { sound: mode === 'sound', system: false };
  return next;
}

// modeOf is the answer the kinds add up to: silent when nothing may make a
// noise or a notice, sound only when nothing may notify, otherwise on.
export function modeOf(p: Prefs): Mode {
  const all = kinds.flatMap((k) => [p.kinds[k].sound, p.kinds[k].system]);
  if (!all.some(Boolean)) return 'silent';
  return kinds.some((k) => p.kinds[k].system) ? 'on' : 'sound';
}

let current: Prefs | undefined;
const listeners = new Set<() => void>();

export function getPrefs(): Prefs {
  if (!current) {
    try {
      current = parsePrefs(localStorage.getItem(storageKey), localStorage.getItem(legacySoundKey));
    } catch {
      current = defaults();
    }
  }
  return current;
}

export function setPrefs(p: Prefs): void {
  current = p;
  try {
    localStorage.setItem(storageKey, JSON.stringify(p));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

export const setKind = (kind: Kind, channel: Channel, on: boolean) =>
  setPrefs({ ...getPrefs(), kinds: { ...getPrefs().kinds, [kind]: { ...getPrefs().kinds[kind], [channel]: on } } });
export const setDnd = (d: Duration | null) => setPrefs({ ...getPrefs(), dndUntil: d ? dndEnd(d) : 0 });
export const setMode = (mode: Mode) => setPrefs(applyMode(getPrefs(), mode));

// mayNotify is the check before a sound or a system notification.
export const mayNotify = (kind: Kind, channel: Channel) => allowed(getPrefs(), kind, channel);

export function usePrefs(): Prefs {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    getPrefs,
  );
}

// useDnd is whether Do not disturb is on, and until when. A timed one turns
// itself off in view when its time comes.
export function useDnd(): { on: boolean; until: number } {
  const p = usePrefs();
  const on = dndActive(p);
  useEffect(() => {
    if (!on || p.dndUntil === forever) return;
    // setTimeout caps at 2^31 ms; the longest timed one is a day.
    const id = setTimeout(() => setPrefs({ ...getPrefs() }), Math.min(p.dndUntil - Date.now() + 50, 2 ** 31 - 1));
    return () => clearTimeout(id);
  }, [on, p.dndUntil]);
  return { on, until: p.dndUntil };
}

// For tests.
export function resetPrefs(): void {
  current = undefined;
}
