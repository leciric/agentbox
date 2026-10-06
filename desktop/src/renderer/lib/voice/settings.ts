// Push-to-talk's settings are this app's own, not the daemon's: they're about
// this machine's microphone and GPU, so they live in localStorage beside the
// app's other local choices (see drafts.ts).
import { useSyncExternalStore } from 'react';
import type { MessageKey } from '../../../shared/i18n/index.ts';
import { autoModel, type VoiceLanguage } from './models';
import type { VoiceLanguage as ReadAloudLanguage } from './speakable.ts';

export type VoiceSettings = {
  // model is a voiceModels id, or auto.
  model: string;
  // send: what you said goes as soon as it's transcribed; edit: it waits in
  // the composer for you to send.
  after: 'send' | 'edit';
  language: VoiceLanguage;
};

export const defaultVoiceSettings: VoiceSettings = { model: autoModel, after: 'edit', language: 'auto' };

const storageKey = 'agentbox.voice';
const listeners = new Set<() => void>();
let current: VoiceSettings | undefined;

export function voiceSettings(): VoiceSettings {
  if (!current) {
    try {
      current = { ...defaultVoiceSettings, ...(JSON.parse(localStorage.getItem(storageKey) ?? '{}') as Partial<VoiceSettings>) };
    } catch {
      current = defaultVoiceSettings;
    }
  }
  return current;
}

export function setVoiceSettings(change: Partial<VoiceSettings>) {
  current = { ...voiceSettings(), ...change };
  try {
    localStorage.setItem(storageKey, JSON.stringify(current));
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const listener of listeners) listener();
}

export function useVoiceSettings(): VoiceSettings {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    voiceSettings,
  );
}

// How replies are read aloud, kept in this app's localStorage: it is how this
// computer sounds, not something the daemon or the agents need to know.

// voices is the voice for each language: a reply is read by the one for its
// own (language.ts).
export type ReadAloudSettings = { on: boolean; voices: Record<ReadAloudLanguage, string>; speed: number };

// Kokoro's voices, a few of each language: its best-rated English ones, and
// all three Brazilian Portuguese.
export const voices: { id: string; label: MessageKey; language: ReadAloudLanguage }[] = [
  { id: 'af_heart', label: 'defaults.voice.v.af_heart', language: 'en' },
  { id: 'af_bella', label: 'defaults.voice.v.af_bella', language: 'en' },
  { id: 'am_michael', label: 'defaults.voice.v.am_michael', language: 'en' },
  { id: 'bf_emma', label: 'defaults.voice.v.bf_emma', language: 'en' },
  { id: 'bm_george', label: 'defaults.voice.v.bm_george', language: 'en' },
  { id: 'pf_dora', label: 'defaults.voice.v.pf_dora', language: 'pt' },
  { id: 'pm_alex', label: 'defaults.voice.v.pm_alex', language: 'pt' },
  { id: 'pm_santa', label: 'defaults.voice.v.pm_santa', language: 'pt' },
];

export const speeds = [0.8, 0.9, 1, 1.1, 1.25, 1.5];

export const languageOf = (voice: string): ReadAloudLanguage | undefined => voices.find((v) => v.id === voice)?.language;

const readAloudStorageKey = 'agentbox.voice.readAloud';

// fallbackLanguage is what a reply is taken to be in when its words don't say:
// this computer's.
export function fallbackLanguage(): ReadAloudLanguage {
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('pt') ? 'pt' : 'en';
}

function defaults(): ReadAloudSettings {
  return { on: false, voices: { en: 'af_heart', pt: 'pf_dora' }, speed: 1 };
}

let currentReadAloud: ReadAloudSettings | undefined;
const readAloudListeners = new Set<() => void>();

export function readAloudSettings(): ReadAloudSettings {
  if (!currentReadAloud) {
    currentReadAloud = defaults();
    try {
      const saved = JSON.parse(localStorage.getItem(readAloudStorageKey) ?? '{}') as Partial<ReadAloudSettings> & { voice?: string };
      if (typeof saved.on === 'boolean') currentReadAloud.on = saved.on;
      // One voice for everything, as it was before: it stays its language's.
      for (const id of [saved.voice, saved.voices?.en, saved.voices?.pt]) {
        const language = languageOf(id ?? '');
        if (language) currentReadAloud.voices = { ...currentReadAloud.voices, [language]: id! };
      }
      if (typeof saved.speed === 'number' && speeds.includes(saved.speed)) currentReadAloud.speed = saved.speed;
    } catch {
      // Nothing saved, or no storage: the defaults.
    }
  }
  return currentReadAloud;
}

export function setReadAloud(change: Partial<ReadAloudSettings>): void {
  currentReadAloud = { ...readAloudSettings(), ...change };
  try {
    localStorage.setItem(readAloudStorageKey, JSON.stringify(currentReadAloud));
  } catch {
    // Kept for this run only.
  }
  for (const listener of readAloudListeners) listener();
}

export function useReadAloudSettings(): ReadAloudSettings {
  return useSyncExternalStore(
    (listener) => {
      readAloudListeners.add(listener);
      return () => readAloudListeners.delete(listener);
    },
    readAloudSettings,
  );
}
