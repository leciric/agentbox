// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { SentenceFeed, sentences, speakableText } from './speakable.ts';

test('markdown is read as its words', () => {
  const md = '## Done\n\nI fixed **the bug** in `parse()` — see [the PR](https://github.com/x/y/pull/1).\n\n- first item\n- second item';
  assert.equal(speakableText(md, 'en'), 'Done.\n\nI fixed the bug in parse() — see the PR.\n\nfirst item.\nsecond item.');
});

test('a code block is read as a note, in the voice language', () => {
  const md = 'Run this:\n\n```bash\nnpm test\n```\n\nThen push.';
  assert.deepEqual(sentences(speakableText(md, 'en')), ['Run this:', 'The code is in the chat.', 'Then push.']);
  assert.deepEqual(sentences(speakableText(md, 'pt')), ['Run this:', 'O código está no chat.', 'Then push.']);
});

test('long inline code, bare links and tables are not spelled out', () => {
  assert.equal(speakableText('Ran `go test ./internal/voice/... -run TestSomethingLong` at https://x.dev/a', 'en'), 'Ran code at a link');
  assert.equal(speakableText('| a | b |\n|---|---|\n| 1 | 2 |', 'en'), 'a, b\n\n1, 2');
  assert.equal(speakableText('snake_case_name stays', 'en'), 'snake_case_name stays');
});

test('sentences split at their ends, and very long ones at a comma', () => {
  assert.deepEqual(sentences('One. Two! Three? Version 1.5 is out.'), ['One.', 'Two!', 'Three?', 'Version 1.5 is out.']);
  const long = `${'word '.repeat(40)}, ${'more '.repeat(40)}end.`;
  const parts = sentences(long);
  assert.ok(parts.length > 1);
  assert.ok(parts.every((p) => p.length <= 240));
  assert.equal(parts.join(' ').replace(/\s+/g, ' '), long.replace(/\s+/g, ' ').trim());
});

test('a streaming reply hands out each sentence once, as it ends', () => {
  const feed = new SentenceFeed('en');
  assert.deepEqual(feed.next('Hello there', false), []);
  assert.deepEqual(feed.next('Hello there. I am', false), ['Hello there.']);
  assert.deepEqual(feed.next('Hello there. I am **done**.', false), []);
  assert.deepEqual(feed.next('Hello there. I am **done**. ', false), ['I am done.']);
  assert.deepEqual(feed.next('Hello there. I am **done**. Bye', false), []);
  assert.deepEqual(feed.next('Hello there. I am **done**. Bye', true), ['Bye']);
  assert.deepEqual(feed.next('Hello there. I am **done**. Bye', true), []);
});

test('a code block streaming in is read once, when it is behind', () => {
  const feed = new SentenceFeed('en');
  assert.deepEqual(feed.next('Look:\n```go\nfunc', false), ['Look:']);
  assert.deepEqual(feed.next('Look:\n```go\nfunc main() {}\n```\nNice', false), ['The code is in the chat.']);
  assert.deepEqual(feed.next('Look:\n```go\nfunc main() {}\n```\nNice.', true), ['Nice.']);
});

test('turning reading on mid-reply starts from the next sentence', () => {
  const feed = new SentenceFeed('en', 'First. Second', false);
  assert.deepEqual(feed.next('First. Second. Third', false), ['Second.']);
  const settled = new SentenceFeed('en', 'All done.', true);
  assert.deepEqual(settled.next('All done.', true), []);
});
