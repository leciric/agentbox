// Run with `npm test`. The worker itself needs a browser; this is the part of
// push-to-talk that doesn't: which model runs where, the language auto picks,
// and where long recordings are cut. scripts/voice-bench.mjs runs the rest.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chunks, sampleRate, silent } from './audio.ts';
import { autoModel, formatMB, likeliest, pickModel } from './models.ts';

test('auto is large-v3 turbo on the GPU and base on the CPU', () => {
  assert.equal(pickModel(autoModel, 'webgpu').id, 'whisper-large-v3-turbo');
  assert.equal(pickModel(autoModel, 'wasm').id, 'whisper-base');
});

test('a chosen model runs wherever it is', () => {
  assert.equal(pickModel('whisper-base', 'webgpu').id, 'whisper-base');
  assert.equal(pickModel('whisper-large-v3-turbo', 'wasm').id, 'whisper-large-v3-turbo');
});

test('a model an older version offered falls back to auto', () => {
  assert.equal(pickModel('whisper-tiny', 'wasm').id, 'whisper-base');
});

test('auto language takes the likelier of Portuguese and English', () => {
  assert.equal(likeliest({ pt: 3.2, en: 1.1 }), 'pt');
  assert.equal(likeliest({ pt: -2, en: 0.5 }), 'en');
  assert.equal(likeliest({}), 'pt');
});

test('sizes read as a person would', () => {
  assert.equal(formatMB(77), '77 MB');
  assert.equal(formatMB(1608), '1.6 GB');
  assert.equal(formatMB(0.2), '1 MB');
});

test('up to 30 seconds is one piece', () => {
  assert.equal(chunks(new Float32Array(30 * sampleRate)).length, 1);
  assert.equal(chunks(new Float32Array(3 * sampleRate)).length, 1);
});

test('a long recording is cut at its quietest moment, and loses nothing', () => {
  const audio = new Float32Array(70 * sampleRate).map((_, i) => Math.sin(i / 7) * 0.5);
  // A pause at 27 s, inside the first cut's window (24–30 s).
  audio.fill(0, 27 * sampleRate, 27 * sampleRate + sampleRate / 5);
  const pieces = chunks(audio);
  assert.equal(pieces.length, 3);
  assert.ok(pieces.every((p) => p.length <= 30 * sampleRate));
  assert.equal(
    pieces.reduce((n, p) => n + p.length, 0),
    audio.length,
  );
  const cut = pieces[0].length / sampleRate;
  assert.ok(cut >= 27 && cut <= 27.2, `cut at ${cut} s, not in the pause`);
});

test('a tap of the key, or silence, is nothing to transcribe', () => {
  assert.ok(silent(new Float32Array(sampleRate / 10).fill(0.5)));
  assert.ok(silent(new Float32Array(2 * sampleRate).fill(0.001)));
  assert.ok(!silent(new Float32Array(2 * sampleRate).map((_, i) => Math.sin(i / 5) * 0.2)));
});
