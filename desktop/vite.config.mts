import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

export default defineConfig({
  root: fileURLToPath(new URL('./src/renderer', import.meta.url)),
  base: './',
  plugins: [react(), tailwindcss()],
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
