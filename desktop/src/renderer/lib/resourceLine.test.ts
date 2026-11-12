// Run with `npm test`.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runs } from './resourceLine.ts';

const at = (s: number) => new Date(Date.UTC(2026, 10, 6, 12, 0, s)).toISOString();
const start = Date.UTC(2026, 10, 6, 12, 0, 0);

test('a stretch nobody watched breaks the line, and lone points are left out', () => {
  const got = runs(
    [
      [at(2), 1],
      [at(4), 2],
      [at(6), 3],
      [at(30), 9], // the view was closed for 24 s: a lone point after it
      [at(50), 4],
      [at(52), 5],
    ],
    start,
    600_000,
  );
  assert.deepEqual(
    got.map((r) => r.map(([, v]) => v)),
    [
      [1, 2, 3],
      [4, 5],
    ],
  );
});

test('points outside the window are dropped', () => {
  assert.deepEqual(runs([[at(2), 1], [at(4), 2]], start + 3000, 600_000), []);
});
