// Packages AgentBox as an AppImage, a .deb and a .pacman in dist/, with the
// agentbox command-line tool inside each (resources/bin/agentbox), built for
// this version.
//
// With --mac it packages the .dmg and .zip instead, for each architecture asked
// for (default: this machine's). A Mac's app carries two binaries: the macOS
// agentbox, which is the front end of AgentBox's Linux VM (internal/hostvm),
// and the Linux agentbox it installs in that VM, for the same architecture
// (the VM runs the Mac's own). electron-builder makes .dmg files only on a Mac.
//
// With --win, packages it for Windows instead: an NSIS installer and a
// portable .exe, with two agentbox binaries inside, the Windows front end
// (resources/bin/agentbox.exe) and the Linux one it installs into AgentBox's
// WSL distro (resources/bin/agentbox-linux).
//
//   npm run dist
//   npm run dist -- --mac [--arm64] [--x64]
//   npm run dist -- --win
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { arch as hostArch } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const repo = join(root, '..');
const { version } = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
const run = (cmd, args, cwd = root, env = {}) => execFileSync(cmd, args, { cwd, stdio: 'inherit', env: { ...process.env, ...env } });

const ldflags = `-s -w -X agentbox/internal/cli.version=${version} -X agentbox/internal/daemon.Version=${version}`;
const goBuild = (out, env = {}) =>
  run('go', ['build', '-trimpath', '-ldflags', ldflags, '-o', join(repo, 'bin', out), './cmd/agentbox'], repo, env);

const args = process.argv.slice(2);
if (args.includes('--win')) {
  goBuild('agentbox.exe', { GOOS: 'windows', GOARCH: 'amd64' });
  goBuild('agentbox-linux', { GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' });
  run('node', [join(root, 'scripts', 'build.mjs')]);
  run('npx', ['electron-builder', '--win', 'nsis', 'portable', '--x64', '--publish', 'never']);
} else if (!args.includes('--mac')) {
  goBuild('agentbox');
  run('node', [join(root, 'scripts', 'build.mjs')]);
  run('npx', ['electron-builder', '--linux', 'AppImage', 'deb', 'pacman', '--publish', 'never']);
} else {
  // electron-builder's names for the architectures, and Go's.
  const goArch = { arm64: 'arm64', x64: 'amd64' };
  let archs = args.filter((a) => a === '--arm64' || a === '--x64').map((a) => a.slice(2));
  if (archs.length === 0) archs = [hostArch() === 'arm64' ? 'arm64' : 'x64'];
  run('node', [join(root, 'scripts', 'build.mjs')]);
  // One architecture at a time: both binaries go to the fixed paths the
  // package copies from, so each .dmg gets its own pair.
  for (const a of archs) {
    goBuild('agentbox', { GOOS: 'darwin', GOARCH: goArch[a], CGO_ENABLED: '0' });
    goBuild('agentbox-linux', { GOOS: 'linux', GOARCH: goArch[a], CGO_ENABLED: '0' });
    run('npx', ['electron-builder', '--mac', 'dmg', 'zip', `--${a}`, '--publish', 'never']);
  }
}
