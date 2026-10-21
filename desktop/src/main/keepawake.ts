// Holds Electron's 'prevent-app-suspension' blocker while the renderer says
// agents are busy: system idle sleep only, the display may still sleep. On a
// Mac, idle sleep pauses the VM and freezes agents mid-turn. What counts as
// busy is the renderer's call (lib/keepAwake.ts); this is only the switch (D19).
import { ipcMain, powerSaveBlocker } from 'electron';

let id: number | undefined;

export function keepAwake(on: boolean): void {
  if (on && id === undefined) id = powerSaveBlocker.start('prevent-app-suspension');
  else if (!on && id !== undefined) {
    powerSaveBlocker.stop(id);
    id = undefined;
  }
}

ipcMain.handle('keepawake:set', (_event, on: boolean) => keepAwake(on === true));
