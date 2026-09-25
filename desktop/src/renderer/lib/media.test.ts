// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { clock, describeAll, kindInfo, mediaKinds } from './media.ts';

test('kindInfo finds the kind by name', () => {
  assert.equal(kindInfo('screenshot'), mediaKinds[0]);
  assert.equal(kindInfo('recording').kind, 'recording');
});

test('kindInfo falls back to the last kind for one it does not know', () => {
  assert.equal(kindInfo('bogus'), mediaKinds[mediaKinds.length - 1]);
});

test('describeAll pluralizes and names the kind and the agent', () => {
  assert.equal(describeAll(1, 'screenshot', 'agent-12'), 'all 1 screenshot of agent-12');
  assert.equal(describeAll(42, 'screenshot', 'agent-12'), 'all 42 screenshots of agent-12');
});

test('describeAll drops the agent clause when there is none', () => {
  assert.equal(describeAll(3, 'log', ''), 'all 3 logs');
});

test('describeAll falls back to "item" for no kind filter', () => {
  assert.equal(describeAll(2, '', ''), 'all 2 items');
  assert.equal(describeAll(1, '', ''), 'all 1 item');
});

test('clock formats seconds as m:ss', () => {
  assert.equal(clock(0), '0:00');
  assert.equal(clock(5), '0:05');
  assert.equal(clock(65), '1:05');
  assert.equal(clock(3599), '59:59');
});

test('clock never goes negative and rounds to the nearest second', () => {
  assert.equal(clock(-5), '0:00');
  assert.equal(clock(4.6), '0:05');
});
