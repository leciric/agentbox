// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isPrerelease, releaseFeed } from './appimageupdate.ts';

test("a release's feed is the directory its assets download from", () => {
  assert.equal(releaseFeed('https://downloads.agentbox.linting.dev/releases/v0.12.0/index.html'), 'https://downloads.agentbox.linting.dev/releases/v0.12.0/');
  assert.equal(
    releaseFeed('https://downloads.agentbox.linting.dev/releases/v0.12.0-nightly.20261001.3/index.html'),
    'https://downloads.agentbox.linting.dev/releases/v0.12.0-nightly.20261001.3/',
  );
  assert.equal(releaseFeed('http://localhost:8080/releases/v1.0.0/index.html?x=1'), 'http://localhost:8080/releases/v1.0.0/');
});

test('anything but a release page has no feed', () => {
  for (const page of [
    '',
    'not a url',
    'https://downloads.agentbox.linting.dev/releases.json',
    'https://downloads.agentbox.linting.dev/releases/index.html',
    'https://downloads.agentbox.linting.dev/releases/v1.0.0/',
    // GitHub's release pages: the private repository serves them to nobody else.
    'https://github.com/leciric/agentbox/releases/tag/v1.0.0',
    'file:///releases/v1.0.0/index.html',
  ]) {
    assert.equal(releaseFeed(page), undefined, page);
  }
});

test('a nightly is a prerelease', () => {
  assert.equal(isPrerelease('0.12.0-nightly.20261001.3'), true);
  assert.equal(isPrerelease('v0.12.0-nightly.20261001.3'), true);
  assert.equal(isPrerelease('0.12.0'), false);
});
