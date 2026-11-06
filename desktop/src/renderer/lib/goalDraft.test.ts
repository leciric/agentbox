import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { draftFromGoal, emptyGoalDraft, specFromDraft } from './goalDraft.ts';

test('a goal draft is sent as its spec, and a goal read back is the same draft', () => {
  assert.equal(specFromDraft(emptyGoalDraft), undefined);
  assert.equal(specFromDraft({ ...emptyGoalDraft, objective: '  ', prOpen: true }), undefined);
  const spec = specFromDraft({ objective: ' Ship it ', prOpen: true, ciGreen: true, commands: 'npm test\n\n  go vet ./... ', budget: '99' });
  assert.deepEqual(spec, {
    objective: 'Ship it',
    checks: [{ kind: 'pr_open' }, { kind: 'ci_green' }, { kind: 'command', command: 'npm test' }, { kind: 'command', command: 'go vet ./...' }],
    budget: 50,
  });
  assert.deepEqual(specFromDraft({ ...emptyGoalDraft, objective: 'x', budget: '' }), { objective: 'x', checks: undefined, budget: undefined });

  const goal: T.AgentGoal = { objective: 'Ship it', source: 'agentbox', status: 'blocked', checked: 2, budget: 5, continuations: 5, updatedAt: '', checks: spec!.checks };
  assert.deepEqual(draftFromGoal(goal), { objective: 'Ship it', prOpen: true, ciGreen: true, commands: 'npm test\ngo vet ./...', budget: '5' });
  assert.deepEqual(draftFromGoal({ ...goal, status: 'cleared' }), emptyGoalDraft);
  assert.equal(draftFromGoal({ ...goal, budget: 10 }).budget, '');
});
