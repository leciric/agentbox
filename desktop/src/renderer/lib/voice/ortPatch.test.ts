// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { patchConvTranspose } from './ortPatch.mts';

test('the onnxruntime-web the voice bundles is patched in both places', () => {
  const bundle = readFileSync(new URL('../../../../node_modules/onnxruntime-web/dist/ort.bundle.min.mjs', import.meta.url), 'utf8');
  const { code, patched } = patchConvTranspose(bundle);
  assert.equal(patched, 2);
  assert.doesNotMatch(code, /let dy[RC] = \(\$\{\w+\}\(dy[RC]Corner\)/);
});

test('the division is done in integers, and a tap between inputs is still skipped', () => {
  const shader = 'let dyC = (${t}(dyCCorner) + ${t}(wC)) / ${t}(uniforms.strides.y);';
  assert.equal(
    patchConvTranspose(shader).code,
    'let dyC = select(${t}(0.5), ${t}((dyCCorner + i32(wC)) / i32(uniforms.strides.y)), (dyCCorner + i32(wC)) % i32(uniforms.strides.y) == 0);',
  );
});
