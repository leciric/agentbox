// Push-to-talk runs Whisper in the renderer (lib/voice), so all the main
// process does for it is let the page have the microphone and the GPU.
import { app, ipcMain, session } from 'electron';
import { join } from 'node:path';
import { chromiumSwitches, readVoiceGPU, writeVoiceGPU, type VoiceGPU } from './voicegpu';

const voiceGPUFile = () => join(app.getPath('userData'), 'voice-gpu.json');

// enableWebGPU must run before the app is ready: it puts the switches the
// saved choice asks for on Chromium's command line (voicegpu.ts). The choice
// is read once, here, so changing it in Settings takes effect on the next
// start; voice:gpu answers with both what's saved and what this run has.
export function enableWebGPU(): void {
  const running = readVoiceGPU(voiceGPUFile());
  for (const [name, value] of chromiumSwitches(running, process.platform)) app.commandLine.appendSwitch(name, value);
  ipcMain.handle('voice:gpu', () => ({ saved: readVoiceGPU(voiceGPUFile()), running, platform: process.platform }));
  ipcMain.handle('voice:set-gpu', (_event, settings: VoiceGPU) => writeVoiceGPU(voiceGPUFile(), { vulkan: settings.vulkan === true }));
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
