// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { lifecycleActions, usesChat } from './agentActions.ts';

test('a running agent can be paused or stopped', () => {
  assert.deepEqual(lifecycleActions('running'), ['pause', 'stop']);
});

test('a paused agent can be resumed or stopped', () => {
  assert.deepEqual(lifecycleActions('paused'), ['resume', 'stop']);
});

test('a stopped agent can only be started', () => {
  assert.deepEqual(lifecycleActions('stopped'), ['start']);
});

test('an agent still being created offers nothing to do yet', () => {
  assert.deepEqual(lifecycleActions('incomplete'), []);
});

test('only a running or paused agent can be stopped', () => {
  for (const state of ['running', 'paused']) assert.ok(lifecycleActions(state).includes('stop'), state);
  for (const state of ['stopped', 'incomplete', 'missing']) assert.ok(!lifecycleActions(state).includes('stop'), state);
});

test('chat is only offered to an agent with a chat interface and an AI tool', () => {
  assert.equal(usesChat({ ai: 'claude', interface: 'chat' }), true);
  assert.equal(usesChat({ ai: 'none', interface: 'chat' }), false);
  assert.equal(usesChat({ ai: 'claude', interface: 'terminal' }), false);
});
