// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { nextMode, parseComparePrefs, pixelDiff } from './compare.ts';

test('the remembered compare mode reads back, and anything else is side by side', () => {
  assert.deepEqual(parseComparePrefs('{"mode":"flip","highlight":true}'), { mode: 'flip', highlight: true });
  assert.deepEqual(parseComparePrefs('{"mode":"wipe"}'), { mode: 'side', highlight: false });
  assert.deepEqual(parseComparePrefs(null), { mode: 'side', highlight: false });
  assert.deepEqual(parseComparePrefs('not json'), { mode: 'side', highlight: false });
});

test('V goes round the modes', () => {
  assert.equal(nextMode('side'), 'slider');
  assert.equal(nextMode('slider'), 'flip');
  assert.equal(nextMode('flip'), 'side');
});

test('pixelDiff marks the pixels that changed, tints their cell, and leaves noise alone', () => {
  const w = 20;
  const h = 20;
  const a = new Uint8ClampedArray(w * h * 4).fill(200);
  const b = a.slice();
  // Noise under the threshold, and one pixel that really changed.
  b[0] = 210;
  const p = (3 * w + 17) * 4;
  b[p] = 0;
  const d = pixelDiff(a, b, w, h);
  assert.equal(d.changed, 1);
  assert.equal(d.total, 400);
  assert.equal(d.overlay[p + 3], 230);
  // Its 16px cell (x 16–19, y 0–15) is tinted; the cell left of it isn't.
  assert.equal(d.overlay[16 * 4 + 3], 60);
  assert.equal(d.overlay[3], 0);
  assert.equal(pixelDiff(a, a.slice(), w, h).changed, 0);
});
