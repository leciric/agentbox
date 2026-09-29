// The app's voice: sentences queued to Kokoro's worker, and played
// in order as their audio comes back, while the next ones are being made.
// Skip ends the sentence playing; stop drops everything queued, and whatever
// the worker is still making for it.
import { useSyncExternalStore } from 'react';
import type { WorkerReply, WorkerRequest } from './kokoro.worker';
import { readAloudSettings } from './settings.ts';
import type { VoiceLanguage } from './speakable.ts';

export type ReaderState = {
  // loading is the first use: Kokoro and eSpeak NG downloading, or starting.
  status: 'idle' | 'loading' | 'speaking';
  // progress of that download, 0 to 1, once it's known.
  progress?: number;
  device?: 'webgpu' | 'wasm';
  error?: string;
};

type Entry = { id: number; audio?: AudioBuffer; failed?: boolean };

let state: ReaderState = { status: 'idle' };
const listeners = new Set<() => void>();
const set = (change: Partial<ReaderState>) => {
  state = { ...state, ...change };
  for (const listener of listeners) listener();
};

let worker: Worker | undefined;
let context: AudioContext | undefined;
let ready = false;
let generation = 1;
let nextId = 1;
let queue: Entry[] = [];
let playing: AudioBufferSourceNode | undefined;

function start(): Worker {
  if (worker) return worker;
  worker = new Worker(new URL('./kokoro.worker.ts', import.meta.url), { type: 'module' });
  worker.onmessage = ({ data }: MessageEvent<WorkerReply>) => {
    switch (data.type) {
      case 'progress':
        if (!ready && data.total > 0) set({ progress: data.loaded / data.total });
        break;
      case 'ready':
        ready = true;
        set({ device: data.device, progress: undefined });
        break;
      case 'audio': {
        const entry = queue.find((e) => e.id === data.id);
        if (data.generation !== generation || !entry || !context) return;
        const buffer = context.createBuffer(1, data.samples.length, data.sampleRate);
        buffer.copyToChannel(data.samples as Float32Array<ArrayBuffer>, 0);
        entry.audio = buffer;
        pump();
        break;
      }
      case 'error': {
        set({ error: data.message });
        const entry = queue.find((e) => e.id === data.id);
        if (data.generation !== generation || !entry) return;
        entry.failed = true;
        pump();
        break;
      }
    }
  };
  worker.onerror = (event) => {
    set({ error: event.message || 'The voice could not start' });
    stop();
    worker?.terminate();
    worker = undefined;
    ready = false;
  };
  return worker;
}

// pump plays the head of the queue once its audio is there.
function pump() {
  while (!playing && queue[0]?.failed) queue.shift();
  const head = queue[0];
  if (!playing && head?.audio && context) {
    const source = context.createBufferSource();
    source.buffer = head.audio;
    source.connect(context.destination);
    source.onended = () => {
      if (playing !== source) return;
      playing = undefined;
      queue.shift();
      pump();
    };
    playing = source;
    source.start();
  }
  status();
}

function status() {
  set({ status: queue.length === 0 ? 'idle' : ready ? 'speaking' : 'loading' });
}

// unlock makes the audio output ready while a click is still being handled:
// a browser (the web app) keeps sound started any later muted.
export function unlock(): void {
  context ??= new AudioContext();
  void context.resume().catch(() => {});
}

// speak queues a sentence in the settings' voice for its language, at their
// speed, as they are now.
export function speak(text: string, language: VoiceLanguage): void {
  const { voices, speed } = readAloudSettings();
  const voice = voices[language];
  unlock();
  const entry: Entry = { id: nextId++ };
  queue.push(entry);
  if (state.error) set({ error: undefined });
  const req: WorkerRequest = { type: 'speak', id: entry.id, generation, text, voice, speed };
  start().postMessage(req);
  status();
}

// skip ends the sentence being read, and goes on to the next.
export function skip(): void {
  playing?.stop();
}

export function stop(): void {
  worker?.postMessage({ type: 'cancel', generation } satisfies WorkerRequest);
  generation++;
  queue = [];
  const source = playing;
  playing = undefined;
  source?.stop();
  status();
}

export function readerState(): ReaderState {
  return state;
}

export function useReader(): ReaderState {
  return useSyncExternalStore((listener) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  }, readerState);
}
