// The agentbox command line tool. A packaged app ships the binary: the app keeps
// its own copy in ~/.local/share/agentbox/bin (the daemon runs from it, so it
// outlives the app's mount), and "Install command-line tool" links it into
// ~/.local/bin. From a checkout, the tool is linked from AGENTBOX_BIN instead.
//
// On Windows the app ships agentbox.exe, the front end of AgentBox's WSL distro
// (internal/hostwsl), and the Linux agentbox it installs in the distro, which
// it looks for beside itself: both are kept in %LOCALAPPDATA%\AgentBox\bin, and
// "Install command-line tool" puts that folder on the user's PATH.
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, existsSync, lstatSync, mkdirSync, readdirSync, readFileSync, renameSync, statSync, symlinkSync, unlinkSync } from 'node:fs';
import { homedir } from 'node:os';
import { basename, dirname, join } from 'node:path';
import { app } from 'electron';
import { dataDir } from './paths';
import { onWindows } from './relay';

export interface CliStatus {
  linkPath: string; // where the command is installed: ~/.local/bin/agentbox
  linked: boolean; // linkPath exists
  path: string | null; // what `agentbox` runs in a login shell, if anything
  version: string | null;
  onPath: boolean; // ~/.local/bin is on a login shell's PATH
  bundled: boolean; // this app ships an agentbox binary
  binary: string | null; // the agentbox binary the app runs, when it knows where it is
}

const exe = onWindows ? 'agentbox.exe' : 'agentbox';
const localBin = join(homedir(), '.local', 'bin');
const managedDir = onWindows ? join(process.env.LOCALAPPDATA || join(homedir(), 'AppData', 'Local'), 'AgentBox', 'bin') : join(dataDir, 'bin');
const linkPath = onWindows ? join(managedDir, exe) : join(localBin, 'agentbox');
const managedBin = join(managedDir, exe);
// On a Mac and on Windows the app also ships the Linux agentbox, which the
// front end (of AgentBox's Linux VM, or of its WSL distro) installs there. The
// front end looks for it beside itself, so it's kept beside the managed copy too.
const linuxName = 'agentbox-linux';

function bundledBin(): string | null {
  if (!app.isPackaged) return null;
  const bin = join(process.resourcesPath, 'bin', exe);
  return existsSync(bin) ? bin : null;
}

function run(cmd: string, args: string[], env?: NodeJS.ProcessEnv): Promise<string | null> {
  return new Promise((resolve) =>
    execFile(cmd, args, { timeout: 10_000, windowsHide: true, env: env ? { ...process.env, ...env } : undefined }, (err, stdout) =>
      resolve(err ? null : stdout.trim() || null),
    ),
  );
}

// keepCurrent makes dst a copy of src, unless it is one. A running .exe can't
// be replaced on Windows, but it can be renamed: the old one is moved aside,
// and removed the next time it isn't running.
function keepCurrent(src: string, dst: string): void {
  if (existsSync(dst) && statSync(dst).size === statSync(src).size && digest(dst) === digest(src)) return;
  mkdirSync(dirname(dst), { recursive: true });
  for (const name of readdirSync(dirname(dst))) {
    if (!name.startsWith(`${basename(dst)}.old-`)) continue;
    try {
      unlinkSync(join(dirname(dst), name));
    } catch {
      // still running
    }
  }
  copyFileSync(src, dst + '.new');
  chmodSync(dst + '.new', 0o755);
  if (onWindows && existsSync(dst)) {
    const old = `${dst}.old-${Date.now()}`;
    renameSync(dst, old);
    try {
      unlinkSync(old);
    } catch {
      // still running
    }
  }
  renameSync(dst + '.new', dst);
}

function digest(file: string): string {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

// agentboxBin is the binary the app starts the daemon with.
export function agentboxBin(): string {
  if (process.env.AGENTBOX_BIN) return process.env.AGENTBOX_BIN;
  const bundled = bundledBin();
  if (!bundled) return 'agentbox';
  // Keep the app's own copy current, so the daemon and agents get this version.
  keepCurrent(bundled, managedBin);
  const linux = join(dirname(bundled), linuxName);
  if (existsSync(linux)) keepCurrent(linux, join(managedDir, linuxName));
  return managedBin;
}

function isLink(path: string): boolean {
  try {
    return lstatSync(path).isSymbolicLink();
  } catch {
    return false;
  }
}

// inShell runs command the way a new terminal would: the user's own shell,
// login and interactive, since zsh (a Mac's default) reads ~/.zshrc only when
// interactive. What the profile prints comes first, so the answer is the last line.
async function inShell(command: string): Promise<string | null> {
  const out = await run(process.env.SHELL || 'bash', ['-lic', command]);
  return out?.split('\n').pop()?.trim() || null;
}

export async function cliStatus(): Promise<CliStatus> {
  if (onWindows) return windowsCliStatus();
  // bash's answer too, for a shell whose PATH isn't colon-separated (fish).
  const path = (await inShell('command -v agentbox')) ?? (await run('bash', ['-lc', 'command -v agentbox']));
  const shellPath = [await inShell('printf %s "$PATH"'), await run('bash', ['-lc', 'printf %s "$PATH"'])].join(':');
  return {
    linkPath,
    linked: existsSync(linkPath) || isLink(linkPath),
    path,
    version: path ? await run(path, ['--version']) : null,
    onPath: shellPath.split(':').includes(localBin),
    bundled: bundledBin() !== null || Boolean(process.env.AGENTBOX_BIN),
    binary: bundledBin() ? agentboxBin() : (process.env.AGENTBOX_BIN ?? null),
  };
}

// installCli links the agentbox command into ~/.local/bin. It replaces an
// earlier link, but never a file it didn't make.
export async function installCli(): Promise<CliStatus> {
  const target = bundledBin() ? agentboxBin() : process.env.AGENTBOX_BIN;
  if (!target) throw new Error('this app has no agentbox binary to install: build it with go build -o bin/agentbox ./cmd/agentbox');
  if (onWindows) {
    await addToUserPath(dirname(target));
    return cliStatus();
  }
  mkdirSync(localBin, { recursive: true });
  if (isLink(linkPath)) unlinkSync(linkPath);
  else if (existsSync(linkPath)) throw new Error(`${linkPath} already exists and isn't a link: remove it first`);
  symlinkSync(target, linkPath);
  return cliStatus();
}

// The user's own PATH, from the registry: this process's PATH is what it
// started with, and PowerShell reads the one a new terminal gets.
const userPathScript = "[Environment]::GetEnvironmentVariable('Path', 'User')";

async function userPath(): Promise<string[]> {
  const out = await run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', userPathScript]);
  return (out ?? '').split(';').filter(Boolean);
}

// addToUserPath puts dir on the user's PATH, for terminals opened after it.
// The folder goes in through the environment, so no quoting can break it.
async function addToUserPath(dir: string): Promise<void> {
  const script = `$d = $env:AGENTBOX_BIN_DIR; $p = ${userPathScript}; if (-not (($p -split ';') -contains $d)) { [Environment]::SetEnvironmentVariable('Path', ((@($p.TrimEnd(';'), $d) | Where-Object { $_ }) -join ';'), 'User') }`;
  const done = await run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', `${script}; 'ok'`], { AGENTBOX_BIN_DIR: dir });
  if (done !== 'ok') throw new Error(`couldn't add ${dir} to your PATH`);
}

async function windowsCliStatus(): Promise<CliStatus> {
  const dir = bundledBin() ? managedDir : process.env.AGENTBOX_BIN ? dirname(process.env.AGENTBOX_BIN) : managedDir;
  const onPath = (await userPath()).some((p) => p.replace(/\\+$/, '').toLowerCase() === dir.toLowerCase());
  const path = (await run('where.exe', ['agentbox']))?.split(/\r?\n/)[0] ?? (onPath ? join(dir, exe) : null);
  return {
    linkPath: join(dir, exe),
    linked: onPath,
    path,
    version: path ? await run(path, ['--version']) : null,
    onPath,
    bundled: bundledBin() !== null || Boolean(process.env.AGENTBOX_BIN),
    binary: bundledBin() ? agentboxBin() : (process.env.AGENTBOX_BIN ?? null),
  };
}
