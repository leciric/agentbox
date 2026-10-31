// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { countEntries, essentialsSection, matches, parseQuery, search, splitEssentials, type SettingSection } from './settingsSearch.ts';

const none = () => null;

const sections: SettingSection[] = [
  {
    id: 'models',
    title: 'Models',
    description: '',
    scope: 'installation',
    groups: [
      {
        id: 'agents',
        title: 'New agents',
        entries: [
          { id: 'model', label: 'Model for new agents', keywords: 'claude opus sonnet', essential: true, render: none },
          { id: 'enforce', label: 'Enforce this model and context window', description: 'Holds the lead to the model above.', keywords: 'cheaper', modified: true, render: none },
        ],
      },
      { id: 'chats', title: 'Every chat', entries: [{ id: 'compact', label: 'Compact chats at', keywords: 'context tokens summarise', render: none }] },
    ],
  },
  {
    id: 'project:agentbox',
    title: 'agentbox',
    description: '',
    scope: 'project',
    groups: [{ id: 'advanced', title: 'Testing AgentBox itself', entries: [{ id: 'nesting', label: 'Nesting: agents run their own Incus', render: none }] }],
  },
];

const ids = (found: SettingSection[]) => found.flatMap((s) => s.groups.flatMap((g) => g.entries.map((e) => e.id)));

test('an empty query leaves every section as it is', () => {
  assert.equal(search(sections, '  '), sections);
});

test('every word has to match, in any order and any case', () => {
  assert.deepEqual(ids(search(sections, 'CONTEXT compact')), ['compact']);
  assert.deepEqual(ids(search(sections, 'context')), ['enforce', 'compact']);
});

test("a word matches the setting's section and group too", () => {
  assert.deepEqual(ids(search(sections, 'agentbox incus')), ['nesting']);
  assert.deepEqual(ids(search(sections, 'new agents opus')), ['model']);
});

test('a setting is found by its description, and Advanced ones like any other', () => {
  assert.deepEqual(ids(search(sections, 'holds the lead')), ['enforce']);
  assert.deepEqual(ids(search(sections, 'cheaper')), ['enforce']);
});

test('@changed finds only settings moved off their default, and narrows words', () => {
  assert.deepEqual(ids(search(sections, '@changed')), ['enforce']);
  assert.deepEqual(ids(search(sections, '@modified compact')), []);
  assert.deepEqual(parseQuery('@Changed  model'), { words: ['model'], changed: true });
});

test('apostrophes and accents are ignored', () => {
  const s = sections[0];
  const g = { id: 'g', title: 'Keep', entries: [] };
  assert.ok(matches(parseQuery('agents media'), s, g, { id: 'm', label: 'Keep a removed agent’s media for', render: none }));
  assert.ok(matches(parseQuery('resume'), s, g, { id: 'r', label: 'Résumé', render: none }));
});

test('nothing found is no sections at all', () => {
  assert.deepEqual(search(sections, 'kubernetes'), []);
});

test('splitEssentials parts each group, dropping a side left empty', () => {
  const { essentials, advanced } = splitEssentials([
    ...sections[0].groups,
    { id: 'only', title: 'Only essential', entries: [{ id: 'x', label: 'x', essential: true, render: none }] },
  ]);
  const shape = (groups: SettingSection['groups']) => groups.map((g) => [g.id, g.entries.map((e) => e.id)]);
  assert.deepEqual(shape(essentials), [
    ['agents', ['model']],
    ['only', ['x']],
  ]);
  assert.deepEqual(shape(advanced), [
    ['agents', ['enforce']],
    ['chats', ['compact']],
  ]);
});

test('essentialsSection gathers essential settings under their groups, in section order', () => {
  const essentials = essentialsSection(sections, 'Essentials', '');
  assert.equal(essentials.id, 'essentials');
  assert.deepEqual(
    essentials.groups.map((g) => [g.id, g.entries.map((e) => e.id)]),
    [['models.agents', ['model']]],
  );
});

test('countEntries counts across groups', () => {
  assert.equal(countEntries(sections[0]), 3);
});
