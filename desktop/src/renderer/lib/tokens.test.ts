// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { humanTokens, limitTone, share, usd, windowNow } from './tokens.ts';

test('humanTokens picks the largest unit that keeps a number readable', () => {
  assert.equal(humanTokens(999), '999');
  assert.equal(humanTokens(12_400), '12.4K');
  assert.equal(humanTokens(12_400_000), '12.4M');
  assert.equal(humanTokens(1_300_000_000), '1.30B');
});

test('usd shows a dash for nothing, and a floor for anything below a cent', () => {
  assert.equal(usd(0), '—');
  assert.equal(usd(0.004), '<$0.01');
  assert.equal(usd(1.2), '$1.20');
  assert.equal(usd(2500), '$2.5K');
});

test('share never says 0% for something that did happen', () => {
  assert.equal(share(0, 0), '0%');
  assert.equal(share(1, 1000), '<1%');
  assert.equal(share(500, 1000), '50%');
});

test('share never rounds a shortfall up to 100%', () => {
  assert.equal(share(996, 1000), '99.6%');
  assert.equal(share(1000, 1000), '100%');
});

test('windowNow reports the utilization while the window has not reset', () => {
  const now = Date.parse('2026-01-01T00:00:00Z');
  assert.equal(windowNow({ utilization: 0.4, resetsAt: '2026-01-02T00:00:00Z' }, now), 0.4);
});

test('windowNow is null once the reset has passed', () => {
  const now = Date.parse('2026-01-02T00:00:00Z');
  assert.equal(windowNow({ utilization: 0.4, resetsAt: '2026-01-01T00:00:00Z' }, now), null);
});

test('windowNow treats an unparsable reset as still running', () => {
  const now = Date.parse('2026-01-01T00:00:00Z');
  assert.equal(windowNow({ utilization: 0.4, resetsAt: 'not-a-date' }, now), 0.4);
});

test('limitTone thresholds match the top bar', () => {
  assert.equal(limitTone(null), 'ok');
  assert.equal(limitTone(0.5), 'ok');
  assert.equal(limitTone(0.7), 'warn');
  assert.equal(limitTone(0.9), 'high');
});
