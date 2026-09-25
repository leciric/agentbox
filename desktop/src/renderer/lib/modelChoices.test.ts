// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { choiceName, groupChoices, isRecommended, matchesQuery, unavailableValue } from './modelChoices.ts';

const choice = (value: string, name: string, group?: string, description?: string): T.ChatOptionChoice => ({ value, name, group, description });

test('groupChoices keeps ungrouped choices in one unnamed group', () => {
  const groups = groupChoices([choice('a', 'A'), choice('b', 'B')]);
  assert.deepEqual(
    groups.map((g) => g.name),
    [''],
  );
  assert.equal(groups[0].choices.length, 2);
});

test('groupChoices starts a new group only when the heading changes', () => {
  const groups = groupChoices([choice('a', 'A', 'X'), choice('b', 'B', 'X'), choice('c', 'C', 'Y'), choice('d', 'D', 'X')]);
  assert.deepEqual(
    groups.map((g) => g.name),
    ['X', 'Y', 'X'],
  );
  assert.equal(groups[0].choices.length, 2);
});

test('isRecommended marks the "default" value or a name that says recommended', () => {
  assert.equal(isRecommended(choice('default', 'Default (recommended)')), true);
  assert.equal(isRecommended(choice('opus', 'Opus, recommended for hard tasks')), true);
  assert.equal(isRecommended(choice('sonnet', 'Sonnet')), false);
});

test('choiceName strips a trailing "(recommended)"', () => {
  assert.equal(choiceName(choice('default', 'Default (recommended)')), 'Default');
  assert.equal(choiceName(choice('sonnet', 'Sonnet')), 'Sonnet');
});

test('matchesQuery checks the name, description and value', () => {
  const c = choice('claude-opus', 'Opus', undefined, 'Best for hard problems');
  assert.equal(matchesQuery(c, ''), true);
  assert.equal(matchesQuery(c, 'opus'), true);
  assert.equal(matchesQuery(c, 'HARD'), true);
  assert.equal(matchesQuery(c, 'claude-opus'), true);
  assert.equal(matchesQuery(c, 'haiku'), false);
});

test('unavailableValue is undefined for no choice, or one still on the menu', () => {
  const choices = [choice('a', 'A'), choice('b', 'B')];
  assert.equal(unavailableValue(choices, ''), undefined);
  assert.equal(unavailableValue(choices, 'a'), undefined);
  assert.equal(unavailableValue(choices, 'gone'), 'gone');
});
