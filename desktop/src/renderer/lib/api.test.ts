// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import { agentPath, isProjectChat } from './api.ts';

test('isProjectChat is true for a bare project ref and for the lead', () => {
  assert.equal(isProjectChat('myproject'), true);
  assert.equal(isProjectChat(`myproject/${T.LeadName}`), true);
});

test('isProjectChat is false for an agent ref', () => {
  assert.equal(isProjectChat('myproject/agent-01'), false);
});

test('agentPath encodes the project and agent into the API path', () => {
  assert.equal(agentPath('myproject/agent-01'), '/v1/agents/myproject/agent-01');
  assert.equal(agentPath('my project/agent 01'), '/v1/agents/my%20project/agent%2001');
});
