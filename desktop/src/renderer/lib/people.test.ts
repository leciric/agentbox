import assert from 'node:assert/strict';
import { test } from 'node:test';
import { initials, PERSON_COLORS, personColor } from './people.ts';
import { SESSION_COLORS } from './sessions.ts';

test('a person has one of the session hues, and no other', () => {
  assert.deepEqual([...PERSON_COLORS], [...SESSION_COLORS]);
});

test('a person keeps one colour for one name, whatever its case', () => {
  assert.equal(personColor({ name: 'Ana' }), personColor({ name: ' ana ' }));
  assert.ok((SESSION_COLORS as readonly string[]).includes(personColor({ name: 'Bob' })));
});

test('initials', () => {
  assert.equal(initials('Ana'), 'A');
  assert.equal(initials("Alice's AgentBox"), 'AA');
  assert.equal(initials('bob-laptop'), 'BL');
  assert.equal(initials(''), '?');
});
