// The page scripts/voice-bench.mjs drives: push-to-talk's own client and
// worker (lib/voice), with recorded audio handed in where the microphone's
// would be.
import { chunks } from '../lib/voice/audio';
import type { VoiceLanguage } from '../lib/voice/models';
import { preloadWhisper, transcribe, whisperState } from '../lib/voice/whisper';

declare global {
  interface Window {
    bench: typeof bench;
  }
}

const bench = {
  // load resolves once model is ready, with how long that took and on what.
  async load(model: string) {
    const started = performance.now();
    preloadWhisper(model);
    for (;;) {
      const s = whisperState();
      if (s.error) throw new Error(s.error);
      if (s.ready === model || (s.ready && model === 'auto')) return { ms: Math.round(performance.now() - started), device: s.device, reason: s.reason, adapter: s.adapter, model: s.ready };
      await new Promise((r) => setTimeout(r, 100));
    }
  },
  async run(base64: string, language: VoiceLanguage, model: string) {
    const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
    const audio = new Float32Array(bytes.buffer);
    const pieces = chunks(audio).length;
    const seconds = audio.length / 16_000;
    const started = performance.now();
    const t = await transcribe(audio, language, model);
    return { ...t, pieces, total: Math.round(performance.now() - started), seconds };
  },
};
window.bench = bench;
