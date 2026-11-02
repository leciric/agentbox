// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { asAgent, pastState } from './pastAgents.ts';

const past: T.ArchivedAgent = {
  ref: 'pawly/agent-07',
  project: 'pawly',
  name: 'agent-07',
  title: 'Fix the login',
  task: 'fix it',
  ai: 'claude',
  interface: 'chat',
  branch: 'agentbox/fix-login',
  baseRef: 'main',
  baseCommit: 'abc123',
  createdAt: '2026-10-01T10:00:00Z',
  archivedAt: '2026-10-02T10:00:00Z',
  tokens: 1200,
  costUsd: 0.5,
};

test('asAgent keeps what the chat shows and gives it no machine to start', () => {
  const agent = asAgent(past);
  assert.equal(agent.ref, 'pawly/agent-07');
  assert.equal(agent.title, 'Fix the login');
  assert.equal(agent.branch, 'agentbox/fix-login');
  assert.equal(agent.state, pastState);
  assert.equal(agent.instance, '');
  assert.equal(agent.worktree, '');
});
