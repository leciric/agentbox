// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { test } from 'node:test';
import type { AppUpdateState } from '../shared/appUpdate.ts';
import { AppUpdates, type Updater } from './appupdate.ts';

// FakeUpdater stands in for electron-updater's AppImageUpdater: the feed
// offers `offered`, downloading reports progress, and installing can fail the
// way electron-updater fails, with an error event.
class FakeUpdater extends EventEmitter implements Updater {
  autoDownload = true;
  autoInstallOnAppQuit = true;
  allowPrerelease = false;
  allowDowngrade = false;
  feeds: string[] = [];
  installed: [boolean | undefined, boolean | undefined][] = [];
  offered = '0.12.0';
  newer = true;
  active = true;
  downloadError?: Error;
  installError?: Error;

  setFeedURL(options: { provider: 'generic'; url: string }) {
    this.feeds.push(options.url);
  }
  async checkForUpdates() {
    if (!this.active) return null;
    return { isUpdateAvailable: this.newer || this.allowDowngrade, updateInfo: { version: this.offered } };
  }
  async downloadUpdate() {
    this.emit('download-progress', { percent: 12.5 });
    this.emit('download-progress', { percent: 12.9 });
    this.emit('download-progress', { percent: 100 });
    if (this.downloadError) throw this.downloadError;
    return ['/cache/AgentBox.AppImage'];
  }
  quitAndInstall(isSilent?: boolean, isForceRunAfter?: boolean) {
    this.installed.push([isSilent, isForceRunAfter]);
    if (this.installError) this.emit('error', this.installError);
  }
}

const release = { version: '0.12.0', url: 'https://github.com/leciric/agentbox/releases/tag/v0.12.0' };

function setup(version = '0.11.0') {
  const updater = new FakeUpdater();
  const published: AppUpdateState[] = [];
  const updates = new AppUpdates(updater, version, (s) => published.push(s));
  return { updater, published, updates };
}

test('it downloads the release the daemon names, from that release, and reports progress', async () => {
  const { updater, published, updates } = setup();
  assert.equal(updater.autoDownload, false);
  assert.equal(updater.autoInstallOnAppQuit, false);
  const state = await updates.download(release);
  assert.deepEqual(state, { state: 'ready', version: '0.12.0' });
  assert.deepEqual(updater.feeds, ['https://github.com/leciric/agentbox/releases/download/v0.12.0/']);
  assert.equal(updater.allowDowngrade, false);
  assert.deepEqual(published, [
    { state: 'downloading', version: '0.12.0', percent: 0 },
    { state: 'downloading', version: '0.12.0', percent: 12 },
    { state: 'downloading', version: '0.12.0', percent: 100 },
    { state: 'ready', version: '0.12.0' },
  ]);
});

test('a nightly going back to the stable channel may install a lower version, nothing else may', async () => {
  const nightly = setup('0.13.0-nightly.20261001.3');
  nightly.updater.newer = false;
  assert.equal((await nightly.updates.download(release)).state, 'ready');
  assert.equal(nightly.updater.allowDowngrade, true);

  const stable = setup('0.13.0');
  stable.updater.newer = false;
  const state = await stable.updates.download(release);
  assert.equal(state.state, 'failed');
  assert.equal(stable.updater.allowDowngrade, false);
});

test('nightly releases are fetched from their own tag', async () => {
  const { updater, updates } = setup('0.12.0-nightly.20261001.3');
  updater.offered = '0.12.0-nightly.20261002.1';
  const nightly = { version: '0.12.0-nightly.20261002.1', url: 'https://github.com/leciric/agentbox/releases/tag/v0.12.0-nightly.20261002.1' };
  assert.equal((await updates.download(nightly)).state, 'ready');
  assert.deepEqual(updater.feeds, ['https://github.com/leciric/agentbox/releases/download/v0.12.0-nightly.20261002.1/']);
});

test('what it refuses or fails at ends failed, with why', async () => {
  const cases: [string, (u: FakeUpdater) => void, { version: string; url: string }, RegExp][] = [
    ['not a release page', () => {}, { version: '0.12.0', url: 'https://example.com/agentbox' }, /isn't a release page/],
    ['already this version', () => {}, { version: '0.11.0', url: 'https://github.com/leciric/agentbox/releases/tag/v0.11.0' }, /already 0\.11\.0/],
    ['not an AppImage', (u) => (u.active = false), release, /not an AppImage/],
    ['the feed offers another version', (u) => (u.offered = '0.12.1'), release, /offers 0\.12\.1, not 0\.12\.0/],
    ['the download fails', (u) => (u.downloadError = new Error('sha512 checksum mismatch')), release, /sha512 checksum mismatch/],
  ];
  for (const [name, arrange, given, error] of cases) {
    const { updater, updates } = setup();
    arrange(updater);
    const state = await updates.download(given);
    assert.equal(state.state, 'failed', name);
    assert.match((state as { error: string }).error, error, name);
  }
});

test('a failed download can be tried again', async () => {
  const { updater, updates } = setup();
  updater.downloadError = new Error('network');
  assert.equal((await updates.download(release)).state, 'failed');
  updater.downloadError = undefined;
  assert.equal((await updates.download(release)).state, 'ready');
});

test('a second click while downloading, or once ready, starts nothing new', async () => {
  const { updater, updates } = setup();
  const first = updates.download(release);
  assert.equal((await updates.download(release)).state, 'downloading');
  await first;
  assert.equal((await updates.download(release)).state, 'ready');
  assert.equal(updater.feeds.length, 1);
});

test('install restarts into the update, silently, and only once it is ready', async () => {
  const { updater, updates } = setup();
  assert.deepEqual(updates.install(), { state: 'idle' });
  assert.equal(updater.installed.length, 0);
  await updates.download(release);
  assert.deepEqual(updates.install(), { state: 'installing', version: '0.12.0' });
  assert.deepEqual(updater.installed, [[true, true]]);
  assert.equal(updater.listenerCount('error'), 0);
});

test('an install electron-updater reports failed, by event, ends failed', async () => {
  const { updater, updates } = setup();
  await updates.download(release);
  updater.installError = new Error('EACCES: permission denied, unlink');
  const state = updates.install();
  assert.equal(state.state, 'failed');
  assert.match((state as { error: string }).error, /EACCES/);
  assert.equal(updater.listenerCount('error'), 0);
});
