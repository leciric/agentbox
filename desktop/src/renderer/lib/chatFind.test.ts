// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { findKinds, fold, hitsByDay, localDay, matchesIn, stepHit } from './chatFind.ts';

const marked = (text: string, query: string) => {
  let out = '';
  let pos = 0;
  for (const [s, e] of matchesIn(text, query)) {
    out += `${text.slice(pos, s)}[${text.slice(s, e)}]`;
    pos = e;
  }
  return out + text.slice(pos);
};

test('a find ignores case and accents, as the daemon does', () => {
  assert.equal(marked('Fix the Login page, then LOGIN again', 'login'), 'Fix the [Login] page, then [LOGIN] again');
  assert.equal(marked('Não sei. Nao sei.', 'nao'), '[Não] sei. [Nao] sei.');
  assert.equal(marked('a ação', 'AÇÃO'), 'a [ação]');
});

test('a find reads any run of whitespace as one space', () => {
  assert.equal(marked('deploy\n\n  it now', 'deploy it'), '[deploy\n\n  it] now');
  assert.equal(marked('anything', '   '), 'anything');
  assert.equal(marked('anything', ''), 'anything');
});

test('a find marks characters outside the BMP whole', () => {
  assert.equal(marked('ship it 🚀 now', 'it 🚀'), 'ship [it 🚀] now');
  const { folded, at } = fold('é🚀x');
  assert.equal(folded, 'e🚀x');
  assert.deepEqual(at, [0, 1, 1, 3]);
});

test('matches do not overlap', () => {
  assert.equal(marked('aaaa', 'aa'), '[aa][aa]');
});

test('stepping through hits starts at the newest and goes round', () => {
  const hits = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
  assert.equal(stepHit(hits, undefined, 'stay'), 'c');
  assert.equal(stepHit(hits, 'gone', 'older'), 'c');
  assert.equal(stepHit(hits, 'c', 'older'), 'b');
  assert.equal(stepHit(hits, 'a', 'older'), 'c');
  assert.equal(stepHit(hits, 'c', 'newer'), 'a');
  assert.equal(stepHit(hits, 'b', 'stay'), 'b');
  assert.equal(stepHit([], 'b', 'older'), undefined);
});

test('findKinds asks for what you wrote, what the AI tool said, or everything', () => {
  assert.deepEqual(findKinds('you'), ['user', 'aside']);
  assert.deepEqual(findKinds('ai'), ['assistant']);
  assert.deepEqual(findKinds('anyone'), []);
});

test('hitsByDay lists the newest first, a group per day, the undated last', () => {
  const at = (y: number, m: number, d: number, h: number) => new Date(y, m - 1, d, h).toISOString();
  const hits = [
    { id: 'a', at: at(2026, 10, 3, 9) },
    { id: 'b' },
    { id: 'c', at: at(2026, 10, 3, 18) },
    { id: 'd', at: at(2026, 10, 10, 12) },
  ];
  assert.deepEqual(
    hitsByDay(hits).map((g) => [g.day, g.hits.map((h) => h.id)]),
    [
      ['2026-10-10', ['d']],
      ['2026-10-03', ['c', 'a']],
      ['', ['b']],
    ],
  );
  assert.deepEqual(hitsByDay([]), []);
  assert.equal(localDay(new Date(2026, 0, 5, 23)), '2026-01-05');
});
