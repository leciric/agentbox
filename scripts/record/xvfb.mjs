// Starts a private X display, so GUI apps can be recorded without opening
// windows on the desktop. It uses Xvfb from PATH, or else a copy extracted
// from Arch's xorg-server-xvfb package into ~/.cache/agentbox-record (no root
// needed, nothing installed).
import { execFileSync, spawn } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

const cacheDir = join(homedir(), '.cache', 'agentbox-record', 'xvfb');
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function findXvfb() {
  try {
    return execFileSync('sh', ['-c', 'command -v Xvfb'], { encoding: 'utf8' }).trim();
  } catch {
    // not installed
  }
  const cached = join(cacheDir, 'usr/bin/Xvfb');
  if (existsSync(cached)) return cached;
  const url = execFileSync('pacman', ['-Sp', '--print-format', '%l', 'xorg-server-xvfb'], { encoding: 'utf8' })
    .split('\n')
    .find((line) => line.includes('xorg-server-xvfb-'));
  if (!url) throw new Error('Xvfb not found: install xorg-server-xvfb');
  mkdirSync(cacheDir, { recursive: true });
  execFileSync('sh', ['-c', 'curl -fsSL "$1" | tar --zstd -x -C "$2" usr/bin/Xvfb', 'sh', url, cacheDir], { stdio: 'inherit' });
  return cached;
}

export async function startXvfb({ width = 1600, height = 1000 } = {}) {
  const bin = findXvfb();
  for (let n = 90; n < 110; n++) {
    const socket = `/tmp/.X11-unix/X${n}`;
    if (existsSync(socket) || existsSync(`/tmp/.X${n}-lock`)) continue;
    const proc = spawn(bin, [`:${n}`, '-screen', '0', `${width}x${height}x24`, '-nolisten', 'tcp'], { stdio: 'ignore' });
    for (let i = 0; i < 50 && !existsSync(socket); i++) await sleep(100);
    if (existsSync(socket)) {
      const stop = () => proc.kill();
      process.once('exit', stop);
      return { display: `:${n}`, stop };
    }
    proc.kill();
  }
  throw new Error('no free X display between :90 and :109');
}
