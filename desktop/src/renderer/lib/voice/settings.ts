// Push-to-talk's settings are this app's own, not the daemon's: they're about
// this machine's microphone and GPU, so they live in localStorage beside the
// app's other local choices (see drafts.ts).
import { useSyncExternalStore } from 'react';
import { autoModel, type VoiceLanguage } from './models';

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
