import assert from 'node:assert/strict';
import { test } from 'node:test';
import { withEarlier, withTail } from './backgroundOutput.ts';

test('a later read of the end adds only what is new, by bytes', () => {
  let buf = withTail(undefined, { output: 'é1\n', outputStart: 10, outputSize: 14 });
  assert.deepEqual(buf, { start: 10, end: 14, size: 14, text: 'é1\n' });
  buf = withTail(buf, { output: '1\né2\n', outputStart: 12, outputSize: 18 });
  assert.deepEqual(buf, { start: 10, end: 18, size: 18, text: 'é1\né2\n' });
  assert.equal(withTail(buf, { output: 'é2\n', outputStart: 14, outputSize: 18 }), buf);
});

test('a read that does not join what is held starts over', () => {
  const buf = withTail(undefined, { output: 'abc', outputStart: 0, outputSize: 3 });
  assert.deepEqual(withTail(buf, { output: 'xyz', outputStart: 100, outputSize: 103 }), { start: 100, end: 103, size: 103, text: 'xyz' });
  assert.deepEqual(withTail(buf, { output: 'a', outputStart: 0, outputSize: 1 }), { start: 0, end: 1, size: 1, text: 'a' });
});

test('an earlier piece goes in front when it ends where the held output starts', () => {
  const buf = withTail(undefined, { output: 'def', outputStart: 3, outputSize: 6 });
  assert.deepEqual(withEarlier(buf, { output: 'abc', outputStart: 0 }), { start: 0, end: 6, size: 6, text: 'abcdef' });
  assert.equal(withEarlier(buf, { output: 'ab', outputStart: 0 }), buf);
});
