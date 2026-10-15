// Opens a file sent in a chat with the system's default app. The file is the
// daemon's, which may be in a VM or behind a hub, so it is copied out first,
// into the app's temporary directory, under the name it was sent with.
import { createWriteStream } from 'node:fs';
import { mkdir, rename, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pipeline } from 'node:stream/promises';
import { shell } from 'electron';
import { requestOptions } from './connection';

// chatFilePath is the one daemon path this reads: a file of an agent's chat or a project's.
export const chatFilePath = /^\/v1\/(projects\/[^/]+|agents\/[^/]+\/[^/]+)\/chat\/files\/([0-9a-f]{16})$/;

// localName is the name a file is saved as here: its own, made safe for any
// file system the app runs on.
export function localName(name: string): string {
  const safe = Array.from(name, (c) => (c < ' ' || '\\/:*?"<>|'.includes(c) ? '_' : c))
    .join('')
    .replace(/^[.\s]+|[.\s]+$/g, '')
    .slice(0, 120);
  return safe || 'file';
}

export async function openChatFile(path: string, name: string): Promise<string> {
  const id = chatFilePath.exec(path)?.[2];
  if (!id) return 'not a chat file';
  const dir = join(tmpdir(), 'agentbox-chat-files', id);
  const file = join(dir, localName(name));
  // A file never changes once sent: one copied out already is the same.
  if (!(await stat(file).catch(() => null))?.isFile()) {
    await mkdir(dir, { recursive: true });
    // Kept under another name until it is whole, so a download cut short isn't taken for the file.
    const part = `${file}.part`;
    await download(path, part).catch(async (err: unknown) => {
      await rm(part, { force: true });
      throw err;
    });
    await rename(part, file);
  }
  return shell.openPath(file);
}

function download(path: string, to: string): Promise<void> {
  const { module, options } = requestOptions(path);
  return new Promise((resolve, reject) => {
    const req = module.request({ ...options, method: 'GET' }, (res) => {
      if (res.statusCode !== 200) {
        res.resume();
        reject(new Error(`the daemon answered ${res.statusCode}`));
        return;
      }
      pipeline(res, createWriteStream(to)).then(resolve, reject);
    });
    req.on('error', reject);
    req.end();
  });
}
