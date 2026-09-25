// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { languageOf } from './highlight.ts';

test('languageOf is undefined with no fence info at all', () => {
  assert.equal(languageOf(undefined), undefined);
});

test('a language Shiki knows is returned as-is, case-insensitively', () => {
  assert.equal(languageOf('python'), 'python');
  assert.equal(languageOf('Python'), 'python');
});

test('a common alias resolves to its Shiki language', () => {
  assert.equal(languageOf('js'), 'javascript');
  assert.equal(languageOf('sh'), 'bash');
  assert.equal(languageOf('yml'), 'yaml');
  assert.equal(languageOf('ts'), 'typescript');
});

test('a fence tag nobody registered is undefined, not guessed', () => {
  assert.equal(languageOf('brainfuck'), undefined);
  assert.equal(languageOf(''), undefined);
});
