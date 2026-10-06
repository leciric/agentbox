// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { AppUpdateState } from '../../shared/appUpdate.ts';
import { openLatestRelease, updateOrOpen } from './releaseLink.ts';

const pinned = 'https://downloads.agentbox.linting.dev/releases/v0.9.0/index.html';
const latest = 'https://downloads.agentbox.linting.dev/releases/v0.10.0/index.html';

test('the link opens the latest release, not the one the last check pinned', async () => {
  const opened: string[] = [];
  await openLatestRelease(async () => ({ url: latest }), (u) => opened.push(u), pinned);
  assert.deepEqual(opened, [latest]);
});

test('with the daemon unable to say, the pinned release', async () => {
  const opened: string[] = [];
  await openLatestRelease(
    async () => {
      throw new Error('finding the latest release: release list: 503 Service Unavailable');
    },
    (u) => opened.push(u),
    pinned,
  );
  assert.deepEqual(opened, [pinned]);
});

const release = { version: '0.10.0', url: latest };
const pinnedRelease = { version: '0.9.0', url: pinned };

function updater(supported: boolean, outcome: AppUpdateState | Error) {
  const downloads: { version: string; url: string }[] = [];
  return {
    downloads,
    supported: async () => supported,
    download: async (r: { version: string; url: string }) => {
      downloads.push(r);
      if (outcome instanceof Error) throw outcome;
      return outcome;
    },
  };
}

test('in an AppImage, the update downloads the latest release and opens nothing', async () => {
  const opened: string[] = [];
  const failures: string[] = [];
  const app = updater(true, { state: 'ready', version: '0.10.0' });
  await updateOrOpen(async () => release, app, (u) => opened.push(u), pinnedRelease, (e) => failures.push(e));
  assert.deepEqual(app.downloads, [release]);
  assert.deepEqual(opened, []);
  assert.deepEqual(failures, []);
});

test('anywhere else, the release page opens', async () => {
  const opened: string[] = [];
  const app = updater(false, { state: 'ready', version: '0.10.0' });
  await updateOrOpen(async () => release, app, (u) => opened.push(u), pinnedRelease, () => {});
  assert.deepEqual(app.downloads, []);
  assert.deepEqual(opened, [latest]);
});

test('a failed update says why and opens the release page instead', async () => {
  for (const outcome of [{ state: 'failed', version: '0.10.0', error: 'sha512 checksum mismatch' } as const, new Error('sha512 checksum mismatch')]) {
    const opened: string[] = [];
    const failures: string[] = [];
    await updateOrOpen(async () => release, updater(true, outcome), (u) => opened.push(u), pinnedRelease, (e) => failures.push(e));
    assert.deepEqual(opened, [latest]);
    assert.deepEqual(failures, ['sha512 checksum mismatch']);
  }
});

test('with the daemon unreachable, the pinned release is the one downloaded', async () => {
  const app = updater(true, { state: 'ready', version: '0.9.0' });
  await updateOrOpen(async () => Promise.reject(new Error('down')), app, () => {}, pinnedRelease, () => {});
  assert.deepEqual(app.downloads, [pinnedRelease]);
});
