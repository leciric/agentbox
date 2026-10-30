import assert from 'node:assert/strict';
import { test } from 'node:test';
import { columnWidths, delimiterOf, parseDelimited } from './delimited.ts';

test('delimiterOf knows CSV and TSV files', () => {
  assert.equal(delimiterOf('data/a.CSV'), ',');
  assert.equal(delimiterOf('a.tsv'), '\t');
  assert.equal(delimiterOf('a.tab'), '\t');
  assert.equal(delimiterOf('csv'), undefined);
  assert.equal(delimiterOf('a.txt'), undefined);
});

test('parseDelimited reads quotes, doubled quotes, line breaks in fields and CRLF', () => {
  const got = parseDelimited('name,note\r\n"Ada, Countess","said ""hi""\nthen left"\r\nBob,\n', ',');
  assert.deepEqual(got.rows, [
    ['name', 'note'],
    ['Ada, Countess', 'said "hi"\nthen left'],
    ['Bob', ''],
  ]);
  assert.equal(got.columns, 2);
  assert.equal(got.unclosed, false);
});

test('parseDelimited keeps ragged rows, empty lines and a last line without a break', () => {
  const got = parseDelimited('a\tb\tc\n\n1\t2', '\t');
  assert.deepEqual(got.rows, [['a', 'b', 'c'], [''], ['1', '2']]);
  assert.equal(got.columns, 3);
  assert.deepEqual(parseDelimited('', ',').rows, []);
  assert.deepEqual(parseDelimited('x,\n', ',').rows, [['x', '']]);
  assert.deepEqual(parseDelimited('a\rb\r', ',').rows, [['a'], ['b']]);
});

test('parseDelimited says when a quote is never closed', () => {
  const got = parseDelimited('a,b\n1,"unfinished\n2,3', ',');
  assert.equal(got.unclosed, true);
  assert.deepEqual(got.rows, [['a', 'b'], ['1', 'unfinished\n2,3']]);
});

test('parseDelimited is quick on a large file', () => {
  const line = Array.from({ length: 20 }, (_, i) => (i % 3 ? `value${i}` : `"quoted, ${i}"`)).join(',') + '\n';
  const text = line.repeat(50_000);
  const started = performance.now();
  const got = parseDelimited(text, ',');
  assert.equal(got.rows.length, 50_000);
  assert.equal(got.rows[49_999][3], 'quoted, 3');
  assert.ok(performance.now() - started < 2000);
});

test('columnWidths measures the header and the first rows, clamped', () => {
  assert.deepEqual(columnWidths(['id', 'a long header'], [['1', 'x'.repeat(90)], ['22']], 3, 4, 40), [4, 40, 4]);
});
