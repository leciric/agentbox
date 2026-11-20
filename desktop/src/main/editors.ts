// The code editors on this computer that can open an agent's worktree: the
// agent view's "Open in …" and Settings → Editor. The worktree is on this
// computer at the path the daemon gives (a Mac's and Linux's are shared with
// the VM at the same path; Windows reads WSL's through \\wsl.localhost), so
// the editor runs here, as the user's own, on the agent's files.
//
// An editor is found by its command on PATH, in the folders installers put
// one that a desktop session's PATH may lack, as a Flatpak (exported by its
// app ID), or on a Mac as an app in Applications, which a Mac app started
// from the Dock needs: its PATH is /usr/bin:/bin:/usr/sbin:/sbin. Only these
// editors can be opened, each with the folder as its one argument: the
// renderer can't name a command to run. The native app finds the same ones
// (native/crates/app/src/editors.rs).
import { spawn } from 'node:child_process';
import { accessSync, constants, statSync } from 'node:fs';
import { homedir } from 'node:os';
import { posix, win32 } from 'node:path';

export interface Editor {
  id: string;
  name: string;
}

interface Kind extends Editor {
  commands: string[]; // on PATH (on Windows, with PATHEXT's extensions)
  flatpaks?: string[]; // Linux: Flatpak app IDs
  apps?: string[]; // Mac: app bundles, without .app
}

// kinds, in the order the first one found is taken when none is picked.
export const kinds: Kind[] = [
  { id: 'vscode', name: 'VS Code', commands: ['code'], flatpaks: ['com.visualstudio.code'], apps: ['Visual Studio Code'] },
  { id: 'cursor', name: 'Cursor', commands: ['cursor'], apps: ['Cursor'] },
  { id: 'windsurf', name: 'Windsurf', commands: ['windsurf'], apps: ['Windsurf'] },
  { id: 'zed', name: 'Zed', commands: ['zed', 'zeditor', 'zedit'], flatpaks: ['dev.zed.Zed'], apps: ['Zed'] },
  { id: 'vscodium', name: 'VSCodium', commands: ['codium'], flatpaks: ['com.vscodium.codium'], apps: ['VSCodium'] },
  { id: 'vscode-insiders', name: 'VS Code Insiders', commands: ['code-insiders'], apps: ['Visual Studio Code - Insiders'] },
  { id: 'sublime', name: 'Sublime Text', commands: ['subl'], flatpaks: ['com.sublimetext.three'], apps: ['Sublime Text'] },
  {
    id: 'idea',
    name: 'IntelliJ IDEA',
    commands: ['idea', 'intellij-idea-ultimate', 'intellij-idea-community'],
    flatpaks: ['com.jetbrains.IntelliJ-IDEA-Ultimate', 'com.jetbrains.IntelliJ-IDEA-Community'],
    apps: ['IntelliJ IDEA', 'IntelliJ IDEA Ultimate', 'IntelliJ IDEA CE'],
  },
  { id: 'goland', name: 'GoLand', commands: ['goland'], flatpaks: ['com.jetbrains.GoLand'], apps: ['GoLand'] },
  { id: 'webstorm', name: 'WebStorm', commands: ['webstorm'], flatpaks: ['com.jetbrains.WebStorm'], apps: ['WebStorm'] },
  {
    id: 'pycharm',
    name: 'PyCharm',
    commands: ['pycharm', 'pycharm-professional', 'pycharm-community'],
    flatpaks: ['com.jetbrains.PyCharm-Professional', 'com.jetbrains.PyCharm-Community'],
    apps: ['PyCharm', 'PyCharm Professional Edition', 'PyCharm CE', 'PyCharm Community Edition'],
  },
  { id: 'rustrover', name: 'RustRover', commands: ['rustrover'], apps: ['RustRover'] },
];

// Host is what finding editors reads of this computer, so tests can fake it.
export interface Host {
  platform: NodeJS.Platform;
  home: string;
  path: string; // $PATH
  pathext?: string; // Windows' $PATHEXT
  // exists is whether an executable file, or a Mac app's folder, is there.
  exists: (path: string) => boolean;
}

// Found is an editor and the command that opens a folder in it: the folder
// goes after args.
export interface Found extends Editor {
  command: string;
  args: string[];
}

// searchDirs are the folders a command is looked for in: PATH's, then those
// installers use that a session's PATH may lack.
function searchDirs(host: Host): string[] {
  const sep = host.platform === 'win32' ? ';' : ':';
  const dirs = host.path.split(sep).filter(Boolean);
  if (host.platform !== 'win32') {
    const p = posix;
    dirs.push(p.join(host.home, '.local/bin'), '/usr/local/bin', '/usr/bin', '/snap/bin');
    if (host.platform === 'darwin') {
      dirs.push('/opt/homebrew/bin', p.join(host.home, 'Library/Application Support/JetBrains/Toolbox/scripts'));
    } else {
      dirs.push(p.join(host.home, '.local/share/JetBrains/Toolbox/scripts'));
    }
  }
  return [...new Set(dirs)];
}

// findEditors lists the editors on host, each once, in kinds' order.
export function findEditors(host: Host): Found[] {
  const win = host.platform === 'win32';
  const join = win ? win32.join : posix.join;
  const dirs = searchDirs(host);
  const exts = win ? (host.pathext || '.COM;.EXE;.BAT;.CMD').split(';').filter(Boolean) : [''];
  const onPath = (name: string): string | null => {
    for (const dir of dirs) {
      for (const ext of exts) {
        const path = join(dir, name + ext.toLowerCase());
        if (host.exists(path)) return path;
      }
    }
    return null;
  };
  const found: Found[] = [];
  for (const kind of kinds) {
    const editor = { id: kind.id, name: kind.name };
    let open: Pick<Found, 'command' | 'args'> | null = null;
    if (host.platform === 'darwin') {
      // The app first: it's there whether or not its command was installed.
      for (const app of kind.apps ?? []) {
        const at = [`/Applications/${app}.app`, posix.join(host.home, 'Applications', `${app}.app`)].find(host.exists);
        if (at) {
          open = { command: 'open', args: ['-a', at] };
          break;
        }
      }
    }
    for (const name of kind.commands) {
      if (open) break;
      const path = onPath(name);
      if (path) open = { command: path, args: [] };
    }
    if (host.platform === 'linux') {
      for (const id of kind.flatpaks ?? []) {
        if (open) break;
        const at = ['/var/lib/flatpak/exports/bin', posix.join(host.home, '.local/share/flatpak/exports/bin')]
          .map((dir) => posix.join(dir, id))
          .find(host.exists);
        if (at) open = { command: at, args: [] };
      }
    }
    if (open) found.push({ ...editor, ...open });
  }
  return found;
}

// pick is the editor to open: the one picked, when it's still here, or else
// the first found.
export function pick(found: Found[], picked: string): Found | undefined {
  return found.find((e) => e.id === picked) ?? found[0];
}

// thisHost is this computer, as findEditors reads it.
export function thisHost(): Host {
  return {
    platform: process.platform,
    home: homedir(),
    path: process.env.PATH ?? '',
    pathext: process.env.PATHEXT,
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

export type OpenError = 'none' | 'folder' | 'failed';

// openInEditor opens folder (a path on this computer) in the editor picked,
// or the first found; it answers the editor's name, or why it couldn't.
export async function openInEditor(picked: string, folder: string): Promise<{ name: string } | { error: OpenError; detail?: string }> {
  const editor = pick(findEditors(thisHost()), picked);
  if (!editor) return { error: 'none' };
  try {
    if (!statSync(folder).isDirectory()) return { error: 'folder' };
  } catch {
    return { error: 'folder' };
  }
  try {
    await launch(editor, folder);
    return { name: editor.name };
  } catch (err) {
    return { error: 'failed', detail: err instanceof Error ? err.message : String(err) };
  }
}

// launch starts the editor on its own, detached from this app, and resolves
// once it has started.
function launch(editor: Found, folder: string): Promise<void> {
  return new Promise((resolve, reject) => {
    // Windows' editor commands are mostly .cmd scripts, which only run
    // through cmd.exe: quoted, as no Windows path has a quote in it.
    const child =
      process.platform === 'win32'
        ? spawn([editor.command, ...editor.args, folder].map((a) => `"${a}"`).join(' '), { shell: true, detached: true, stdio: 'ignore', windowsHide: true })
        : spawn(editor.command, [...editor.args, folder], { detached: true, stdio: 'ignore' });
    child.once('error', reject);
    child.once('spawn', () => {
      child.unref();
      resolve();
    });
  });
}
