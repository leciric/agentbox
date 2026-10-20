// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { recordedFor, videoAt } from './recording.ts';

const meta = { duration: 4, recorded: 20, cuts: [{ from: 2, to: 10 }, { from: 12, to: 20 }] };

test('videoAt takes out what was cut before the moment', () => {
  assert.equal(videoAt(meta, 1), 1);
  assert.equal(videoAt(meta, 10), 2);
  assert.equal(videoAt(meta, 11), 3);
  assert.equal(videoAt(meta, 20), 4);
});

test('videoAt puts a moment inside a cut where the cut is', () => {
  assert.equal(videoAt(meta, 6), 2);
  assert.equal(videoAt(meta, 15), 4);
});

test('an untrimmed recording keeps the wall clock', () => {
  assert.equal(videoAt({}, 7.5), 7.5);
  assert.equal(videoAt(undefined, 3), 3);
  assert.equal(recordedFor({ duration: 9 }), 9);
  assert.equal(recordedFor(meta), 20);
  assert.equal(recordedFor(undefined), 0);
});
