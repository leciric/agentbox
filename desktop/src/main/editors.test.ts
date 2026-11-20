// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { findEditors, pick, type Host } from './editors.ts';

const host = (platform: NodeJS.Platform, files: string[], path = '/usr/bin:/bin'): Host => ({
  platform,
  home: platform === 'win32' ? 'C:\\Users\\ana' : platform === 'darwin' ? '/Users/ana' : '/home/ana',
  path,
  pathext: platform === 'win32' ? '.COM;.EXE;.BAT;.CMD' : undefined,
  exists: (p) => files.includes(p),
});

test('Linux: editors on PATH, in kinds order, each once', () => {
  const found = findEditors(host('linux', ['/usr/bin/cursor', '/usr/bin/code', '/usr/local/bin/code']));
  assert.deepEqual(
    found.map((e) => [e.id, e.command, e.args]),
    [
      ['vscode', '/usr/bin/code', []],
      ['cursor', '/usr/bin/cursor', []],
    ],
  );
});

test("Linux: an editor outside a desktop session's PATH, or a Flatpak, is found", () => {
  const found = findEditors(
    host('linux', ['/home/ana/.local/bin/zed', '/var/lib/flatpak/exports/bin/com.visualstudio.code', '/home/ana/.local/share/JetBrains/Toolbox/scripts/goland'], '/usr/bin'),
  );
  assert.deepEqual(
    found.map((e) => [e.id, e.command]),
    [
      ['vscode', '/var/lib/flatpak/exports/bin/com.visualstudio.code'],
      ['zed', '/home/ana/.local/bin/zed'],
      ['goland', '/home/ana/.local/share/JetBrains/Toolbox/scripts/goland'],
    ],
  );
});

test('Mac: an app in Applications opens with open -a, whatever PATH has', () => {
  const found = findEditors(host('darwin', ['/Applications/Visual Studio Code.app', '/Users/ana/Applications/Zed.app', '/opt/homebrew/bin/subl']));
  assert.deepEqual(
    found.map((e) => [e.id, e.command, e.args]),
    [
      ['vscode', 'open', ['-a', '/Applications/Visual Studio Code.app']],
      ['zed', 'open', ['-a', '/Users/ana/Applications/Zed.app']],
      ['sublime', '/opt/homebrew/bin/subl', []],
    ],
  );
});

test("Windows: a command is found with PATHEXT's extensions", () => {
  const dir = 'C:\\Users\\ana\\AppData\\Local\\Programs\\Microsoft VS Code\\bin';
  const found = findEditors(host('win32', [`${dir}\\code.cmd`], `C:\\Windows;${dir}`));
  assert.deepEqual(
    found.map((e) => [e.id, e.command]),
    [['vscode', `${dir}\\code.cmd`]],
  );
});

test('nothing installed finds nothing', () => {
  assert.deepEqual(findEditors(host('linux', [])), []);
  assert.equal(pick([], 'vscode'), undefined);
});

test('the editor picked is opened while it is installed, else the first found', () => {
  const found = findEditors(host('linux', ['/usr/bin/code', '/usr/bin/zed']));
  assert.equal(pick(found, 'zed')?.id, 'zed');
  assert.equal(pick(found, '')?.id, 'vscode');
  assert.equal(pick(found, 'cursor')?.id, 'vscode');
});
