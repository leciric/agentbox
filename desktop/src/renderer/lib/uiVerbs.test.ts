import assert from 'node:assert/strict';
import { test } from 'node:test';
import { asVerbView, verbOf } from './uiVerbs.ts';

test('each view lights its verb', () => {
  assert.equal(verbOf({ kind: 'home' }), 'ask');
  assert.equal(verbOf({ kind: 'project', project: 'p' }), 'ask');
  assert.equal(verbOf({ kind: 'agent', ref: 'p/agent-1' }), 'ask');
  assert.equal(verbOf({ kind: 'watch', ref: 'p/agent-1' }), 'watch');
  assert.equal(verbOf({ kind: 'review', tab: 'proof' }), 'review');
  assert.equal(verbOf({ kind: 'settings' }), 'settings');
});

test('places that moved open where they are now', () => {
  assert.deepEqual(asVerbView({ kind: 'work', project: 'p', filter: 'review' }), { kind: 'review', tab: 'work', project: 'p', filter: 'review' });
  assert.deepEqual(asVerbView({ kind: 'media' }), { kind: 'review', tab: 'proof' });
  assert.deepEqual(asVerbView({ kind: 'agent', ref: 'p/a', tab: 'terminal' }), { kind: 'watch', ref: 'p/a', place: 'terminal' });
  assert.deepEqual(asVerbView({ kind: 'agent', ref: 'p/a', tab: 'chat' }), { kind: 'agent', ref: 'p/a', tab: 'chat' });
  assert.deepEqual(asVerbView({ kind: 'project', project: 'p' }), { kind: 'project', project: 'p' });
});
