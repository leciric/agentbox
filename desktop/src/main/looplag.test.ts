// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { lagMs, lagOf } from './looplag.ts';

const ns = (ms: number) => ms * 1e6;
const at = new Date('2026-11-06T12:00:00Z');

test('a window under lagMs is no lag', () => {
  assert.equal(lagOf({ max: ns(lagMs), percentile: () => ns(30) }, 5000, at), null);
  assert.equal(lagOf({ max: Number.NaN, percentile: () => 0 }, 5000, at), null);
});

test('a stall is reported in milliseconds', () => {
  assert.deepEqual(lagOf({ max: ns(1234.4), percentile: () => ns(250.6) }, 5000, at), {
    at: '2026-11-06T12:00:00.000Z',
    maxMs: 1234,
    p99Ms: 251,
    windowMs: 5000,
  });
});
