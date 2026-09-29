// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isNightly, isUpgrade } from './nightly.ts';

test('a nightly is <release>-nightly.<date>.<run>', () => {
  assert.equal(isNightly('0.11.0-nightly.20260929.12'), true);
  assert.equal(isNightly('v0.11.0-nightly.20260929.12'), true);
  assert.equal(isNightly('0.11.0'), false);
  assert.equal(isNightly('0.11.0-rc.1'), false);
  assert.equal(isNightly('preview'), false);
  assert.equal(isNightly(undefined), false);
});

test('an offer is an upgrade only when it is newer', () => {
  assert.equal(isUpgrade('0.11.0', '0.10.0'), true);
  assert.equal(isUpgrade('0.11.0', '0.11.0-nightly.20260929.12'), true);
  assert.equal(isUpgrade('0.11.0-nightly.20260930.2', '0.11.0-nightly.20260929.12'), true);
  assert.equal(isUpgrade('0.11.0-nightly.20260929.13', '0.11.0-nightly.20260929.12'), true);
  assert.equal(isUpgrade('0.11.0-nightly.20260929.12', '0.10.0'), true);
  // Back from a nightly to the latest stable, which is lower.
  assert.equal(isUpgrade('0.10.0', '0.11.0-nightly.20260929.12'), false);
  assert.equal(isUpgrade('0.10.0', '0.10.0'), false);
});
