// Where AgentBox keeps its state: ~/.agentbox, or AGENTBOX_HOME when that is
// an absolute path, as the CLI has it (internal/paths).
import { lstatSync } from 'node:fs';
import { homedir } from 'node:os';
import { isAbsolute, join, resolve } from 'node:path';

function xdg(name: string, fallback: string): string {
  const value = process.env[name];
  return value && isAbsolute(value) ? value : fallback;
}

const agentboxHome = process.env.AGENTBOX_HOME;
export const dataDir = agentboxHome && isAbsolute(agentboxHome) ? resolve(agentboxHome) : join(homedir(), '.agentbox');
export const socketPath = process.env.AGENTBOX_SOCKET || join(dataDir, 'run', 'agentbox.sock');

// legacyDataDir is where earlier versions kept it, ~/.local/share/agentbox
// (or XDG_DATA_HOME's): the first agentbox command of this one moves it to
// dataDir (internal/datamove).
export const legacyDataDir = join(xdg('XDG_DATA_HOME', join(homedir(), '.local', 'share')), 'agentbox');

// dataMovePending reports whether legacyDataDir is still there to move.
export function dataMovePending(): boolean {
  if (legacyDataDir === dataDir) return false;
  try {
    return lstatSync(legacyDataDir).isDirectory();
  } catch {
    return false;
  }
}
