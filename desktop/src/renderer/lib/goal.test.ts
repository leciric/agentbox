import assert from 'node:assert/strict';
import { test } from 'node:test';
import { budgetOK, draftOf, emptyGoal, specOf } from './goal.ts';

test('a goal draft becomes the spec the API takes', () => {
  assert.equal(specOf({ ...emptyGoal, objective: '  ' }), undefined);
  assert.deepEqual(specOf({ objective: ' Ship it ', prOpen: true, ciGreen: true, commands: 'go test ./...\n\n  npm test  ', budget: '5' }), {
    objective: 'Ship it',
    checks: [{ kind: 'pr_open' }, { kind: 'ci_green' }, { kind: 'command', command: 'go test ./...' }, { kind: 'command', command: 'npm test' }],
    budget: 5,
  });
  assert.deepEqual(specOf({ ...emptyGoal, objective: 'x' }), { objective: 'x', checks: undefined, budget: undefined });
});

test('a goal the agent has fills the fields; a tool goal only its objective', () => {
  const d = draftOf({
    objective: 'green', source: 'agentbox', status: 'active', checked: 1, budget: 7, updatedAt: '',
    checks: [{ kind: 'ci_green', met: false }, { kind: 'command', command: 'make check' }],
  });
  assert.deepEqual(d, { objective: 'green', prOpen: false, ciGreen: true, commands: 'make check', budget: '7' });
  assert.deepEqual(draftOf({ objective: 'mine', source: 'tool', status: 'active', checked: 0, updatedAt: '' }), { ...emptyGoal, objective: 'mine' });
  assert.deepEqual(draftOf({ objective: 'gone', source: 'agentbox', status: 'cleared', checked: 0, updatedAt: '' }), emptyGoal);
});

test('the budget is empty or 1 to 50', () => {
  for (const ok of ['', ' ', '1', '50']) assert.equal(budgetOK({ ...emptyGoal, budget: ok }), true, ok);
  for (const bad of ['0', '51', '2.5', '-1', 'ten']) assert.equal(budgetOK({ ...emptyGoal, budget: bad }), false, bad);
});
