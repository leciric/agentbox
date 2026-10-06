// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { limiter } from './videoStills.ts';

// task is a job the test finishes by hand, recording when it started.
function task(started: string[], name: string) {
  let finish!: () => void;
  const done = new Promise<string>((resolve) => (finish = () => resolve(name)));
  return { run: () => (started.push(name), done), finish: () => finish() };
}

const tick = () => new Promise((resolve) => setImmediate(resolve));

test('limiter runs at most its limit at a time, in order', async () => {
  const run = limiter(2);
  const started: string[] = [];
  const [a, b, c] = ['a', 'b', 'c'].map((n) => task(started, n));
  const results = [run(a!.run), run(b!.run), run(c!.run)];
  await tick();
  assert.deepEqual(started, ['a', 'b']);
  a!.finish();
  await tick();
  assert.deepEqual(started, ['a', 'b', 'c']);
  b!.finish();
  c!.finish();
  assert.deepEqual(await Promise.all(results), ['a', 'b', 'c']);
});

test('limiter drops a task aborted while it waits, and rejects it', async () => {
  const run = limiter(1);
  const started: string[] = [];
  const [a, b, c] = ['a', 'b', 'c'].map((n) => task(started, n));
  const gone = new AbortController();
  const first = run(a!.run);
  const second = run(b!.run, gone.signal);
  const third = run(c!.run);
  gone.abort(new Error('scrolled away'));
  await assert.rejects(second, /scrolled away/);
  a!.finish();
  await first;
  await tick();
  assert.deepEqual(started, ['a', 'c']);
  c!.finish();
  assert.equal(await third, 'c');
});

test('limiter frees the slot of a task that failed', async () => {
  const run = limiter(1);
  await assert.rejects(run(() => Promise.reject(new Error('unplayable'))), /unplayable/);
  assert.equal(await run(() => Promise.resolve('next')), 'next');
});
