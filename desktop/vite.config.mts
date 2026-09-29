import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import { patchConvTranspose } from './src/renderer/lib/voice/ortPatch.mts';

// kokoroPatch fixes onnxruntime-web's WebGPU ConvTranspose, which reads the
// voice as a buzz on AMD GPUs (src/renderer/lib/voice/ortPatch.mts). Each of
// its builds with WebGPU in it must be patched in both places, or the build
// fails: a new onnxruntime-web that changed them needs looking at again.
function kokoroPatch(): Plugin {
  return {
    name: 'agentbox-ort-convtranspose',
    transform(code, id) {
      if (!/[\\/]onnxruntime-web[\\/]dist[\\/]/.test(id) || !code.includes('dyRCorner')) return null;
      const { code: patched, patched: n } = patchConvTranspose(code);
      if (n !== 2) this.error(`onnxruntime-web's ConvTranspose changed (${n} of 2 places patched): check lib/voice/ortPatch.mts`);
      return { code: patched, map: null };
    },
  };
}

export default defineConfig({
  root: fileURLToPath(new URL('./src/renderer', import.meta.url)),
  base: './',
  plugins: [react(), tailwindcss(), kokoroPatch()],
  // kokoro-js imports eSpeak NG, which is GPL-3.0, as the package "phonemizer":
  // this one fetches it when the voice is first used instead of bundling it
  // (src/renderer/lib/voice/espeak.ts).
  resolve: { alias: { phonemizer: fileURLToPath(new URL('./src/renderer/lib/voice/espeak.ts', import.meta.url)) } },
  worker: { format: 'es', plugins: () => [kokoroPatch()] },
  build: {
    outDir: fileURLToPath(new URL('./out/renderer', import.meta.url)),
    emptyOutDir: true,
    target: 'esnext', // noVNC uses top-level await
    chunkSizeWarningLimit: 2000,
    // index.html is the desktop app's; web.html is the same app in a browser, served by a hub.
    rollupOptions: {
      input: {
        index: fileURLToPath(new URL('./src/renderer/index.html', import.meta.url)),
        web: fileURLToPath(new URL('./src/renderer/web.html', import.meta.url)),
      },
    },
  },
});
