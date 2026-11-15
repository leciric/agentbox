// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { projectAgentDefaults, startingPick } from './agentPick.ts';

const project = (over: Partial<T.Project> = {}) =>
  ({ name: 'pawly', agentTool: '', agentModel: '', agentEffort: '', agentContextWindow: '', agentMode: '', ...over }) as T.Project;
const last: T.AgentPick = { ai: 'cursor', model: 'gpt-5', effort: 'high' };

test('a project with a tool starts on it, leaving the rest to the project', () => {
  assert.deepEqual(startingPick(project({ agentTool: 'opencode', agentModel: 'openrouter/kimi' }), last), { ai: 'opencode', model: '', effort: '' });
});

test('a project with no tool starts on the last pick', () => {
  assert.deepEqual(startingPick(project(), last), last);
});

test('with neither, Claude Code', () => {
  assert.deepEqual(startingPick(project(), undefined), { ai: 'claude', model: '', effort: '' });
  assert.deepEqual(startingPick(project(), { ai: '', model: '', effort: '' }), { ai: 'claude', model: '', effort: '' });
});

test("a project's defaults are its tool's alone", () => {
  const p = project({ agentModel: 'sonnet', agentEffort: 'low', agentContextWindow: '200000' });
  assert.deepEqual(projectAgentDefaults(p, 'claude'), { model: 'sonnet', effort: 'low', window: '200000' });
  assert.deepEqual(projectAgentDefaults(p, 'cursor'), { model: '', effort: '', window: '' });
  assert.equal(projectAgentDefaults(project({ agentModel: 'auto' }), 'claude').model, '');
});
