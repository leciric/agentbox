// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { matchFiles, mentionAt } from './mentions.ts';

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
