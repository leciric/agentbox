// The code editors on this computer that can open an agent's worktree inside
// its machine, over SSH: the agent's "Open in …" and Settings → Code editor.
// The worktree isn't on this computer: it's on the VM's disk, in the agent's
// machine. `agentbox ssh-config` lets ssh reach that machine as
// <instance>.agentbox (internal/sshconfig), so an editor that works over SSH
// opens it there: VS Code and the editors made from it through their
// Remote-SSH URL (vscode://vscode-remote/ssh-remote+<host><path>), and Zed
// with ssh://<user>@<host><path>. Editors with no cheap way in over SSH
// (Sublime Text; JetBrains' IDEs, which need Gateway) aren't offered.
//
// An editor is found by its command on PATH, in the folders installers put
// one that a desktop session's PATH may lack, as a Flatpak (exported by its
// app ID), or on a Mac as an app in Applications, which a Mac app started
// from the Dock needs: its PATH is /usr/bin:/bin:/usr/sbin:/sbin. Only these
// editors can be opened, at a URL or a command made here: the renderer names
// an editor and an agent, never a command to run. Not on Windows yet, whose
// ssh config and ProxyCommand would be Windows' (internal/hostwsl). The
// native app finds the same ones (native/crates/app/src/editors.rs).
import { execFile, spawn } from 'node:child_process';
import { accessSync, constants, statSync } from 'node:fs';
import { homedir } from 'node:os';
import { posix } from 'node:path';

export interface Editor {
  id: string;
  name: string;
}

interface Kind extends Editor {
  // How it opens a folder over SSH: its URL scheme's vscode-remote URL, or
  // Zed's ssh:// argument.
  remote: { scheme: string } | 'zed';
  commands: string[]; // on PATH
  flatpaks?: string[]; // Linux: Flatpak app IDs
  apps?: string[]; // Mac: app bundles, without .app
}

// kinds, in the order the first one found is taken when none is picked.
export const kinds: Kind[] = [
  { id: 'vscode', name: 'VS Code', remote: { scheme: 'vscode' }, commands: ['code'], flatpaks: ['com.visualstudio.code'], apps: ['Visual Studio Code'] },
  { id: 'cursor', name: 'Cursor', remote: { scheme: 'cursor' }, commands: ['cursor'], apps: ['Cursor'] },
  { id: 'windsurf', name: 'Windsurf', remote: { scheme: 'windsurf' }, commands: ['windsurf'], apps: ['Windsurf'] },
  { id: 'zed', name: 'Zed', remote: 'zed', commands: ['zed', 'zeditor', 'zedit'], flatpaks: ['dev.zed.Zed'], apps: ['Zed'] },
  { id: 'vscodium', name: 'VSCodium', remote: { scheme: 'vscodium' }, commands: ['codium'], flatpaks: ['com.vscodium.codium'], apps: ['VSCodium'] },
  { id: 'vscode-insiders', name: 'VS Code Insiders', remote: { scheme: 'vscode-insiders' }, commands: ['code-insiders'], apps: ['Visual Studio Code - Insiders'] },
];

// Host is what finding editors reads of this computer, so tests can fake it.
export interface Host {
  platform: NodeJS.Platform;
  home: string;
  path: string; // $PATH
  // exists is whether an executable file, or a Mac app's folder, is there.
  exists: (path: string) => boolean;
}

// Found is an editor, how it opens a folder over SSH, and its command (Zed's,
// which is given the ssh:// argument).
export interface Found extends Editor {
  remote: Kind['remote'];
  command: string;
}

// searchDirs are the folders a command is looked for in: PATH's, then those
// installers use that a session's PATH may lack.
function searchDirs(host: Host): string[] {
  const dirs = host.path.split(':').filter(Boolean);
  dirs.push(posix.join(host.home, '.local/bin'), '/usr/local/bin', '/usr/bin', '/snap/bin');
  if (host.platform === 'darwin') dirs.push('/opt/homebrew/bin');
  return [...new Set(dirs)];
}

// findEditors lists the editors on host, each once, in kinds' order.
export function findEditors(host: Host): Found[] {
  if (host.platform === 'win32') return [];
  const dirs = searchDirs(host);
  const onPath = (name: string) => dirs.map((dir) => posix.join(dir, name)).find(host.exists) ?? null;
  const found: Found[] = [];
  for (const kind of kinds) {
    let command: string | null = null;
    if (host.platform === 'darwin') {
      // The app first: it's there whether or not its command was installed.
      // Its URL is the app's; Zed's command is the one inside it.
      for (const app of kind.apps ?? []) {
        const at = [`/Applications/${app}.app`, posix.join(host.home, 'Applications', `${app}.app`)].find(host.exists);
        if (at) {
          command = kind.remote === 'zed' ? `${at}/Contents/MacOS/cli` : at;
          break;
        }
      }
    }
    for (const name of kind.commands) {
      if (command) break;
      command = onPath(name);
    }
    if (host.platform === 'linux') {
      for (const id of kind.flatpaks ?? []) {
        if (command) break;
        command = ['/var/lib/flatpak/exports/bin', posix.join(host.home, '.local/share/flatpak/exports/bin')].map((dir) => posix.join(dir, id)).find(host.exists) ?? null;
      }
    }
    if (command) found.push({ id: kind.id, name: kind.name, remote: kind.remote, command });
  }
  return found;
}

// pick is the editor to open: the one picked, when it's still here, or else
// the first found.
export function pick(found: Found[], picked: string): Found | undefined {
  return found.find((e) => e.id === picked) ?? found[0];
}

// SSHSetup is what `agentbox ssh-config --json` answers: the user ssh logs in
// as, and what makes an instance's name its host name.
export interface SSHSetup {
  user: string;
  hostSuffix: string;
}

// Target is how an editor opens an agent's worktree: a URL for the system to
// open, or Zed's command with its ssh:// argument.
export type Target = { url: string } | { command: string; args: string[] };

// remoteTarget is where editor opens the worktree of the agent whose machine
// is instance: the path in the machine, on the host ssh knows it by.
export function remoteTarget(editor: Found, setup: SSHSetup, instance: string, worktree: string): Target {
  const host = instance + setup.hostSuffix;
  const path = worktree.split('/').map(encodeURIComponent).join('/');
  if (editor.remote === 'zed') return { command: editor.command, args: [`ssh://${encodeURIComponent(setup.user)}@${host}${path}`] };
  return { url: `${editor.remote.scheme}://vscode-remote/ssh-remote+${host}${path}` };
}

// thisHost is this computer, as findEditors reads it.
export function thisHost(): Host {
  return {
    platform: process.platform,
    home: homedir(),
    path: process.env.PATH ?? '',
    exists: (path) => {
      try {
        if (path.endsWith('.app')) return statSync(path).isDirectory();
        accessSync(path, constants.X_OK);
        return statSync(path).isFile();
      } catch {
        return false;
      }
    },
  };
}

// listEditors is what the renderer is told: each editor's ID and name.
export function listEditors(): Editor[] {
  return findEditors(thisHost()).map(({ id, name }) => ({ id, name }));
}

export type OpenError = 'none' | 'ssh' | 'failed';

// sshSetup runs `agentbox ssh-config --json`, which makes sure ssh can reach
// agents' machines (the key, AgentBox's config and its Include line) and says
// how.
export function sshSetup(bin: string): Promise<SSHSetup> {
  return new Promise((resolve, reject) => {
    execFile(bin, ['ssh-config', '--json'], { timeout: 30_000, windowsHide: true }, (err, stdout, stderr) => {
      if (err) return reject(new Error(String(stderr).trim() || err.message));
      try {
        const setup = JSON.parse(String(stdout)) as SSHSetup;
        if (!setup.user || !setup.hostSuffix) throw new Error(`agentbox ssh-config said ${String(stdout).trim()}`);
        resolve(setup);
      } catch (e) {
        reject(e instanceof Error ? e : new Error(String(e)));
      }
    });
  });
}

// openInEditor opens an agent's worktree (its path in the agent's machine,
// whose Incus instance is instance) in the editor picked, or the first
// found, over SSH; it answers the editor's name, or why it couldn't. bin is
// the agentbox command, and openURL the system's opener (Electron's shell).
export async function openInEditor(
  picked: string,
  instance: string,
  worktree: string,
  bin: string,
  openURL: (url: string) => Promise<void>,
): Promise<{ name: string } | { error: OpenError; detail?: string }> {
  const editor = pick(findEditors(thisHost()), picked);
  if (!editor) return { error: 'none' };
  let setup: SSHSetup;
  try {
    setup = await sshSetup(bin);
  } catch (err) {
    return { error: 'ssh', detail: err instanceof Error ? err.message : String(err) };
  }
  try {
    const target = remoteTarget(editor, setup, instance, worktree);
    if ('url' in target) await openURL(target.url);
    else await launch(target.command, target.args);
    return { name: editor.name };
  } catch (err) {
    return { error: 'failed', detail: err instanceof Error ? err.message : String(err) };
  }
}

// launch starts a command on its own, detached from this app, and resolves
// once it has started.
function launch(command: string, args: string[]): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { detached: true, stdio: 'ignore' });
    child.once('error', reject);
    child.once('spawn', () => {
      child.unref();
      resolve();
    });
  });
}
