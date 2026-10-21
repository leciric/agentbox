// Holds Electron's 'prevent-app-suspension' blocker while a renderer says
// agents are busy: system idle sleep only, the display may still sleep. On a
// Mac, idle sleep pauses the VM and freezes agents mid-turn. What counts as
// busy is the renderer's call (lib/keepAwake.ts); this is only the switch (D19).
// Each window's hold is dropped when it navigates, crashes or closes, so the
// blocker can't outlive the renderer that asked for it.
import { ipcMain, powerSaveBlocker, type WebContents } from 'electron';

let id: number | undefined;
const holders = new Set<number>();

function apply(): void {
  if (holders.size > 0 && id === undefined) id = powerSaveBlocker.start('prevent-app-suspension');
  else if (holders.size === 0 && id !== undefined) {
    powerSaveBlocker.stop(id);
    id = undefined;
  }
}

export function release(contents: WebContents): void {
  holders.delete(contents.id);
  apply();
}

ipcMain.handle('keepawake:set', (event, on: boolean) => {
  const contents = event.sender;
  if (on !== true) return release(contents);
  if (!holders.has(contents.id)) {
    contents.once('render-process-gone', () => release(contents));
    contents.once('destroyed', () => release(contents));
    // A reload or a new page, not the app's own in-page navigation.
    contents.on('did-start-navigation', (e) => e.isMainFrame && !e.isSameDocument && release(contents));
  }
  holders.add(contents.id);
  apply();
});
