import assert from 'node:assert/strict';
import { test } from 'node:test';
import { heldBack, plainText } from './plainText.ts';

test('a table is its header and row count', () => {
  const t = '| Fruit | Color |\n|---|---|\n| Banana | Yellow |\n| Apple | Red |';
  assert.equal(plainText(t), 'Table: Fruit, Color · 2 rows');
  assert.equal(plainText(`Here:\n\n${t}\n\nDone`), 'Here: Table: Fruit, Color · 2 rows Done');
  assert.equal(plainText('| A |\n|:-:|\n| 1 |'), 'Table: A · 1 row');
});

test('a table cut mid-way reads as its cells', () => {
  assert.equal(plainText('|---|---|\n| Banana | Yellow |'), 'Banana, Yellow');
});

test('code, links, emphasis, headings, lists and tags are stripped', () => {
  assert.equal(plainText('# Title\n**Done.** See [PR #12](https://x/12)\n\n```go\nx\n```\nmerged'), 'Title Done. See PR #12 merged');
  assert.equal(plainText('- [x] ~~old~~ `new`<br>\n1. __one__ and _two_\n> quote\n---'), 'old new one and two quote');
  assert.equal(plainText('snake_case_name stays'), 'snake_case_name stays');
  assert.equal(plainText('text ```open fence'), 'text');
  assert.equal(plainText(undefined), '');
});

test('heldBack waits for a table\'s separator row', () => {
  assert.equal(heldBack('Intro\n\n| Fruit | Color'), 'Intro\n');
  assert.equal(heldBack('Intro\n\n| Fruit | Color |'), 'Intro\n');
  assert.equal(heldBack('| Fruit | Color |\n| Banana'), '');
  assert.equal(heldBack('| Fruit | Color |\n|---|---|'), '| Fruit | Color |\n|---|---|');
  assert.equal(heldBack('| F |\n|---|\n| a | b'), '| F |\n|---|\n| a | b');
  assert.equal(heldBack('plain a | b'), 'plain a | b');
  assert.equal(heldBack('```\n| not a table'), '```\n| not a table');
});
