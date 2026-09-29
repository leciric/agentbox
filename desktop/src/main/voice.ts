// Push-to-talk runs Whisper in the renderer (lib/voice), so all the main
// process does for it is let the page have the microphone and the GPU.
import { app, session } from 'electron';

// enableWebGPU must run before the app is ready. Chromium ships WebGPU on
// Linux behind these switches still: without them navigator.gpu finds no
// adapter, and with enable-unsafe-webgpu alone only SwiftShader, the CPU
// pretending to be a GPU; Vulkan is what reaches the real one.
export function enableWebGPU(): void {
  if (process.platform !== 'linux') return;
  app.commandLine.appendSwitch('enable-unsafe-webgpu');
  app.commandLine.appendSwitch('enable-features', 'Vulkan');
}

// allowMicrophone lets the app's own page record audio (getUserMedia), and
// nothing more of media: no camera, no screen. Every other permission is
// granted as Electron grants it with no handler at all.
export function allowMicrophone(): void {
  session.defaultSession.setPermissionRequestHandler((_contents, permission, callback, details) => {
    if (permission !== 'media') return callback(true);
    const types = 'mediaTypes' in details ? (details.mediaTypes ?? []) : [];
    callback(types.length > 0 && types.every((type) => type === 'audio'));
  });
}
