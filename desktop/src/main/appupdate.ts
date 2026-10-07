// Updating the app in place: "Update available" downloads the new release's
// package for this OS and architecture, checks it against the release's
// SHA256SUMS, puts it where this copy of the app is, and the app restarts into
// it. What the new app does as it starts brings the rest along: it copies its
// own agentbox binaries over the ones the daemon runs from (cli.ts), restarts a
// daemon of another version (restartIfStale in daemon.ts), and on a Mac
// `agentbox daemon start` installs the new Linux agentbox in the VM first
// (internal/hostvm). So the command-line tool, the VM's agentbox and the
// daemon end up on the app's version.
//
// Nothing here needs a password. A Mac's .app is replaced by renaming it aside
// and the new one into its place, in its own folder; a portable .exe the same
// way, file for file (an AppImage updates through appimageupdate.ts); the Windows per-user installer installs
// without asking for admin rights. Anything else (a .deb or .pacman, an app
// still on its disk image) opens the release page instead, as before.
//
// Squirrel (Electron's autoUpdater, and electron-updater on a Mac) isn't used:
// on a Mac it only takes an update whose signature satisfies the running app's
// designated requirement, which an ad hoc signed build's never does, and every
// build is ad hoc signed until the repository has Apple's certificate. The
// SHA256SUMS check is what stands in: it catches a corrupt or swapped
// download, the same trust as downloading the release by hand. A Mac app that
// is Developer ID signed additionally has to be signed by the same team as the
// running one and pass `spctl --assess`, Gatekeeper's own check.
//
// This file has no Electron in it, so that its tests can run in Node; the
// main process's half (updater.ts) wires it to the window and restarts.
import { createHash } from 'node:crypto';
import { createWriteStream, existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync } from 'node:fs';
import { basename, dirname, join, win32 } from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import type { AppUpdateErrorCode, AppUpdateProgress, AppUpdateSupport, InstallKind } from '../shared/appupdate.ts';

export interface Asset {
  name: string;
  url: string;
  size: number;
}

export interface Release {
  version: string;
  url: string;
  assets?: Asset[];
}

export class UpdateError extends Error {
  readonly code: AppUpdateErrorCode;
  constructor(code: AppUpdateErrorCode, message: string) {
    super(message);
    this.code = code;
  }
}

// Install is where this copy of the app is: path is the .app bundle on a Mac,
// the AppImage or portable .exe file, or the installer's install folder.
export interface Install {
  kind: InstallKind;
  path: string;
}

// Machine is what detectInstall looks at, passed in for the tests.
export interface Machine {
  platform: NodeJS.Platform;
  execPath: string;
  env: Record<string, string | undefined>;
  packaged: boolean;
  writable: (dir: string) => boolean;
  exists: (path: string) => boolean;
}

// The uninstaller electron-builder's NSIS installer leaves beside the app.
const nsisUninstaller = 'Uninstall AgentBox.exe';

export function detectInstall(m: Machine): { support: AppUpdateSupport; install?: Install } {
  const no = (reason: Extract<AppUpdateSupport, { inPlace: false }>['reason']) => ({ support: { inPlace: false as const, reason } });
  const yes = (kind: InstallKind, path: string, dir: string) =>
    m.writable(dir) ? { support: { inPlace: true as const, kind }, install: { kind, path } } : no('notWritable');
  if (!m.packaged) return no('dev');
  switch (m.platform) {
    case 'darwin': {
      // …/AgentBox.app/Contents/MacOS/AgentBox
      const app = dirname(dirname(dirname(m.execPath)));
      if (!app.endsWith('.app')) return no('unknown');
      if (app.startsWith('/Volumes/')) return no('macDiskImage');
      if (app.includes('/AppTranslocation/')) return no('macTranslocated');
      return yes('mac', app, dirname(app));
    }
    case 'linux': {
      const image = m.env.APPIMAGE;
      if (image) return yes('appimage', image, dirname(image));
      return no('linuxPackage');
    }
    case 'win32': {
      const portable = m.env.PORTABLE_EXECUTABLE_FILE;
      if (portable) return yes('portable', portable, win32.dirname(portable));
      const dir = win32.dirname(m.execPath);
      if (m.exists(win32.join(dir, nsisUninstaller))) return yes('nsis', dir, dir);
      return no('unknown');
    }
  }
  return no('unknown');
}

// assetName is the release's file for kind, as electron-builder names them
// (desktop/package.json's artifactName): arch is Node's (process.arch).
export function assetName(kind: Exclude<InstallKind, 'appimage'>, version: string, arch: string): string {
  switch (kind) {
    case 'mac':
      return `AgentBox-${version}-mac-${arch}.zip`;
    case 'nsis':
      return `AgentBox-${version}-${arch}-setup.exe`;
    case 'portable':
      return `AgentBox-${version}-${arch}-portable.exe`;
  }
}

// parseSums reads a SHA256SUMS: sha256sum's and shasum's "<hex>  <name>", or
// "<hex> *<name>" for a sum made in binary mode.
export function parseSums(text: string): Map<string, string> {
  const sums = new Map<string, string>();
  for (const line of text.split(/\r?\n/)) {
    const m = /^([0-9a-fA-F]{64}) [ *](.+)$/.exec(line.trim());
    if (m) sums.set(m[2], m[1].toLowerCase());
  }
  return sums;
}

type Fetch = (url: string) => Promise<Response>;

// The sums files a release has: SHA256SUMS for everything a nightly has and a
// release's Linux and Windows files, SHA256SUMS-mac for a release's Mac files.
const sumsFiles = ['SHA256SUMS', 'SHA256SUMS-mac'];

// expectedSum is name's SHA256 as the release lists it.
export async function expectedSum(release: Release, name: string, fetch: Fetch): Promise<string> {
  for (const file of sumsFiles) {
    const asset = release.assets?.find((a) => a.name === file);
    if (!asset) continue;
    let text: string;
    try {
      const res = await fetch(asset.url);
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      text = await res.text();
    } catch (err) {
      throw new UpdateError('download', `${file}: ${message(err)}`);
    }
    const sum = parseSums(text).get(name);
    if (sum) return sum;
  }
  throw new UpdateError('noChecksum', `the release's SHA256SUMS doesn't list ${name}`);
}

// download writes url to dest, telling onProgress as the bytes arrive, and
// answers their SHA256. A download that runs past size (when it's known) is
// cut off: it isn't the file the release lists.
export async function download(url: string, dest: string, size: number, fetch: Fetch, onProgress: (received: number, total: number) => void): Promise<string> {
  let res: Response;
  try {
    res = await fetch(url);
  } catch (err) {
    throw new UpdateError('download', message(err));
  }
  if (!res.ok || !res.body) throw new UpdateError('download', `${url}: ${res.status} ${res.statusText}`);
  const total = size || Number(res.headers.get('content-length')) || 0;
  const hash = createHash('sha256');
  let received = 0;
  let reported = 0;
  const count = new Transform({
    transform(chunk: Buffer, _enc, done) {
      received += chunk.length;
      if (size && received > size) return done(new UpdateError('checksum', `${basename(dest)} is larger than the ${size} bytes the release lists`));
      hash.update(chunk);
      // Every percent or so: each one is a message to the window.
      if (received - reported >= Math.max(total / 100, 256 << 10) || received === total) {
        reported = received;
        onProgress(received, total);
      }
      done(null, chunk);
    },
  });
  mkdirSync(dirname(dest), { recursive: true });
  try {
    await pipeline(Readable.fromWeb(res.body as import('node:stream/web').ReadableStream), count, createWriteStream(dest, { mode: 0o755 }));
  } catch (err) {
    rmSync(dest, { force: true });
    throw err instanceof UpdateError ? err : new UpdateError('download', message(err));
  }
  return hash.digest('hex');
}

// Run runs a command and answers what it printed, stdout and stderr together
// (codesign prints what it found on stderr); it rejects when the command fails.
export type Run = (cmd: string, args: string[]) => Promise<string>;

export interface UpdateOptions {
  release: Release;
  install: Install;
  arch: string; // process.arch
  tmp: string; // a folder of the app's own for the download, where it isn't the install's
  fetch: Fetch;
  run: Run;
  onProgress: (p: AppUpdateProgress) => void;
}

// update downloads, checks and puts in place the release's package for
// install, and answers what to start once the app has quit: the new app (the
// same path as the old), or on Windows the installer that installs it.
export async function update(o: UpdateOptions): Promise<{ relaunch: string } | { installer: string }> {
  const { release, install: chosen } = o;
  const version = release.version;
  // An AppImage updates through electron-updater (appimageupdate.ts), which
  // downloads only what changed, not through here.
  if (chosen.kind === 'appimage') throw new UpdateError('unsupported', 'an AppImage updates with electron-updater');
  const install = { ...chosen, kind: chosen.kind };
  if (!release.assets?.length) throw new UpdateError('noAssets', `The release list has no files for ${version}`);
  const name = assetName(install.kind, version, o.arch);
  const asset = release.assets.find((a) => a.name === name);
  if (!asset) throw new UpdateError('noBuild', `${version} has no ${name}`);
  const want = await expectedSum(release, name, o.fetch);

  const dest = downloadPath(install, o.tmp, name);
  const got = await download(asset.url, dest, asset.size, o.fetch, (received, total) =>
    o.onProgress({ phase: 'download', version, received, total }),
  );
  o.onProgress({ phase: 'verify', version });
  if (got !== want) {
    rmSync(dest, { force: true });
    throw new UpdateError('checksum', `${name}'s SHA256 is ${got}, and SHA256SUMS says ${want}`);
  }

  switch (install.kind) {
    case 'mac': {
      let app: string;
      try {
        app = await unpackMacApp(dest, install.path, version, o.run);
      } catch (err) {
        rmSync(macStaging(install.path), { recursive: true, force: true });
        throw err;
      } finally {
        rmSync(dest, { force: true });
      }
      o.onProgress({ phase: 'install', version });
      swap(app, install.path, macOld(install.path));
      rmSync(macStaging(install.path), { recursive: true, force: true });
      return { relaunch: install.path };
    }
    case 'portable':
      o.onProgress({ phase: 'install', version });
      // A running .exe can't be replaced, but it can be renamed: the old one
      // goes aside, and is removed the next time the app starts.
      swap(dest, install.path, `${install.path}.old-${Date.now()}`);
      return { relaunch: install.path };
    case 'nsis':
      o.onProgress({ phase: 'install', version });
      return { installer: dest };
  }
}

// downloadPath is where name downloads to: beside what it replaces, so that
// putting it in place is a rename on the same disk, or for the installer in tmp.
function downloadPath(install: Install & { kind: Exclude<InstallKind, 'appimage'> }, tmp: string, name: string): string {
  switch (install.kind) {
    case 'mac':
      return join(tmp, name);
    case 'portable':
      return join(dirname(install.path), `.${basename(install.path)}.update`);
    case 'nsis':
      return join(tmp, name);
  }
}

// The new Mac app is unpacked beside the old one, and the old one is moved
// aside there until the next start (cleanUp): a running app's files are still
// in use.
const macStaging = (app: string) => join(dirname(app), `.${basename(app)}.update`);
const macOld = (app: string) => join(dirname(app), `.${basename(app)}.old`);

// unpackMacApp unpacks the release's .zip beside app with ditto, which keeps
// the bundle's symlinks and signature intact, and checks what came out: the
// same app (CFBundleIdentifier), the version asked for, a code signature that
// verifies, and, when the running app has a Developer ID, the same team and
// Gatekeeper's assent. It answers the new .app's path.
export async function unpackMacApp(zip: string, app: string, version: string, run: Run): Promise<string> {
  const staging = macStaging(app);
  rmSync(staging, { recursive: true, force: true });
  mkdirSync(staging, { recursive: true });
  try {
    await run('ditto', ['-x', '-k', zip, staging]);
  } catch (err) {
    throw new UpdateError('install', `unpacking ${basename(zip)}: ${message(err)}`);
  }
  const name = readdirSync(staging).find((n) => n.endsWith('.app'));
  if (!name) throw new UpdateError('signature', `${basename(zip)} has no app in it`);
  const fresh = join(staging, name);
  const plist = (path: string) => readFileSync(join(path, 'Contents', 'Info.plist'), 'utf8');
  const id = plistString(plist(app), 'CFBundleIdentifier');
  const newId = plistString(plist(fresh), 'CFBundleIdentifier');
  if (!id || newId !== id) throw new UpdateError('signature', `the new app is ${newId}, not ${id}`);
  const newVersion = plistString(plist(fresh), 'CFBundleShortVersionString');
  if (newVersion !== version) throw new UpdateError('signature', `the new app says it is ${newVersion}, not ${version}`);
  try {
    await run('codesign', ['--verify', '--deep', '--strict', fresh]);
  } catch (err) {
    throw new UpdateError('signature', `the new app's signature doesn't verify: ${message(err)}`);
  }
  const team = await teamOf(app, run);
  if (team) {
    const newTeam = await teamOf(fresh, run);
    if (newTeam !== team) throw new UpdateError('signature', `the new app is signed by ${newTeam ?? 'nobody'}, not ${team}`);
    try {
      await run('spctl', ['--assess', '--type', 'execute', fresh]);
    } catch (err) {
      throw new UpdateError('signature', `Gatekeeper refused the new app: ${message(err)}`);
    }
  }
  return fresh;
}

// teamOf is the Developer ID team an app is signed by, or null for an ad hoc
// signature ("TeamIdentifier=not set").
async function teamOf(app: string, run: Run): Promise<string | null> {
  const out = await run('codesign', ['-dv', '--verbose=2', app]).catch(() => '');
  const team = /^TeamIdentifier=(.+)$/m.exec(out)?.[1].trim();
  return team && team !== 'not set' ? team : null;
}

export function plistString(plist: string, key: string): string | undefined {
  const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp(`<key>${escaped}</key>\\s*<string>([^<]*)</string>`).exec(plist)?.[1];
}

// swap puts fresh where target is, moving target to old first, and moves it
// back when fresh can't go in.
export function swap(fresh: string, target: string, old: string): void {
  rmSync(old, { recursive: true, force: true });
  try {
    renameSync(target, old);
  } catch (err) {
    rmSync(fresh, { recursive: true, force: true });
    throw new UpdateError('install', message(err));
  }
  try {
    renameSync(fresh, target);
  } catch (err) {
    renameSync(old, target);
    rmSync(fresh, { recursive: true, force: true });
    throw new UpdateError('install', message(err));
  }
}

// cleanUp removes what an update left beside the app: the old app moved aside,
// and a download or unpacking an update that failed didn't remove. A portable
// .exe of an earlier version is in use until that one exits, so it may stay a
// while longer.
export function cleanUp(install: Install): void {
  const dir = dirname(install.path);
  const name = basename(install.path);
  const leftovers =
    install.kind === 'mac'
      ? [macOld(install.path), macStaging(install.path)]
      : install.kind === 'nsis'
        ? []
        : [join(dir, `.${name}.update`), ...safeList(dir).filter((n) => n.startsWith(`${name}.old-`)).map((n) => join(dir, n))];
  for (const path of leftovers) {
    if (!existsSync(path)) continue;
    try {
      rmSync(path, { recursive: true, force: true });
    } catch {
      // still running
    }
  }
}

function safeList(dir: string): string[] {
  try {
    return readdirSync(dir);
  } catch {
    return [];
  }
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
