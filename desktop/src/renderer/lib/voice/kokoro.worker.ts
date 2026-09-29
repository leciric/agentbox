/// <reference lib="webworker" />
// Kokoro-82M, run in a worker so the chat never waits on it: sentences come in,
// audio goes out, one at a time and in order. WebGPU when this machine's
// Chromium has an adapter (with onnxruntime-web patched, ortPatch.mts),
// WebAssembly otherwise. The weights come from
// Hugging Face the first time and stay in the cache; eSpeak NG comes the same
// way (espeak.ts). onnxruntime's own WebAssembly is bundled: it is MIT.
import { env as transformers } from '@huggingface/transformers';
import { env, KokoroTTS } from 'kokoro-js';
import ortModule from '../../../../node_modules/onnxruntime-web/dist/ort-wasm-simd-threaded.jsep.mjs?url';
import ortWasm from '../../../../node_modules/onnxruntime-web/dist/ort-wasm-simd-threaded.jsep.wasm?url';
import { loadEspeak, onEspeakProgress, phones } from './espeak.ts';
import { espeakPhonemes } from './g2p.ts';

export type WorkerRequest =
  | { type: 'speak'; id: number; generation: number; text: string; voice: string; speed: number }
  | { type: 'cancel'; generation: number };

export type WorkerReply =
  | { type: 'progress'; loaded: number; total: number }
  | { type: 'ready'; device: 'webgpu' | 'wasm' }
  | { type: 'audio'; id: number; generation: number; samples: Float32Array; sampleRate: number }
  | { type: 'error'; id?: number; generation?: number; message: string };

const model = 'onnx-community/Kokoro-82M-v1.0-ONNX';
const post = (reply: WorkerReply, transfer: Transferable[] = []) => (self as unknown as DedicatedWorkerGlobalScope).postMessage(reply, transfer);

// Threads need SharedArrayBuffer, which a page only has when it's
// cross-origin isolated, and the app's isn't: turning it on for the whole
// renderer would hand the same high-resolution timers to the HTML reports
// agents leave in Media. Where a page does have it, use it.
if (typeof SharedArrayBuffer !== 'undefined') transformers.backends.onnx.wasm!.numThreads = Math.min(4, Math.ceil(navigator.hardwareConcurrency / 2));
env.wasmPaths = { mjs: new URL(ortModule, self.location.href).href, wasm: new URL(ortWasm, self.location.href).href } as unknown as string;

// What has come down so far, of every file the first load fetches. Hugging
// Face's CDN doesn't always say how big the weights are, so their sizes are
// known here too.
const weights: Record<string, number> = { 'onnx/model.onnx': 325532232, 'onnx/model_quantized.onnx': 92361116 };
const files = new Map<string, { loaded: number; total: number }>();
const report = (file: string, loaded: number, total: number) => {
  files.set(file, { loaded, total: Math.max(total || weights[file] || 0, loaded) });
  let l = 0;
  let t = 0;
  for (const f of files.values()) {
    l += f.loaded;
    t += f.total;
  }
  post({ type: 'progress', loaded: l, total: t });
};
onEspeakProgress(({ file, loaded, total }) => report(file, loaded, total));

let tts: Promise<KokoroTTS> | undefined;
let device: 'webgpu' | 'wasm' = 'wasm';

const progress_callback = (p: { status: string; file?: string; loaded?: number; total?: number }) => {
  if (p.status === 'progress' && p.file) report(p.file, p.loaded ?? 0, p.total ?? 0);
};

async function onWasm(): Promise<KokoroTTS> {
  const k = await KokoroTTS.from_pretrained(model, { dtype: 'q8', device: 'wasm', progress_callback });
  device = 'wasm';
  post({ type: 'ready', device });
  return k;
}

function load(): Promise<KokoroTTS> {
  tts ??= (async () => {
    const gpu = (navigator as Navigator & { gpu?: { requestAdapter(): Promise<unknown> } }).gpu;
    const adapter = await gpu?.requestAdapter().catch(() => null);
    const [kokoro] = await Promise.all([
      (async () => {
        if (adapter) {
          try {
            const k = await KokoroTTS.from_pretrained(model, { dtype: 'fp32', device: 'webgpu', progress_callback });
            device = 'webgpu';
            post({ type: 'ready', device });
            return k;
          } catch (err) {
            console.warn('Kokoro on WebGPU failed, using WebAssembly', err);
          }
        }
        return onWasm();
      })(),
      loadEspeak(),
    ]);
    return kokoro;
  })();
  tts.catch(() => (tts = undefined));
  return tts;
}

// Kokoro's speech stays within ±1. A GPU that gets one of its operations
// wrong makes a loud buzz instead, far outside it: onnxruntime-web's
// ConvTranspose did on AMD's Vulkan driver (ortPatch.mts), and whatever does
// next is caught here, and the rest is read on the CPU.
const speech = (samples: Float32Array) => samples.every((x) => Math.abs(x) <= 2);

async function speak(text: string, voice: string, speed: number): Promise<Float32Array> {
  const kokoro = await load();
  const samples = await generate(kokoro, text, voice, speed);
  if (device === 'wasm' || speech(samples)) return samples;
  console.warn('Kokoro on WebGPU made noise, not speech: using WebAssembly');
  tts = onWasm();
  tts.catch(() => (tts = undefined));
  return generate(await tts, text, voice, speed);
}

async function generate(kokoro: KokoroTTS, text: string, voice: string, speed: number): Promise<Float32Array> {
  // kokoro-js reads English itself; Portuguese goes through Kokoro's own
  // eSpeak NG rules (g2p.ts), with the voice kokoro-js has no entry for.
  if (voice.startsWith('p')) {
    const ps = await espeakPhonemes(text, (t) => phones(t, 'pt-br'));
    const { input_ids } = kokoro.tokenizer(ps, { truncation: true });
    return (await kokoro.generate_from_ids(input_ids, { voice: voice as 'af_heart', speed })).audio;
  }
  return (await kokoro.generate(text, { voice: voice as 'af_heart', speed })).audio;
}

// Requests are read in turn; cancel drops what's waiting from before it.
let cancelled = 0;
let queue = Promise.resolve();
self.onmessage = (event: MessageEvent<WorkerRequest>) => {
  const req = event.data;
  if (req.type === 'cancel') {
    cancelled = req.generation;
    return;
  }
  queue = queue.then(async () => {
    if (req.generation <= cancelled) return;
    try {
      const samples = await speak(req.text, req.voice, req.speed);
      post({ type: 'audio', id: req.id, generation: req.generation, samples, sampleRate: 24000 }, [samples.buffer]);
    } catch (err) {
      post({ type: 'error', id: req.id, generation: req.generation, message: err instanceof Error ? err.message : String(err) });
    }
  });
};
