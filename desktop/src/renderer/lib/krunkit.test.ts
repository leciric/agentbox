// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { krunkitInstall, krunkitPlan, offersRemake } from './krunkit.ts';

test('no check, no plan: not a Mac with Apple Silicon', () => {
  assert.equal(krunkitPlan(undefined), null);
  assert.equal(krunkitPlan(null), null);
});

test('installed krunkit is ready', () => {
  assert.deepEqual(krunkitPlan({ available: true }), { kind: 'ready' });
});

test('Homebrew installs a missing krunkit', () => {
  assert.deepEqual(krunkitPlan({ available: false, missing: 'krunkit', brew: true }), { kind: 'brew', missing: 'krunkit', command: krunkitInstall });
});

test('Homebrew upgrades its Lima for a missing driver', () => {
  assert.deepEqual(krunkitPlan({ available: false, missing: 'driver', brew: true }), { kind: 'brew', missing: 'driver', command: 'brew upgrade lima' });
});

test('without Homebrew, the commands to run by hand', () => {
  assert.deepEqual(krunkitPlan({ available: false, missing: 'krunkit' }), { kind: 'manual', missing: 'krunkit', command: krunkitInstall });
  assert.deepEqual(krunkitPlan({ available: false, missing: 'driver' }), { kind: 'manual', missing: 'driver', command: '' });
});

test('Settings offers a remake only for an existing VM made without krunkit', () => {
  const krunkit = { available: false, missing: 'krunkit' as const, brew: true };
  assert.equal(offersRemake({ exists: true, vmType: 'vz', krunkit }), true);
  assert.equal(offersRemake({ exists: true, vmType: 'vz', krunkit: { available: true } }), true);
  assert.equal(offersRemake({ exists: true, vmType: 'krunkit', krunkit }), false);
  assert.equal(offersRemake({ exists: false, krunkit }), false);
  assert.equal(offersRemake({ exists: true, vmType: 'vz' }), false);
  assert.equal(offersRemake(null), false);
});
