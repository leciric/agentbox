/// <reference lib="webworker" />
// Whisper, run off the page's thread: push-to-talk hands this worker 16 kHz
// mono audio and gets the words back. It runs on WebGPU when the machine has
// it, and on the CPU through WASM when it hasn't, and says which.
//
// The ONNX runtime's WASM is bundled with the app (Vite copies it into the
// build), so nothing but the model's weights comes from the network, and those
// only once: transformers.js keeps them in Cache Storage.
import { AutoProcessor, AutoTokenizer, Tensor, WhisperForConditionalGeneration, env, type PreTrainedModel, type PreTrainedTokenizer, type Processor } from '@huggingface/transformers';
import gpuWasm from 'onnxruntime-web/ort-wasm-simd-threaded.asyncify.wasm?url';
import gpuMjs from 'onnxruntime-web/ort-wasm-simd-threaded.asyncify.mjs?url';
import cpuWasm from 'onnxruntime-web/ort-wasm-simd-threaded.wasm?url';
import cpuMjs from 'onnxruntime-web/ort-wasm-simd-threaded.mjs?url';
import { chunks, sampleRate } from './audio';
import { autoLanguages, likeliest, pickModel, type VoiceDevice, type VoiceLanguage } from './models';

export type WorkerRequest =
  | { type: 'probe' }
  | { type: 'load'; model: string }
  | { type: 'transcribe'; id: number; model: string; audio: Float32Array; language: VoiceLanguage };

export type WorkerReply =
  | { type: 'device'; device: VoiceDevice; reason?: DeviceReason; adapter?: string }
  | { type: 'progress'; model: string; file: string; loaded: number; total: number }
  | { type: 'ready'; model: string; device: VoiceDevice; ms: number }
  | { type: 'result'; id: number; text: string; language: string; ms: { detect: number; transcribe: number } }
  | { type: 'error'; id?: number; message: string };

const scope = self as unknown as DedicatedWorkerGlobalScope;
const post = (reply: WorkerReply) => scope.postMessage(reply);

const onnx = env.backends.onnx as { wasm?: { wasmPaths?: unknown; numThreads?: number } };
if (onnx.wasm) {
  // Threads need SharedArrayBuffer, which a page loaded from file:// can't
  // have (it isn't cross-origin isolated): one thread, on the CPU.
  onnx.wasm.numThreads = 1;
}
env.allowLocalModels = false;
env.useWasmCache = false;

// device is what this machine can run Whisper on, found once: WebGPU needs an
// adapter, and our models want its shader-f16 feature for their fp16 encoder.
// DeviceReason says why it isn't WebGPU, as a code the page puts into words in
// the app's language (whisper.ts's deviceReason): a worker can't know it.
export type DeviceReason = { code: 'unavailable' | 'noGpu' | 'software' | 'failed'; error?: string };

let device: Promise<{ device: VoiceDevice; f16: boolean; reason?: DeviceReason; adapter?: string }> | undefined;
function findDevice() {
  device ??= (async () => {
    const gpu = (navigator as Navigator & { gpu?: { requestAdapter(): Promise<{ features: Set<string>; info?: { vendor: string; architecture: string; description: string; isFallbackAdapter?: boolean } } | null> } }).gpu;
    if (!gpu) return { device: 'wasm' as const, f16: false, reason: { code: 'unavailable' } };
    try {
      const adapter = await gpu.requestAdapter();
      if (!adapter) return { device: 'wasm' as const, f16: false, reason: { code: 'noGpu' } };
      const info = adapter.info;
      // SwiftShader is WebGPU emulated on the CPU, slower than WASM: what
      // Chromium hands out when it can't reach the real GPU.
      if (info?.isFallbackAdapter || /swiftshader/i.test(`${info?.vendor} ${info?.architecture} ${info?.description}`))
        return { device: 'wasm' as const, f16: false, reason: { code: 'software' } };
      const name = info && ([info.vendor, info.architecture].filter(Boolean).join(' ') || info.description);
      return { device: 'webgpu' as const, f16: adapter.features.has('shader-f16'), adapter: name || undefined };
    } catch (err) {
      return { device: 'wasm' as const, f16: false, reason: { code: 'failed', error: String(err) } };
    }
  })();
  return device;
}

type Loaded = { id: string; model: PreTrainedModel; processor: Processor; tokenizer: PreTrainedTokenizer; device: VoiceDevice };
let loaded: Promise<Loaded> | undefined;
let loadedId = '';

async function load(setting: string): Promise<Loaded> {
  const found = await findDevice();
  const choice = pickModel(setting, found.device);
  // WebGPU needs the runtime built with asyncify; the CPU alone runs faster
  // on the plain one.
  const [wasm, mjs] = found.device === 'webgpu' ? [gpuWasm, gpuMjs] : [cpuWasm, cpuMjs];
  if (onnx.wasm) onnx.wasm.wasmPaths = { wasm: new URL(wasm, scope.location.href).href, mjs: new URL(mjs, scope.location.href).href };
  if (loaded && loadedId === choice.id) return loaded;
  const previous = loaded;
  loadedId = choice.id;
  loaded = (async () => {
    await previous?.then((p) => p.model.dispose()).catch(() => {});
    const started = performance.now();
    const progress_callback = (event: { status: string; file?: string; loaded?: number; total?: number }) => {
      if (event.status === 'progress' && event.file) post({ type: 'progress', model: choice.id, file: event.file, loaded: event.loaded ?? 0, total: event.total ?? 0 });
    };
    let dtype = choice.dtype[found.device];
    // Without shader-f16 WebGPU can't run fp16: the encoder goes full precision.
    if (found.device === 'webgpu' && !found.f16) dtype = { ...dtype, encoder_model: 'fp32' };
    const [model, processor, tokenizer] = await Promise.all([
      WhisperForConditionalGeneration.from_pretrained(choice.repo, { device: found.device, dtype: dtype as never, progress_callback }),
      AutoProcessor.from_pretrained(choice.repo, { progress_callback }),
      AutoTokenizer.from_pretrained(choice.repo, { progress_callback }),
    ]);
    // generate() only passes on the inputs a model lists, and Whisper's list
    // leaves out encoder_outputs, so an encoding done for the language would
    // be thrown away and done again: listed, it's used.
    const params = (model as unknown as { forward_params: string[] }).forward_params;
    if (!params.includes('encoder_outputs')) params.push('encoder_outputs');
    // A first run compiles the WebGPU shaders, which would otherwise make the
    // first thing you say the slowest.
    await transcribe({ id: choice.id, model, processor, tokenizer, device: found.device }, new Float32Array(16_000), 'en');
    post({ type: 'ready', model: choice.id, device: found.device, ms: Math.round(performance.now() - started) });
    return { id: choice.id, model, processor, tokenizer, device: found.device };
  })();
  loaded.catch(() => {
    if (loadedId === choice.id) {
      loaded = undefined;
      loadedId = '';
    }
  });
  return loaded;
}

type Generate = (args: Record<string, unknown>) => Promise<Tensor>;

// transcribe runs each 30-second piece in turn; the language, when it's
// auto, is found from the first.
async function transcribe(w: Loaded, audio: Float32Array, language: VoiceLanguage) {
  const texts: string[] = [];
  const ms = { detect: 0, transcribe: 0 };
  let spoken: VoiceLanguage | string = language;
  for (const piece of chunks(audio)) {
    const got = await transcribePiece(w, piece, spoken as VoiceLanguage);
    spoken = got.language;
    texts.push(got.text);
    ms.detect += got.ms.detect;
    ms.transcribe += got.ms.transcribe;
  }
  return { text: texts.filter(Boolean).join(' '), language: spoken, ms };
}

async function transcribePiece(w: Loaded, audio: Float32Array, language: VoiceLanguage) {
  const inputs = (await w.processor(audio)) as { input_features: Tensor };
  let spoken: string = language;
  const started = performance.now();
  let encoder_outputs: Tensor | undefined;
  if (language === 'auto') {
    // transformers.js doesn't detect Whisper's language (it assumes English),
    // so it's done here the way Whisper itself does: encode once, run the
    // decoder one step from start-of-transcript, and take whichever of
    // Portuguese and English it scores higher. The encoding is kept for the
    // transcription, so only the one decoder step is extra.
    const session = (w.model as unknown as { sessions: Record<string, { run(feeds: Record<string, unknown>): Promise<Record<string, unknown>> }> }).sessions.model;
    const encoded = await session.run({ input_features: (inputs.input_features as unknown as { ort_tensor: unknown }).ort_tensor });
    encoder_outputs = new Tensor(encoded.last_hidden_state as never);
    const config = w.model.generation_config as unknown as { decoder_start_token_id: number; lang_to_id: Record<string, number> };
    const out = (await w.model({
      input_features: inputs.input_features,
      encoder_outputs,
      decoder_input_ids: new Tensor('int64', [BigInt(config.decoder_start_token_id)], [1, 1]),
    })) as { logits: Tensor };
    const logits = out.logits.data as Float32Array;
    const vocab = out.logits.dims.at(-1)!;
    const scores: Record<string, number> = {};
    for (const code of autoLanguages) scores[code] = logits[logits.length - vocab + config.lang_to_id[`<|${code}|>`]];
    spoken = likeliest(scores);
  }
  const detected = performance.now();
  const ids = await (w.model.generate as unknown as Generate)({
    inputs: inputs.input_features,
    ...(encoder_outputs ? { encoder_outputs } : {}),
    language: spoken,
    task: 'transcribe',
    // Whisper sometimes loops on a phrase until it runs out of tokens; nobody
    // says more than about eight tokens a second, so it stops there.
    max_new_tokens: Math.min(440, Math.ceil((audio.length / sampleRate) * 8) + 16),
  });
  const text = w.tokenizer.batch_decode(ids as never, { skip_special_tokens: true })[0]?.trim() ?? '';
  return { text, language: spoken, ms: { detect: Math.round(detected - started), transcribe: Math.round(performance.now() - detected) } };
}

scope.onmessage = async ({ data }: MessageEvent<WorkerRequest>) => {
  try {
    if (data.type === 'probe') {
      const found = await findDevice();
      post({ type: 'device', device: found.device, reason: found.reason, adapter: found.adapter });
    } else if (data.type === 'load') {
      await load(data.model);
    } else if (data.type === 'transcribe') {
      const w = await load(data.model);
      post({ type: 'result', id: data.id, ...(await transcribe(w, data.audio, data.language)) });
    }
  } catch (err) {
    post({ type: 'error', id: data.type === 'transcribe' ? data.id : undefined, message: err instanceof Error ? err.message : String(err) });
  }
};
