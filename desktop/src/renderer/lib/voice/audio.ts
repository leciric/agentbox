// Whisper hears 30 seconds at a time, at 16 kHz. A longer recording is cut
// into pieces of at most that, each cut made at the quietest moment of its
// last few seconds, so it falls between words rather than through one.

export const sampleRate = 16_000;
const window = 30 * sampleRate;
const lookBack = 6 * sampleRate;
const step = sampleRate / 10;

export function chunks(audio: Float32Array): Float32Array[] {
  const out: Float32Array[] = [];
  let start = 0;
  while (audio.length - start > window) {
    let cut = start + window;
    let quietest = Infinity;
    for (let at = start + window - lookBack; at + step <= start + window; at += step) {
      let energy = 0;
      for (let i = at; i < at + step; i++) energy += audio[i] * audio[i];
      if (energy < quietest) {
        quietest = energy;
        cut = at + step / 2;
      }
    }
    out.push(audio.subarray(start, cut));
    start = cut;
  }
  if (audio.length - start > 0) out.push(audio.subarray(start));
  return out;
}

// silent is whether a recording has nothing worth sending to Whisper: too
// short, or never louder than a room's hum. Whisper makes words up out of
// silence ("Thank you."), so a stray tap of the key shouldn't reach it.
export function silent(audio: Float32Array): boolean {
  if (audio.length < sampleRate / 4) return true;
  let peak = 0;
  for (const sample of audio) peak = Math.max(peak, Math.abs(sample));
  return peak < 0.01;
}
