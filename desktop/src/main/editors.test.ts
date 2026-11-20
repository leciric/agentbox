// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { findEditors, pick, remoteTarget, type Host } from './editors.ts';

const host = (platform: NodeJS.Platform, files: string[], path = '/usr/bin:/bin'): Host => ({
  platform,
  home: platform === 'darwin' ? '/Users/ana' : '/home/ana',
  path,
  exists: (p) => files.includes(p),
});

test('Linux: editors on PATH, in kinds order, each once', () => {
  const found = findEditors(host('linux', ['/usr/bin/cursor', '/usr/bin/code', '/usr/local/bin/code']));
  assert.deepEqual(
    found.map((e) => [e.id, e.command]),
    [
      ['vscode', '/usr/bin/code'],
      ['cursor', '/usr/bin/cursor'],
    ],
  );
});

test("Linux: an editor outside a desktop session's PATH, or a Flatpak, is found", () => {
  const found = findEditors(host('linux', ['/home/ana/.local/bin/zed', '/var/lib/flatpak/exports/bin/com.visualstudio.code'], '/usr/bin'));
  assert.deepEqual(
    found.map((e) => [e.id, e.command]),
    [
      ['vscode', '/var/lib/flatpak/exports/bin/com.visualstudio.code'],
      ['zed', '/home/ana/.local/bin/zed'],
    ],
  );
});

test('editors with no cheap way in over SSH are not offered', () => {
  assert.deepEqual(findEditors(host('linux', ['/usr/bin/subl', '/usr/bin/idea', '/usr/bin/goland'])), []);
});

test("Mac: an app in Applications is found whatever PATH has, and Zed's command is the one inside it", () => {
  const found = findEditors(host('darwin', ['/Applications/Visual Studio Code.app', '/Users/ana/Applications/Zed.app']));
  assert.deepEqual(
    found.map((e) => [e.id, e.command]),
    [
      ['vscode', '/Applications/Visual Studio Code.app'],
      ['zed', '/Users/ana/Applications/Zed.app/Contents/MacOS/cli'],
    ],
  );
});

test('Windows: none yet', () => {
  assert.deepEqual(findEditors({ platform: 'win32', home: 'C:\\Users\\ana', path: 'C:\\bin', exists: () => true }), []);
});

test('the editor picked is opened while it is installed, else the first found', () => {
  const found = findEditors(host('linux', ['/usr/bin/code', '/usr/bin/zed']));
  assert.equal(pick(found, 'zed')?.id, 'zed');
  assert.equal(pick(found, '')?.id, 'vscode');
  assert.equal(pick(found, 'cursor')?.id, 'vscode');
  assert.equal(pick([], 'vscode'), undefined);
});

test("the worktree opens inside the agent's machine: a Remote-SSH URL, or Zed's ssh://", () => {
  const found = findEditors(host('linux', ['/usr/bin/code', '/usr/bin/cursor', '/usr/bin/windsurf', '/usr/bin/zed', '/usr/bin/codium', '/usr/bin/code-insiders']));
  const setup = { user: 'ana', hostSuffix: '.agentbox' };
  const at = (id: string) => remoteTarget(found.find((e) => e.id === id)!, setup, 'ab-pawly-agent-01', '/home/ana/.local/share/agentbox/worktrees/pawly/agent-01');
  assert.deepEqual(at('vscode'), { url: 'vscode://vscode-remote/ssh-remote+ab-pawly-agent-01.agentbox/home/ana/.local/share/agentbox/worktrees/pawly/agent-01' });
  assert.deepEqual(at('cursor'), { url: 'cursor://vscode-remote/ssh-remote+ab-pawly-agent-01.agentbox/home/ana/.local/share/agentbox/worktrees/pawly/agent-01' });
  const url = (id: string) => {
    const target = at(id);
    return 'url' in target ? target.url : '';
  };
  assert.ok(url('windsurf').startsWith('windsurf://vscode-remote/ssh-remote+ab-pawly-agent-01.agentbox/'));
  assert.ok(url('vscodium').startsWith('vscodium://vscode-remote/ssh-remote+'));
  assert.ok(url('vscode-insiders').startsWith('vscode-insiders://vscode-remote/ssh-remote+'));
  assert.deepEqual(at('zed'), { command: '/usr/bin/zed', args: ['ssh://ana@ab-pawly-agent-01.agentbox/home/ana/.local/share/agentbox/worktrees/pawly/agent-01'] });
  // A path with a space stays one path.
  assert.deepEqual(remoteTarget(found[0], setup, 'ab-p-a', '/home/ana/my work/a'), { url: 'vscode://vscode-remote/ssh-remote+ab-p-a.agentbox/home/ana/my%20work/a' });
});
