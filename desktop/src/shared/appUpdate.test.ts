// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isPrerelease, releaseFeed } from './appUpdate.ts';

test("a release's feed is the directory its assets download from", () => {
  assert.equal(releaseFeed('https://github.com/leciric/agentbox/releases/tag/v0.12.0'), 'https://github.com/leciric/agentbox/releases/download/v0.12.0/');
  assert.equal(
    releaseFeed('https://github.com/leciric/agentbox/releases/tag/v0.12.0-nightly.20261001.3/'),
    'https://github.com/leciric/agentbox/releases/download/v0.12.0-nightly.20261001.3/',
  );
  assert.equal(releaseFeed('http://localhost:8080/releases/tag/v1.0.0?x=1'), 'http://localhost:8080/releases/download/v1.0.0/');
});

test('anything but a release page has no feed', () => {
  for (const page of ['', 'not a url', 'https://github.com/leciric/agentbox/releases', 'https://github.com/leciric/agentbox/releases/tag/', 'file:///releases/tag/v1.0.0']) {
    assert.equal(releaseFeed(page), undefined, page);
  }
});

test('a nightly is a prerelease', () => {
  assert.equal(isPrerelease('0.12.0-nightly.20261001.3'), true);
  assert.equal(isPrerelease('v0.12.0-nightly.20261001.3'), true);
  assert.equal(isPrerelease('0.12.0'), false);
});
