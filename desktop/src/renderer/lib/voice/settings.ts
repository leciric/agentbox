// How replies are read aloud, kept in this app's localStorage: it is how this
// computer sounds, not something the daemon or the agents need to know.
import { useSyncExternalStore } from 'react';
import type { VoiceLanguage } from './speakable.ts';

export type ReadAloudSettings = { on: boolean; voice: string; speed: number };

// Kokoro's voices, a few of each language: its best-rated English ones, and
// all three Brazilian Portuguese.
export const voices: { id: string; label: string; language: VoiceLanguage }[] = [
  { id: 'af_heart', label: 'Heart — English (US), female', language: 'en' },
  { id: 'af_bella', label: 'Bella — English (US), female', language: 'en' },
  { id: 'am_michael', label: 'Michael — English (US), male', language: 'en' },
  { id: 'bf_emma', label: 'Emma — English (UK), female', language: 'en' },
  { id: 'bm_george', label: 'George — English (UK), male', language: 'en' },
  { id: 'pf_dora', label: 'Dora — Português (Brasil), feminina', language: 'pt' },
  { id: 'pm_alex', label: 'Alex — Português (Brasil), masculino', language: 'pt' },
  { id: 'pm_santa', label: 'Santa — Português (Brasil), masculino', language: 'pt' },
];

export const speeds = [0.8, 0.9, 1, 1.1, 1.25, 1.5];

export const languageOf = (voice: string): VoiceLanguage => voices.find((v) => v.id === voice)?.language ?? 'en';

const storageKey = 'agentbox.voice.readAloud';

function defaults(): ReadAloudSettings {
  const pt = typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('pt');
  return { on: false, voice: pt ? 'pf_dora' : 'af_heart', speed: 1 };
}

let current: ReadAloudSettings | undefined;
const listeners = new Set<() => void>();

export function readAloudSettings(): ReadAloudSettings {
  if (!current) {
    current = defaults();
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<ReadAloudSettings>;
      if (typeof saved.on === 'boolean') current.on = saved.on;
      if (voices.some((v) => v.id === saved.voice)) current.voice = saved.voice!;
      if (typeof saved.speed === 'number' && speeds.includes(saved.speed)) current.speed = saved.speed;
    } catch {
      // Nothing saved, or no storage: the defaults.
    }
  }
  return current;
}

export function setReadAloud(change: Partial<ReadAloudSettings>): void {
  current = { ...readAloudSettings(), ...change };
  try {
    localStorage.setItem(storageKey, JSON.stringify(current));
  } catch {
    // Kept for this run only.
  }
  for (const listener of listeners) listener();
}

export function useReadAloudSettings(): ReadAloudSettings {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    readAloudSettings,
  );
}
