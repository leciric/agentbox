// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fitCount, meterPercent } from './meter.ts';

test('an unreported total draws no bar, not a full one', () => {
  assert.equal(meterPercent(0, 0), undefined);
  assert.equal(meterPercent(5, 0), undefined);
  assert.equal(meterPercent(NaN, 10), undefined);
  assert.equal(meterPercent(1, NaN), undefined);
});

test('nothing used draws an empty bar, with no stub', () => {
  assert.equal(meterPercent(0, 32), 0);
});

test('a bar is the share used, kept between empty and full', () => {
  assert.equal(meterPercent(8, 32), 25);
  assert.equal(meterPercent(40, 32), 100);
  assert.equal(meterPercent(-1, 32), 0);
});

test('every chip shows when the row has room, with no more button', () => {
  assert.equal(fitCount([50, 50, 50], 170, 10, 40), 3);
  assert.equal(fitCount([], 100, 10, 40), 0);
});

test('chips that would wrap go behind the more button, which keeps its room', () => {
  // 50+10+50+10+40 = 160 fits; a third chip would take it to 220.
  assert.equal(fitCount([50, 50, 50, 50], 170, 10, 40), 2);
  assert.equal(fitCount([200, 50], 170, 10, 40), 0);
});
