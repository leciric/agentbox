// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { branchSlug, maxBranchSlugLen } from './branch.ts';

test('a title becomes lowercase kebab-case', () => {
  assert.equal(branchSlug('Fix the Login Bug'), 'fix-the-login-bug');
});

test('punctuation collapses into single dashes, trimmed at the ends', () => {
  assert.equal(branchSlug('  --Fix!! login, bug??--  '), 'fix-login-bug');
});

test('only the first line of a multi-line title counts', () => {
  assert.equal(branchSlug('Fix login\n\nDetails go here'), 'fix-login');
});

test('a slug at or under the limit is kept whole', () => {
  const title = 'a'.repeat(maxBranchSlugLen);
  assert.equal(branchSlug(title), title);
  assert.equal(branchSlug(title).length, maxBranchSlugLen);
});

test('a long slug is cut at the last word boundary within the limit', () => {
  const title = `${'a'.repeat(40)}-short-words-continue-past-the-limit`;
  const slug = branchSlug(title);
  assert.ok(slug.length <= maxBranchSlugLen, slug);
  assert.ok(!slug.endsWith('-'), slug);
  assert.ok(title.toLowerCase().startsWith(slug));
});

test('a single word longer than the limit is hard-cut, not left over-length', () => {
  const title = 'a'.repeat(maxBranchSlugLen + 20);
  const slug = branchSlug(title);
  assert.equal(slug.length, maxBranchSlugLen);
  assert.equal(slug, 'a'.repeat(maxBranchSlugLen));
});

test('an empty or all-punctuation title becomes an empty slug', () => {
  assert.equal(branchSlug(''), '');
  assert.equal(branchSlug('   '), '');
  assert.equal(branchSlug('!!!'), '');
});
