// The page's side of whisper.worker.ts: one worker for the whole app, started
// the first time something asks, and what it's doing, for the mic button and
// Settings to show.
import { useSyncExternalStore } from 'react';
import type { VoiceDevice, VoiceLanguage } from './models';
import { t } from '../../../shared/i18n/index.ts';
import { voiceSettings } from './settings';
import type { WorkerReply, WorkerRequest } from './whisper.worker';

export type WhisperState = {
  // device is unknown until the worker has looked; reason says why it isn't
  // WebGPU when it isn't.
  device?: VoiceDevice;
  reason?: string;
  // adapter names the GPU WebGPU found, as it describes it.
  adapter?: string;
  // loading is a model being downloaded or started, with its bytes so far.
  loading?: { model: string; loaded: number; total: number };
  ready?: string;
  error?: string;
};

export type Transcript = { text: string; language: string; ms: { detect: number; transcribe: number } };

let worker: Worker | undefined;
let state: WhisperState = {};
const listeners = new Set<() => void>();
const pending = new Map<number, { resolve: (t: Transcript) => void; reject: (e: Error) => void }>();
let nextId = 1;
// Each file's bytes, so the progress is of the whole model, not of one file.
let files = new Map<string, { loaded: number; total: number }>();

function update(change: Partial<WhisperState>) {
  state = { ...state, ...change };
  for (const listener of listeners) listener();
}

function start(): Worker {
  if (worker) return worker;
  worker = new Worker(new URL('./whisper.worker.ts', import.meta.url), { type: 'module' });
  worker.onmessage = ({ data }: MessageEvent<WorkerReply>) => {
    switch (data.type) {
      case 'device':
        update({ device: data.device, reason: data.reason, adapter: data.adapter });
        break;
      case 'progress': {
        if (state.loading?.model !== data.model) files = new Map();
        files.set(data.file, { loaded: data.loaded, total: data.total });
        let loaded = 0;
        let total = 0;
        for (const f of files.values()) {
          loaded += f.loaded;
          total += f.total;
        }
        update({ loading: { model: data.model, loaded, total }, error: undefined });
        break;
      }
      case 'ready':
        files = new Map();
        update({ loading: undefined, ready: data.model, device: data.device, error: undefined });
        break;
      case 'result':
        pending.get(data.id)?.resolve(data);
        pending.delete(data.id);
        break;
      case 'error':
        update({ loading: undefined, error: data.message });
        if (data.id !== undefined) {
          pending.get(data.id)?.reject(new Error(data.message));
          pending.delete(data.id);
        }
        break;
    }
  };
  worker.onerror = (event) => update({ loading: undefined, error: event.message || t('chat.voice.workerStart') });
  send({ type: 'probe' });
  return worker;
}

function send(request: WorkerRequest, transfer: Transferable[] = []) {
  start().postMessage(request, transfer);
}

// probe finds the device without loading anything.
export function probeWhisper() {
  start();
}

// preload starts the model downloading and loading, so it's ready by the
// time you've finished speaking the first time.
export function preloadWhisper(model = voiceSettings().model) {
  send({ type: 'load', model });
}

export function transcribe(audio: Float32Array, language: VoiceLanguage = voiceSettings().language, model = voiceSettings().model): Promise<Transcript> {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    send({ type: 'transcribe', id, model, audio, language }, [audio.buffer]);
  });
}

// forgetWhisper stops the worker, so a model whose files were just cleared
// isn't still held in memory, and loads again from scratch.
export function forgetWhisper() {
  worker?.terminate();
  worker = undefined;
  for (const p of pending.values()) p.reject(new Error(t('chat.voice.stopped')));
  pending.clear();
  files = new Map();
  update({ loading: undefined, ready: undefined, error: undefined });
}

export function whisperState(): WhisperState {
  return state;
}

export function useWhisper(): WhisperState {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    whisperState,
  );
}
