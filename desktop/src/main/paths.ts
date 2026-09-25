// Where AgentBox keeps its state, following the same XDG rules as the CLI.
import { homedir } from 'node:os';
import { isAbsolute, join } from 'node:path';

function xdg(name: string, fallback: string): string {
  const value = process.env[name];
  return value && isAbsolute(value) ? value : fallback;
}

export const dataDir = join(xdg('XDG_DATA_HOME', join(homedir(), '.local', 'share')), 'agentbox');
export const socketPath = process.env.AGENTBOX_SOCKET || join(dataDir, 'run', 'agentbox.sock');
