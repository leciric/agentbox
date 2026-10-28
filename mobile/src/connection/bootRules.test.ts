import assert from 'node:assert/strict';
import { test } from 'node:test';
import { begin, parseBoot, safeAfter } from './bootRules.ts';

test('a start after one that finished is a normal one', () => {
  assert.deepEqual(begin(parseBoot(null)), { mode: 'normal', crashes: 0, error: undefined, record: { starts: 1 } });
  assert.equal(begin(parseBoot('{"starts":0}')).mode, 'normal');
});

test('a crash on start clears the cache once, and then the app waits for the user', () => {
  let record = parseBoot(null);
  const modes: string[] = [];
  // Every start crashes before it finishes: the record is all that's kept.
  for (let i = 0; i < 4; i++) {
    const next = begin(record);
    modes.push(next.mode);
    record = parseBoot(JSON.stringify({ ...next.record, error: 'TypeError: boom' }));
  }
  assert.deepEqual(modes, ['normal', 'recovered', 'safe', 'safe']);
  assert.equal(safeAfter, 2);
  const last = begin(record);
  assert.equal(last.error, 'TypeError: boom');
  assert.equal(last.crashes, 4);
});

test('a record it cannot read counts as a crash, not as a reason to crash', () => {
  assert.equal(begin(parseBoot('{not json')).mode, 'recovered');
  assert.equal(begin(parseBoot('{"starts":"lots"}')).mode, 'normal');
  assert.equal(begin(parseBoot('null')).mode, 'normal');
});
