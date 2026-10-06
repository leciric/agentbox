// Run with `npm test` (node's own test runner, which strips the types).
// Against a fake release server and fake ditto/codesign: what is downloaded,
// what it's checked against, and what ends up where the app was.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, test } from 'node:test';
import type { AppUpdateProgress } from '../shared/appupdate.ts';
import { assetName, cleanUp, detectInstall, parseSums, plistString, swap, update, UpdateError, type Machine, type Release, type Run } from './appupdate.ts';

const sha = (data: string | Buffer) => createHash('sha256').update(data).digest('hex');

// The fake release server: path → body.
const files = new Map<string, string | Buffer>();
let server: Server;
let base = '';
before(async () => {
  server = createServer((req, res) => {
    const body = files.get(req.url ?? '');
    if (body === undefined) {
      res.writeHead(404).end('not found');
      return;
    }
    res.writeHead(200, { 'content-length': Buffer.byteLength(body) }).end(body);
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});
after(() => server.close());

const fetcher = (url: string) => fetch(url);

function tmp(): string {
  return mkdtempSync(join(process.env.TMPDIR || tmpdir(), 'appupdate-'));
}

// release serves a release whose assets are the given files, with a
// SHA256SUMS over them (sumsName, and what it lists, can be changed).
function release(version: string, assets: Record<string, string | Buffer>, sums?: { name?: string; body?: string }): Release {
  const list = Object.entries(assets).map(([name, body]) => {
    files.set(`/${version}/${name}`, body);
    return { name, url: `${base}/${version}/${name}`, size: Buffer.byteLength(body) };
  });
  const sumsName = sums?.name ?? 'SHA256SUMS';
  const sumsBody = sums?.body ?? Object.entries(assets).map(([name, body]) => `${sha(body)}  ${name}\n`).join('');
  files.set(`/${version}/${sumsName}`, sumsBody);
  list.push({ name: sumsName, url: `${base}/${version}/${sumsName}`, size: sumsBody.length });
  return { version, url: `https://github.com/leciric/agentbox/releases/tag/v${version}`, assets: list };
}

const noRun: Run = async (cmd) => {
  throw new Error(`${cmd} wasn't expected`);
};

function machine(over: Partial<Machine>): Machine {
  return { platform: 'linux', execPath: '/opt/AgentBox/agentbox-desktop', env: {}, packaged: true, writable: () => true, exists: () => false, ...over };
}

test('detectInstall: which installs update in place, and why the others open the release page', () => {
  const mac = '/Applications/AgentBox.app/Contents/MacOS/AgentBox';
  assert.deepEqual(detectInstall(machine({ platform: 'darwin', execPath: mac })), {
    support: { inPlace: true, kind: 'mac' },
    install: { kind: 'mac', path: '/Applications/AgentBox.app' },
  });
  assert.deepEqual(detectInstall(machine({ platform: 'darwin', execPath: '/Volumes/AgentBox 0.11.0/AgentBox.app/Contents/MacOS/AgentBox' })).support, {
    inPlace: false,
    reason: 'macDiskImage',
  });
  assert.deepEqual(
    detectInstall(machine({ platform: 'darwin', execPath: '/private/var/folders/x/T/AppTranslocation/1234/d/AgentBox.app/Contents/MacOS/AgentBox' })).support,
    { inPlace: false, reason: 'macTranslocated' },
  );
  // /Applications for a user who isn't an admin.
  assert.deepEqual(detectInstall(machine({ platform: 'darwin', execPath: mac, writable: () => false })).support, { inPlace: false, reason: 'notWritable' });

  assert.deepEqual(detectInstall(machine({ env: { APPIMAGE: '/home/ana/Apps/AgentBox-0.11.0-x86_64.AppImage' } })).install, {
    kind: 'appimage',
    path: '/home/ana/Apps/AgentBox-0.11.0-x86_64.AppImage',
  });
  // A .deb or .pacman lives in /opt, root's.
  assert.deepEqual(detectInstall(machine({})).support, { inPlace: false, reason: 'linuxPackage' });

  const win = 'C:\\Users\\ana\\AppData\\Local\\Programs\\AgentBox\\AgentBox.exe';
  assert.deepEqual(
    detectInstall(machine({ platform: 'win32', execPath: win, exists: (p) => p === 'C:\\Users\\ana\\AppData\\Local\\Programs\\AgentBox\\Uninstall AgentBox.exe' })).install,
    { kind: 'nsis', path: 'C:\\Users\\ana\\AppData\\Local\\Programs\\AgentBox' },
  );
  assert.deepEqual(
    detectInstall(machine({ platform: 'win32', execPath: 'C:\\Temp\\x\\AgentBox.exe', env: { PORTABLE_EXECUTABLE_FILE: 'D:\\AgentBox-0.11.0-x64-portable.exe' } })).install,
    { kind: 'portable', path: 'D:\\AgentBox-0.11.0-x64-portable.exe' },
  );
  assert.deepEqual(detectInstall(machine({ platform: 'win32', execPath: 'C:\\Program Files\\AgentBox\\AgentBox.exe' })).support, { inPlace: false, reason: 'unknown' });
  assert.deepEqual(detectInstall(machine({ packaged: false })).support, { inPlace: false, reason: 'dev' });
});

test('assetName follows the names the release workflows publish', () => {
  assert.equal(assetName('mac', '0.12.0', 'arm64'), 'AgentBox-0.12.0-mac-arm64.zip');
  assert.equal(assetName('mac', '0.12.0-nightly.20261005.58', 'x64'), 'AgentBox-0.12.0-nightly.20261005.58-mac-x64.zip');
  assert.equal(assetName('nsis', '0.12.0', 'x64'), 'AgentBox-0.12.0-x64-setup.exe');
  assert.equal(assetName('portable', '0.12.0', 'x64'), 'AgentBox-0.12.0-x64-portable.exe');
});

test('parseSums reads sha256sum and shasum output, text and binary mode', () => {
  const a = 'a'.repeat(64);
  const b = 'B'.repeat(64);
  const sums = parseSums(`${a}  AgentBox-0.12.0-x86_64.AppImage\n${b} *AgentBox-0.12.0-x64-setup.exe\r\nnonsense\n`);
  assert.equal(sums.get('AgentBox-0.12.0-x86_64.AppImage'), a);
  assert.equal(sums.get('AgentBox-0.12.0-x64-setup.exe'), 'b'.repeat(64));
  assert.equal(sums.size, 2);
});

test('a portable .exe is replaced in place, with progress, after its checksum matches', async () => {
  const dir = tmp();
  const image = join(dir, 'AgentBox-0.11.0-x64-portable.exe');
  writeFileSync(image, 'old', { mode: 0o755 });
  const body = Buffer.alloc(3 << 20, 7); // a few progress reports' worth
  const rel = release('0.12.0', { 'AgentBox-0.12.0-x64-portable.exe': body, 'AgentBox-0.12.0-amd64.deb': 'deb' });
  const progress: AppUpdateProgress[] = [];
  const next = await update({ release: rel, install: { kind: 'portable', path: image }, arch: 'x64', tmp: join(dir, 'tmp'), fetch: fetcher, run: noRun, onProgress: (p) => progress.push(p) });
  assert.deepEqual(next, { relaunch: image });
  assert.ok(readFileSync(image).equals(body));
  assert.equal(statSync(image).mode & 0o111, 0o111);
  // The old one is renamed aside, to be removed at the next start.
  assert.deepEqual(readdirSync(dir).filter((f) => !f.includes('.old-')), ['AgentBox-0.11.0-x64-portable.exe']);
  assert.equal(readdirSync(dir).filter((f) => f.includes('.old-')).length, 1);
  const downloads = progress.filter((p) => p.phase === 'download');
  assert.ok(downloads.length > 1, 'the download reports progress as it goes');
  assert.deepEqual(downloads.at(-1), { phase: 'download', version: '0.12.0', received: body.length, total: body.length });
  assert.deepEqual(
    progress.filter((p) => p.phase !== 'download').map((p) => p.phase),
    ['verify', 'install'],
  );
  rmSync(dir, { recursive: true });
});

test('a download that doesn\'t match SHA256SUMS is thrown away, and the app is left as it was', async () => {
  const dir = tmp();
  const image = join(dir, 'AgentBox.exe');
  writeFileSync(image, 'old');
  const rel = release('0.12.1', { 'AgentBox-0.12.1-x64-portable.exe': 'tampered' }, { body: `${sha('the real one')}  AgentBox-0.12.1-x64-portable.exe\n` });
  await assert.rejects(
    update({ release: rel, install: { kind: 'portable', path: image }, arch: 'x64', tmp: join(dir, 'tmp'), fetch: fetcher, run: noRun, onProgress: () => {} }),
    (err: UpdateError) => err.code === 'checksum',
  );
  assert.equal(readFileSync(image, 'utf8'), 'old');
  assert.deepEqual(readdirSync(dir), ['AgentBox.exe']);
  rmSync(dir, { recursive: true });
});

test('a download larger than the release lists is cut off', async () => {
  const dir = tmp();
  const image = join(dir, 'AgentBox.exe');
  writeFileSync(image, 'old');
  const rel = release('0.12.2', { 'AgentBox-0.12.2-x64-portable.exe': 'a longer file than listed' });
  rel.assets![0].size = 4;
  await assert.rejects(
    update({ release: rel, install: { kind: 'portable', path: image }, arch: 'x64', tmp: join(dir, 'tmp'), fetch: fetcher, run: noRun, onProgress: () => {} }),
    (err: UpdateError) => err.code === 'checksum',
  );
  assert.equal(readFileSync(image, 'utf8'), 'old');
  assert.deepEqual(readdirSync(dir), ['AgentBox.exe']);
  rmSync(dir, { recursive: true });
});

test('an AppImage is not updated here: electron-updater does it (appimageupdate.ts)', async () => {
  const rel = release('0.12.6', {});
  await assert.rejects(
    update({ release: rel, install: { kind: 'appimage', path: '/nowhere/AgentBox.AppImage' }, arch: 'x64', tmp: '/nowhere', fetch: fetcher, run: noRun, onProgress: () => {} }),
    (err: UpdateError) => err.code === 'unsupported',
  );
});

test('what stops an update before anything is downloaded', async () => {
  const install = { kind: 'portable' as const, path: '/nowhere/AgentBox.exe' };
  const go = (rel: Release, arch = 'x64') => update({ release: rel, install, arch, tmp: '/nowhere', fetch: fetcher, run: noRun, onProgress: () => {} });
  const code = (want: string) => (err: UpdateError) => err.code === want;
  // GitHub couldn't be reached: the daemon's last find has no files.
  await assert.rejects(go({ version: '0.12.3', url: 'page' }), code('noAssets'));
  // No arm64 portable .exe is published.
  await assert.rejects(go(release('0.12.3', { 'AgentBox-0.12.3-x64-portable.exe': 'x' }), 'arm64'), code('noBuild'));
  // Listed nowhere: nothing to check it against.
  await assert.rejects(go(release('0.12.4', { 'AgentBox-0.12.4-x64-portable.exe': 'x' }, { body: '' })), code('noChecksum'));
  // The asset itself is missing from the server.
  const rel = release('0.12.5', { 'AgentBox-0.12.5-x64-portable.exe': 'x' });
  files.delete('/0.12.5/AgentBox-0.12.5-x64-portable.exe');
  await assert.rejects(go(rel), code('download'));
});

// A fake Mac: ditto unpacks by copying a prepared bundle, codesign verifies
// unless told not to and reports a team.
function fakeMac(bundle: string, opts: { verifies?: boolean; teams?: Record<string, string>; spctl?: boolean } = {}) {
  const calls: string[] = [];
  const run: Run = async (cmd, args) => {
    calls.push(`${cmd} ${args.join(' ')}`);
    if (cmd === 'ditto') {
      cpSync(bundle, join(args[3], 'AgentBox.app'), { recursive: true });
      return '';
    }
    if (cmd === 'codesign' && args[0] === '--verify') {
      if (opts.verifies === false) throw new Error(`${args.at(-1)}: invalid signature (code or signature have been modified)`);
      return '';
    }
    if (cmd === 'codesign' && args[0] === '-dv') {
      const app = args.at(-1)!;
      const team = Object.entries(opts.teams ?? {}).find(([suffix]) => app.endsWith(suffix))?.[1] ?? 'not set';
      return `Executable=${app}/Contents/MacOS/AgentBox\nIdentifier=dev.agentbox.desktop\nSignature=adhoc\nTeamIdentifier=${team}`;
    }
    if (cmd === 'spctl') {
      if (opts.spctl === false) throw new Error('rejected');
      return 'accepted';
    }
    throw new Error(`unexpected ${cmd}`);
  };
  return { run, calls };
}

function makeBundle(at: string, version: string, id = 'dev.agentbox.desktop', marker = version): string {
  mkdirSync(join(at, 'Contents', 'MacOS'), { recursive: true });
  writeFileSync(
    join(at, 'Contents', 'Info.plist'),
    `<?xml version="1.0"?>\n<plist version="1.0"><dict>\n\t<key>CFBundleIdentifier</key>\n\t<string>${id}</string>\n\t<key>CFBundleShortVersionString</key>\n\t<string>${version}</string>\n</dict></plist>\n`,
  );
  writeFileSync(join(at, 'Contents', 'MacOS', 'AgentBox'), marker);
  return at;
}

const macZip = (version: string) => `AgentBox-${version}-mac-arm64.zip`;

test('a Mac app is unpacked beside the old one, checked, and swapped in; the old one is kept until the next start', async () => {
  const dir = tmp();
  const apps = join(dir, 'Applications');
  const app = makeBundle(join(apps, 'AgentBox.app'), '0.11.0');
  const fresh = makeBundle(join(dir, 'built', 'AgentBox.app'), '0.12.0');
  // A stable release keeps the Mac's sums apart, in SHA256SUMS-mac.
  const rel = release('0.12.0', { [macZip('0.12.0')]: 'zip bytes', 'AgentBox-0.12.0-x86_64.AppImage': 'img' }, { name: 'SHA256SUMS-mac' });
  files.set('/0.12.0/SHA256SUMS', `${sha('img')}  AgentBox-0.12.0-x86_64.AppImage\n`);
  rel.assets!.push({ name: 'SHA256SUMS', url: `${base}/0.12.0/SHA256SUMS`, size: 1 });
  const mac = fakeMac(fresh);
  const next = await update({ release: rel, install: { kind: 'mac', path: app }, arch: 'arm64', tmp: join(dir, 'updates'), fetch: fetcher, run: mac.run, onProgress: () => {} });
  assert.deepEqual(next, { relaunch: app });
  assert.equal(readFileSync(join(app, 'Contents', 'MacOS', 'AgentBox'), 'utf8'), '0.12.0');
  assert.deepEqual(readdirSync(apps).sort(), ['.AgentBox.app.old', 'AgentBox.app']);
  assert.deepEqual(readdirSync(join(dir, 'updates')), [], 'the .zip is removed once unpacked');
  assert.ok(mac.calls.some((c) => c.startsWith('codesign --verify --deep --strict ')));
  assert.ok(!mac.calls.some((c) => c.startsWith('spctl')), 'an ad hoc signed app has no team for Gatekeeper to check');

  cleanUp({ kind: 'mac', path: app });
  assert.deepEqual(readdirSync(apps), ['AgentBox.app']);
  rmSync(dir, { recursive: true });
});

test('a Mac app that fails a check is never put in place', async () => {
  const cases: { name: string; bundle: (at: string) => string; mac?: Parameters<typeof fakeMac>[1] }[] = [
    { name: 'a broken signature', bundle: (at) => makeBundle(at, '0.12.0'), mac: { verifies: false } },
    { name: 'another app', bundle: (at) => makeBundle(at, '0.12.0', 'com.example.other') },
    { name: 'another version', bundle: (at) => makeBundle(at, '0.11.9') },
    // A Developer ID signed app takes only its own team's update…
    { name: 'another team', bundle: (at) => makeBundle(at, '0.12.0'), mac: { teams: { 'Applications/AgentBox.app': 'TEAM1', '.update/AgentBox.app': 'TEAM2' } } },
    // …and only one Gatekeeper accepts.
    { name: 'Gatekeeper refusing', bundle: (at) => makeBundle(at, '0.12.0'), mac: { teams: { 'AgentBox.app': 'TEAM1' }, spctl: false } },
  ];
  for (const c of cases) {
    const dir = tmp();
    const apps = join(dir, 'Applications');
    const app = makeBundle(join(apps, 'AgentBox.app'), '0.11.0');
    const rel = release('0.12.0', { [macZip('0.12.0')]: 'zip bytes' });
    const mac = fakeMac(c.bundle(join(dir, 'built', 'AgentBox.app')), c.mac);
    await assert.rejects(
      update({ release: rel, install: { kind: 'mac', path: app }, arch: 'arm64', tmp: join(dir, 'updates'), fetch: fetcher, run: mac.run, onProgress: () => {} }),
      (err: UpdateError) => err.code === 'signature',
      c.name,
    );
    assert.equal(readFileSync(join(app, 'Contents', 'MacOS', 'AgentBox'), 'utf8'), '0.11.0', c.name);
    assert.deepEqual(readdirSync(apps), ['AgentBox.app'], `${c.name}: nothing left beside the app`);
    rmSync(dir, { recursive: true });
  }
});

test('the same team and Gatekeeper\'s assent let a Developer ID signed update in', async () => {
  const dir = tmp();
  const app = makeBundle(join(dir, 'Applications', 'AgentBox.app'), '0.11.0');
  const mac = fakeMac(makeBundle(join(dir, 'built', 'AgentBox.app'), '0.12.0'), { teams: { 'AgentBox.app': 'TEAM1' } });
  const rel = release('0.12.0', { [macZip('0.12.0')]: 'zip bytes' });
  await update({ release: rel, install: { kind: 'mac', path: app }, arch: 'arm64', tmp: join(dir, 'updates'), fetch: fetcher, run: mac.run, onProgress: () => {} });
  assert.ok(mac.calls.some((c) => c.startsWith('spctl --assess --type execute ')));
  assert.equal(readFileSync(join(app, 'Contents', 'MacOS', 'AgentBox'), 'utf8'), '0.12.0');
  rmSync(dir, { recursive: true });
});

test('a portable .exe is renamed aside, and the old one removed at the next start', async () => {
  const dir = tmp();
  const exe = join(dir, 'AgentBox-0.11.0-x64-portable.exe');
  writeFileSync(exe, 'old');
  const rel = release('0.12.0', { 'AgentBox-0.12.0-x64-portable.exe': 'new exe' });
  const next = await update({ release: rel, install: { kind: 'portable', path: exe }, arch: 'x64', tmp: join(dir, 'tmp'), fetch: fetcher, run: noRun, onProgress: () => {} });
  assert.deepEqual(next, { relaunch: exe });
  assert.equal(readFileSync(exe, 'utf8'), 'new exe');
  assert.equal(readdirSync(dir).filter((n) => n.startsWith('AgentBox-0.11.0-x64-portable.exe.old-')).length, 1);
  cleanUp({ kind: 'portable', path: exe });
  assert.deepEqual(readdirSync(dir), ['AgentBox-0.11.0-x64-portable.exe']);
  rmSync(dir, { recursive: true });
});

test('the Windows installer is downloaded, checked, and handed back to run', async () => {
  const dir = tmp();
  const rel = release('0.12.0', { 'AgentBox-0.12.0-x64-setup.exe': 'setup' });
  const next = await update({ release: rel, install: { kind: 'nsis', path: 'C:\\AgentBox' }, arch: 'x64', tmp: join(dir, 'updates'), fetch: fetcher, run: noRun, onProgress: () => {} });
  assert.deepEqual(next, { installer: join(dir, 'updates', 'AgentBox-0.12.0-x64-setup.exe') });
  assert.equal(readFileSync(join(dir, 'updates', 'AgentBox-0.12.0-x64-setup.exe'), 'utf8'), 'setup');
  rmSync(dir, { recursive: true });
});

test('swap puts the old one back when the new one can\'t go in', () => {
  const dir = tmp();
  const target = join(dir, 'AgentBox.app');
  makeBundle(target, '0.11.0');
  // fresh doesn't exist: the second rename fails.
  assert.throws(() => swap(join(dir, 'missing.app'), target, join(dir, '.AgentBox.app.old')), (err: UpdateError) => err.code === 'install');
  assert.ok(existsSync(join(target, 'Contents', 'Info.plist')));
  assert.ok(!existsSync(join(dir, '.AgentBox.app.old')));
  rmSync(dir, { recursive: true });
});

test('plistString reads a key of an XML Info.plist', () => {
  const plist = '<dict>\n\t<key>CFBundleShortVersionString</key>\n\t<string>0.12.0</string>\n\t<key>CFBundleIdentifier</key><string>dev.agentbox.desktop</string></dict>';
  assert.equal(plistString(plist, 'CFBundleShortVersionString'), '0.12.0');
  assert.equal(plistString(plist, 'CFBundleIdentifier'), 'dev.agentbox.desktop');
  assert.equal(plistString(plist, 'CFBundleName'), undefined);
});
