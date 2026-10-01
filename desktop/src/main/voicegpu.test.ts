// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { chromiumSwitches, defaultVoiceGPU, readVoiceGPU, writeVoiceGPU } from './voicegpu.ts';

// Vulkan moves Chromium's compositor too, which left recordings white in
// Media on the user's Linux GPU: it's only on when asked for.
test('Vulkan is off unless it was turned on', () => {
  assert.deepEqual(defaultVoiceGPU, { vulkan: false });
  assert.deepEqual(chromiumSwitches(defaultVoiceGPU, 'linux'), [['enable-unsafe-webgpu']]);
  assert.deepEqual(chromiumSwitches({ vulkan: true }, 'linux'), [['enable-unsafe-webgpu'], ['enable-features', 'Vulkan']]);
});

test('no switches outside Linux', () => {
  assert.deepEqual(chromiumSwitches({ vulkan: true }, 'darwin'), []);
  assert.deepEqual(chromiumSwitches({ vulkan: true }, 'win32'), []);
});

test('the choice is kept in a file, and a missing or broken one is the default', () => {
  const dir = mkdtempSync(join(tmpdir(), 'voicegpu-'));
  const file = join(dir, 'sub', 'voice-gpu.json');
  assert.deepEqual(readVoiceGPU(file), defaultVoiceGPU);
  writeVoiceGPU(file, { vulkan: true });
  assert.deepEqual(readVoiceGPU(file), { vulkan: true });
  writeVoiceGPU(file, { vulkan: false });
  assert.deepEqual(readVoiceGPU(file), { vulkan: false });
  writeFileSync(file, '{"vulkan": "yes"');
  assert.deepEqual(readVoiceGPU(file), defaultVoiceGPU);
  writeFileSync(file, '{"vulkan": "yes"}');
  assert.deepEqual(readVoiceGPU(file), defaultVoiceGPU);
});
