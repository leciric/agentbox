import assert from 'node:assert/strict';
import { test } from 'node:test';
import { busy } from './keepAwake.ts';

const agent = (ref: string, state: string, chat?: string) => ({ ref, state, chat });
const none = new Map<string, string | undefined>();

test('nothing running is not busy', () => {
  assert.equal(busy([], none, []), false);
  assert.equal(busy([agent('p/a', 'running', 'ready')], new Map([['p', 'ready']]), [{ status: 'succeeded' }]), false);
});

test('an agent mid-turn, a lead mid-turn or a running job is busy', () => {
  assert.equal(busy([agent('p/a', 'running', 'running')], none, []), true);
  assert.equal(busy([], new Map([['p', 'running']]), []), true);
  assert.equal(busy([], none, [{ status: 'running' }]), true);
});

test('an agent whose machine is not running is not busy', () => {
  assert.equal(busy([agent('p/a', 'stopped', 'running')], none, []), false);
});

test('waiting on a permission or a question does not hold it', () => {
  assert.equal(busy([agent('p/a', 'running', 'waiting')], new Map([['p', 'waiting']]), []), false);
  assert.equal(busy([agent('p/a', 'running', 'running')], none, [], new Set(['p/a'])), false);
  assert.equal(busy([agent('p/a', 'running', 'running'), agent('p/b', 'running', 'running')], none, [], new Set(['p/a'])), true);
});

test('a stalled turn does not hold it', () => {
  assert.equal(busy([{ ref: 'p/a', state: 'running', chat: 'running', stalledSince: '2026-10-21T10:00:00Z' }], none, []), false);
});
