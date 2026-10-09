// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { openLatestRelease } from './releaseLink.ts';

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
