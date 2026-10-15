import assert from 'node:assert/strict';
import { test } from 'node:test';
import { framePage, richKind } from './rich.ts';

test('the fenced blocks a chat draws', () => {
  assert.equal(richKind('svg'), 'svg');
  assert.equal(richKind('html preview'), 'html');
  assert.equal(richKind('HTML  Preview'), 'html');
  assert.equal(richKind('html'), undefined);
  assert.equal(richKind('mermaid'), 'mermaid');
  assert.equal(richKind('ts'), undefined);
  assert.equal(richKind(undefined), undefined);
});

test("a render's page lets nothing load or run", () => {
  const page = framePage('html', '<script>alert(1)</script><p>hi</p>');
  assert.match(page, /default-src 'none'/);
  assert.doesNotMatch(page, /script-src/);
  assert.match(page, /<p>hi<\/p>/);
});
