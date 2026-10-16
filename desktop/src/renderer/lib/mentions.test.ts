// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { matchAgents, matchFiles, matchProjects, mentionAt } from './mentions.ts';

test('mentionAt finds an @ at the start of the message', () => {
  assert.deepEqual(mentionAt('@comp', 5), { start: 0, query: 'comp' });
});

test('mentionAt finds an @ anywhere after whitespace', () => {
  assert.deepEqual(mentionAt('see @Compo', 10), { start: 4, query: 'Compo' });
});

test('mentionAt is undefined with no @, or one not at a word boundary', () => {
  assert.equal(mentionAt('hello', 5), undefined);
  assert.equal(mentionAt('ana@example.com', 16), undefined);
});

test('mentionAt only looks at text up to the cursor', () => {
  assert.deepEqual(mentionAt('@comp oser', 5), { start: 0, query: 'comp' });
});

test('matchFiles lists everything, unranked, for an empty query', () => {
  const files = ['b.ts', 'a.ts'];
  assert.deepEqual(
    matchFiles(files, '').map((m) => m.label),
    ['b.ts', 'a.ts'],
  );
});

test('matchFiles ranks a substring match by how early and short it is', () => {
  const files = ['src/renderer/components/Composer.tsx', 'src/Composer.tsx', 'src/renderer/lib/composer-helpers.ts'];
  const matched = matchFiles(files, 'composer').map((m) => m.label);
  assert.equal(matched[0], 'src/Composer.tsx');
});

test('matchFiles falls back to a fuzzy subsequence match', () => {
  const files = ['src/renderer/components/chat/Composer.tsx', 'src/renderer/lib/utils.ts'];
  const matched = matchFiles(files, 'cmpsr').map((m) => m.label);
  assert.deepEqual(matched, ['src/renderer/components/chat/Composer.tsx']);
});

test('matchFiles finds nothing for a query with no match at all', () => {
  assert.deepEqual(matchFiles(['a.ts', 'b.ts'], 'zzz'), []);
});

test('matchFiles respects the limit', () => {
  const files = Array.from({ length: 20 }, (_, i) => `file${i}.ts`);
  assert.equal(matchFiles(files, '', 3).length, 3);
});

test('a matched item carries the path as both label and insert', () => {
  const [item] = matchFiles(['README.md'], 'read');
  assert.deepEqual(item, { label: 'README.md', insert: 'README.md', kind: 'file' });
});

test('matchProjects matches the name or the display name, earliest match first', () => {
  const projects = [
    { name: 'my-saiapp4', displayName: 'My App' },
    { name: 'saiapp4', displayName: 'Sai App' },
    { name: 'other', displayName: 'Other' },
  ];
  assert.deepEqual(matchProjects(projects, 'SAI'), [
    { label: 'Sai App (saiapp4)', insert: 'saiapp4', kind: 'project' },
    { label: 'My App (my-saiapp4)', insert: 'my-saiapp4', kind: 'project' },
  ]);
  assert.deepEqual(matchProjects(projects, 'my app').map((m) => m.insert), ['my-saiapp4']);
  assert.deepEqual(matchProjects(projects, 'zzz'), []);
});

test('matchProjects lists every project for an empty query', () => {
  const projects = ['a', 'b', 'c', 'd'].map((name) => ({ name, displayName: name }));
  assert.deepEqual(matchProjects(projects, '').map((m) => m.label), projects.map((p) => p.name));
});

test('matchAgents matches the name or the title, earliest match first', () => {
  const agents = [
    { name: 'agent-2', title: 'Fix the tour' },
    { name: 'agent-3', title: 'Tour polish' },
    { name: 'agent-4', title: 'Media' },
  ];
  assert.deepEqual(
    matchAgents(agents, 'tour').map((m) => m.insert),
    ['agent-3', 'agent-2'],
  );
  assert.equal(matchAgents(agents, '')[0].label, 'Fix the tour (agent-2)');
});
