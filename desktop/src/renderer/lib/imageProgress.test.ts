// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { percentOf } from './imageProgress.ts';

test("the image wait's bar reads the download's percentage, and none otherwise", () => {
  assert.equal(percentOf('40% of 1843 MB'), 40);
  assert.equal(percentOf('100% of 1843 MB'), 100);
  assert.equal(percentOf('Joining the parts'), null);
  assert.equal(percentOf(undefined), null);
});
