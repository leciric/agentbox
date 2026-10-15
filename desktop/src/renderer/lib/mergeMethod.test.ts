import assert from 'node:assert/strict';
import { test } from 'node:test';
import { defaultMethod, withPick } from './mergeMethod.ts';

const all = ['merge', 'squash', 'rebase'];

test('the first allowed method when nothing is remembered', () => {
  assert.equal(defaultMethod(all, {}, 'p'), 'merge');
});

test('the project, then the global method', () => {
  const prefs = { global: 'squash', projects: { p: 'rebase' } };
  assert.equal(defaultMethod(all, prefs, 'p'), 'rebase');
  assert.equal(defaultMethod(all, prefs, 'other'), 'squash');
});

test('a remembered method the repository no longer allows falls back', () => {
  assert.equal(defaultMethod(['squash', 'rebase'], { global: 'merge', projects: { p: 'merge' } }, 'p'), 'squash');
  assert.equal(defaultMethod(['squash', 'rebase'], { global: 'rebase', projects: { p: 'merge' } }, 'p'), 'rebase');
});

test('the first pick is also the global default; later picks only the project', () => {
  const first = withPick({}, 'a', 'squash');
  assert.deepEqual(first, { global: 'squash', projects: { a: 'squash' } });
  assert.deepEqual(withPick(first, 'b', 'rebase'), { global: 'squash', projects: { a: 'squash', b: 'rebase' } });
});
