// The speech-to-text models push-to-talk can run, all Whisper as ONNX from
// Hugging Face's onnx-community, run in the renderer by transformers.js. None
// is bundled with the app: each downloads from Hugging Face the first time
// it's used, into the renderer's Cache Storage, and runs from there offline.
//
// Pure data and choices, so they're tested without a browser.

export type VoiceDevice = 'webgpu' | 'wasm';

// The files each model loads, as transformers.js names its dtypes: on WebGPU
// the encoder in fp16, which is what quality hangs on, and the decoder in 4
// bits; on the CPU both 8-bit, which WASM runs fastest.
type Dtypes = Record<'encoder_model' | 'decoder_model_merged', string>;

export type VoiceModel = {
  id: string;
  label: string;
  repo: string;
  // What a download of it costs, in MB, per device (the ONNX files, give or
  // take the few MB of config and tokenizer beside them).
  size: Record<VoiceDevice, number>;
  dtype: Record<VoiceDevice, Dtypes>;
  note: string;
};

const gpu: Dtypes = { encoder_model: 'fp16', decoder_model_merged: 'q4' };
const cpu: Dtypes = { encoder_model: 'q8', decoder_model_merged: 'q8' };

export const voiceModels: VoiceModel[] = [
  {
    id: 'whisper-large-v3-turbo',
    label: 'Whisper large-v3 turbo',
    repo: 'onnx-community/whisper-large-v3-turbo',
    size: { webgpu: 1608, wasm: 1085 },
    dtype: { webgpu: gpu, wasm: cpu },
    note: 'The most accurate, in Portuguese above all. Made for the GPU: on the CPU it takes about a minute a sentence.',
  },
  {
    id: 'whisper-small',
    label: 'Whisper small',
    repo: 'onnx-community/whisper-small',
    size: { webgpu: 410, wasm: 249 },
    dtype: { webgpu: gpu, wasm: cpu },
    note: 'Good in English, fair in Portuguese. Quick on the GPU; some 20 seconds a sentence on the CPU.',
  },
  {
    id: 'whisper-base',
    label: 'Whisper base',
    repo: 'onnx-community/whisper-base',
    size: { webgpu: 165, wasm: 77 },
    dtype: { webgpu: gpu, wasm: cpu },
    note: 'The smallest and quickest, and the least accurate, in Portuguese above all. What Automatic uses on the CPU.',
  },
];

// auto is large-v3 turbo on WebGPU, and base without it: on the CPU (one
// thread, since the page can't have SharedArrayBuffer) small takes some 20
// seconds a sentence and turbo a minute, where base takes about five.
export const autoModel = 'auto';

export function pickModel(setting: string, device: VoiceDevice): VoiceModel {
  const chosen = voiceModels.find((m) => m.id === setting);
  if (chosen) return chosen;
  return voiceModels.find((m) => m.id === (device === 'webgpu' ? 'whisper-large-v3-turbo' : 'whisper-base'))!;
}

export type VoiceLanguage = 'auto' | 'pt' | 'en';

// Whisper hears 99 languages, but auto only chooses between the two people
// here speak: Portuguese or English, whichever the model finds likelier.
export const autoLanguages = ['pt', 'en'] as const;

export function likeliest(scores: Record<string, number>): string {
  let best: string = autoLanguages[0];
  for (const language of autoLanguages) if ((scores[language] ?? -Infinity) > (scores[best] ?? -Infinity)) best = language;
  return best;
}

// formatMB says a download's size the way a person reads it.
export function formatMB(mb: number): string {
  return mb >= 1000 ? `${(mb / 1000).toFixed(1)} GB` : `${Math.max(1, Math.round(mb))} MB`;
}
