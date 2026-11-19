// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { begin, isBusy, recentMs, reset, setClock, slowMs, update } from './daemonBusy.ts';

let clock = 0;
setClock(() => clock);
afterEach(() => {
  reset();
  clock = 0;
});

test('a request out past slowMs makes the daemon busy, and its answer clears it', () => {
  const done = begin();
  assert.equal(isBusy(), false, 'busy the moment it was sent');
  clock = slowMs + 1;
  update();
  assert.equal(isBusy(), true);
  done();
  assert.equal(isBusy(), false);
});

test('a quick request never marks it', () => {
  const done = begin();
  clock = slowMs - 1;
  done();
  update();
  assert.equal(isBusy(), false);
});

test('a timeout keeps it busy for recentMs after', () => {
  const done = begin();
  clock = 30_000;
  done(true);
  assert.equal(isBusy(), true);
  clock += recentMs - 1;
  update();
  assert.equal(isBusy(), true);
  clock += 2;
  update();
  assert.equal(isBusy(), false);
});
