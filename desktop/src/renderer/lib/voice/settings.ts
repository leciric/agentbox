// How replies are read aloud, kept in this app's localStorage: it is how this
// computer sounds, not something the daemon or the agents need to know.
import { useSyncExternalStore } from 'react';
import type { VoiceLanguage } from './speakable.ts';

// voices is the voice for each language: a reply is read by the one for its
// own (language.ts).
export type ReadAloudSettings = { on: boolean; voices: Record<VoiceLanguage, string>; speed: number };

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

export const languageOf = (voice: string): VoiceLanguage | undefined => voices.find((v) => v.id === voice)?.language;

const storageKey = 'agentbox.voice.readAloud';

// fallbackLanguage is what a reply is taken to be in when its words don't say:
// this computer's.
export function fallbackLanguage(): VoiceLanguage {
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('pt') ? 'pt' : 'en';
}

function defaults(): ReadAloudSettings {
  return { on: false, voices: { en: 'af_heart', pt: 'pf_dora' }, speed: 1 };
}

let current: ReadAloudSettings | undefined;
const listeners = new Set<() => void>();

export function readAloudSettings(): ReadAloudSettings {
  if (!current) {
    current = defaults();
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<ReadAloudSettings> & { voice?: string };
      if (typeof saved.on === 'boolean') current.on = saved.on;
      // One voice for everything, as it was before: it stays its language's.
      for (const id of [saved.voice, saved.voices?.en, saved.voices?.pt]) {
        const language = languageOf(id ?? '');
        if (language) current.voices = { ...current.voices, [language]: id! };
      }
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
