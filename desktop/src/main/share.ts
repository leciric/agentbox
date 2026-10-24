// Saves an agent's run as the page the daemon writes (GET .../share,
// internal/share): the user picks where, and the page is copied out of the
// daemon to there, which may be in a VM or behind a hub.
import { rename, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { app, dialog, shell, type BrowserWindow } from 'electron';
import { download, localName } from './chatfiles';
import { t } from '../shared/i18n/index.ts';

// sharePath is the one daemon path this reads: an agent's page.
export const sharePath = /^\/v1\/agents\/[^/]+\/[^/]+\/share$/;

// saveShare answers where the page was saved, or '' when the user didn't pick a place.
export async function saveShare(win: BrowserWindow | undefined, path: string, name: string): Promise<string> {
  if (!sharePath.test(path)) throw new Error('not an agent page');
  const options = {
    title: t('main.sharePage'),
    defaultPath: join(app.getPath('downloads'), localName(name)),
    filters: [{ name: 'HTML', extensions: ['html'] }],
  };
  const picked = win ? await dialog.showSaveDialog(win, options) : await dialog.showSaveDialog(options);
  if (picked.canceled || !picked.filePath) return '';
  const part = `${picked.filePath}.part`;
  await download(path, part).catch(async (err: unknown) => {
    await rm(part, { force: true });
    throw err;
  });
  await rename(part, picked.filePath);
  return picked.filePath;
}

// showShare shows a saved page in the file manager: a path of this machine, not the daemon's.
export function showShare(file: string): void {
  shell.showItemInFolder(file);
}
