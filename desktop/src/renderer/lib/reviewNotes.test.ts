import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parsePatch } from './pulls.ts';
import { inNote, noteAt, noteRange, splitGitDiff, whereLabel } from './reviewNotes.ts';

const patch = ['@@ -10,4 +10,4 @@ func save() {', ' keep()', '-old()', '+renamed()', '+added()', ' last()'].join('\n');
const lines = parsePatch(patch);

test('a range is its rows of code, numbered as the new file unless all removed', () => {
  assert.deepEqual(noteRange('a.go', lines, 1, 1), { path: 'a.go', side: 'new', start: 10, end: 10, code: ' keep()' });
  // Dragged upwards, from added() to keep(): the same as downwards.
  assert.deepEqual(noteRange('a.go', lines, 4, 1), { path: 'a.go', side: 'new', start: 10, end: 12, code: ' keep()\n-old()\n+renamed()\n+added()' });
  // Starting on a removed line, it's still the new file's lines; only
  // removed lines alone are the old file's.
  assert.deepEqual(noteRange('a.go', lines, 2, 5), { path: 'a.go', side: 'new', start: 11, end: 13, code: '-old()\n+renamed()\n+added()\n last()' });
  assert.deepEqual(noteRange('a.go', lines, 2, 2), { path: 'a.go', side: 'old', start: 11, end: 11, code: '-old()' });
  assert.equal(noteRange('a.go', lines, 0, 0), null, 'the hunk header is no code');
});

test('a note goes back under its last line while the diff has its code there', () => {
  const note = { side: 'new', start: 11, end: 12, code: '+renamed()\n+added()' };
  assert.equal(noteAt(lines, note), 4);
  assert.equal(noteAt(lines, { ...note, code: '+renamed()\n+moved()' }), -1, 'other code at that line now');
  assert.equal(noteAt(lines, { ...note, end: 99 }), -1);
  assert.equal(noteAt(lines, { side: 'old', start: 11, end: 11, code: '-old()' }), 2);
  assert.ok(inNote(lines[3], note) && !inNote(lines[1], note) && !inNote(lines[2], note));
});

test('where a note is reads as one line, lines, or removed ones', () => {
  assert.equal(whereLabel({ side: 'new', start: 3, end: 3 }).key, 'review.notes.line');
  assert.equal(whereLabel({ side: 'new', start: 3, end: 5 }).key, 'review.notes.lines');
  assert.equal(whereLabel({ side: 'old', start: 3, end: 5 }).key, 'review.notes.removedLines');
});

test('a whole diff is cut into its files, from their first hunk', () => {
  const text = [
    'diff --git a/a.go b/a.go',
    'index 1..2 100644',
    '--- a/a.go',
    '+++ b/a.go',
    '@@ -1 +1 @@',
    '-x',
    '+y',
    'diff --git a/logo.png b/logo.png',
    'Binary files a/logo.png and b/logo.png differ',
    'diff --git a/gone.txt b/gone.txt',
    'deleted file mode 100644',
    '--- a/gone.txt',
    '+++ /dev/null',
    '@@ -1 +0,0 @@',
    '-bye',
    '',
  ].join('\n');
  assert.deepEqual(splitGitDiff(text), [
    { path: 'a.go', patch: '@@ -1 +1 @@\n-x\n+y' },
    { path: 'logo.png', patch: '' },
    { path: 'gone.txt', patch: '@@ -1 +0,0 @@\n-bye' },
  ]);
});
