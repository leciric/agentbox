// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { nextRadio, sectionChanges } from './sectionEdit.ts';

const opened = { name: 'Work', description: 'Shop apps', color: 'sky' };

test('sectionChanges sends nothing when the user changed nothing', () => {
  assert.deepEqual(sectionChanges(opened, { ...opened }), {});
});

test('sectionChanges sends only what the user changed, not what the section became meanwhile', () => {
  // The user only recoloured; the live section was renamed elsewhere meanwhile,
  // which the dialog never learned of, so the name isn't in the request.
  assert.deepEqual(sectionChanges(opened, { ...opened, color: 'mint' }), { color: 'mint' });
  assert.deepEqual(sectionChanges(opened, { ...opened, name: ' Play ' }), { name: 'Play' });
});

test('sectionChanges lets the user clear the description and the colour', () => {
  assert.deepEqual(sectionChanges(opened, { ...opened, description: '  ', color: '' }), { description: '', color: '' });
});

test('sectionChanges ignores an emptied name rather than sending it', () => {
  assert.deepEqual(sectionChanges(opened, { ...opened, name: '   ' }), {});
});

test('nextRadio moves with the arrows, wrapping, and with Home and End', () => {
  assert.equal(nextRadio('ArrowRight', 0, 6), 1);
  assert.equal(nextRadio('ArrowDown', 5, 6), 0);
  assert.equal(nextRadio('ArrowLeft', 0, 6), 5);
  assert.equal(nextRadio('ArrowUp', 3, 6), 2);
  assert.equal(nextRadio('Home', 4, 6), 0);
  assert.equal(nextRadio('End', 1, 6), 5);
  assert.equal(nextRadio('Tab', 1, 6), undefined);
  assert.equal(nextRadio('ArrowRight', 0, 0), undefined);
});
