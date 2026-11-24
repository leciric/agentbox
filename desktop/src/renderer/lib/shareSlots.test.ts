import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseSlots } from './shareSlots.ts';

test('the slots field saves only a whole number from 0 to 64', () => {
  assert.equal(parseSlots('3'), 3);
  assert.equal(parseSlots(' 12 '), 12);
  assert.equal(parseSlots('0'), 0);
  assert.equal(parseSlots('64'), 64);
  // Emptied to type another number: nothing is saved, least of all 0.
  assert.equal(parseSlots(''), null);
  assert.equal(parseSlots('  '), null);
  assert.equal(parseSlots('65'), null);
  assert.equal(parseSlots('-1'), null);
  assert.equal(parseSlots('2.5'), null);
  assert.equal(parseSlots('1e1'), null);
});
