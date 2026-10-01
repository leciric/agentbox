// Whether push-to-talk may put Chromium on Vulkan, kept apart from voice.ts so
// it has no Electron in it and node's test runner can load it.
//
// It's a file in the app's data directory rather than localStorage, beside the
// voice's other settings, because the switches it decides go onto Chromium's
// command line before the app is ready, before any page could be asked.
import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';

export type VoiceGPU = { vulkan: boolean };

// Off unless asked for: --enable-features=Vulkan moves Chromium's compositor
// onto Vulkan too, not only WebGPU, and on some Linux GPUs that leaves every
// <video> white (Media's recordings, and their thumbnails), where Chromium's
// default GL compositing plays them.
export const defaultVoiceGPU: VoiceGPU = { vulkan: false };

export function readVoiceGPU(file: string): VoiceGPU {
  try {
    const stored = JSON.parse(readFileSync(file, 'utf8')) as Partial<VoiceGPU>;
    return { vulkan: stored.vulkan === true };
  } catch {
    return defaultVoiceGPU;
  }
}

export function writeVoiceGPU(file: string, settings: VoiceGPU): void {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(`${file}.tmp`, JSON.stringify({ vulkan: settings.vulkan }, null, 2));
  renameSync(`${file}.tmp`, file);
}

// chromiumSwitches are the command-line switches for settings on platform.
// Chromium ships WebGPU on Linux behind these still: without them
// navigator.gpu finds no adapter, and with enable-unsafe-webgpu alone only
// SwiftShader, the CPU pretending to be a GPU, which Whisper turns down for
// WASM (whisper.worker.ts); Vulkan is what reaches the real one.
export function chromiumSwitches(settings: VoiceGPU, platform: NodeJS.Platform): [string, string?][] {
  if (platform !== 'linux') return [];
  const switches: [string, string?][] = [['enable-unsafe-webgpu']];
  if (settings.vulkan) switches.push(['enable-features', 'Vulkan']);
  return switches;
}
