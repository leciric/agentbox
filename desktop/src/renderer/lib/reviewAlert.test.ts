import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { batch, detectNewlyReady, readyKey } from './reviewAlert.ts';

const item = (ref: string, fingerprint = 'a'): T.ReviewItem => ({ project: 'p', agent: ref, ref, finishedAt: '2026-10-14T10:00:00Z', fingerprint });

test('the first list is the baseline: nothing is news', () => {
  const r = detectNewlyReady(null, [item('a1'), item('a2')]);
  assert.deepEqual(r.fresh, []);
  assert.deepEqual([...r.known].sort(), [readyKey(item('a1')), readyKey(item('a2'))]);
});

test('an agent that lands afterwards is fresh, once', () => {
  const base = detectNewlyReady(null, [item('a1')]);
  const next = detectNewlyReady(base.known, [item('a1'), item('a2')]);
  assert.deepEqual(next.fresh.map((i) => i.ref), ['a2']);
  assert.deepEqual(detectNewlyReady(next.known, [item('a1'), item('a2')]).fresh, []);
});

test('several landing together are all fresh', () => {
  const base = detectNewlyReady(null, []);
  assert.equal(detectNewlyReady(base.known, [item('a1'), item('a2'), item('a3')]).fresh.length, 3);
});

test('an agent finishing again (new fingerprint) is news; one that dropped off and returned unchanged is not', () => {
  const a = detectNewlyReady(null, [item('a1')]);
  const gone = detectNewlyReady(a.known, []);
  assert.deepEqual(detectNewlyReady(gone.known, [item('a1')]).fresh, []);
  assert.deepEqual(detectNewlyReady(gone.known, [item('a1', 'b')]).fresh.map((i) => i.ref), ['a1']);
});

test('batch hands over what arrives together once', async () => {
  const calls: string[][] = [];
  const b = batch<string>(20, (all) => calls.push(all));
  b.add(['x']);
  b.add(['y', 'z']);
  b.add([]);
  await new Promise((r) => setTimeout(r, 60));
  assert.deepEqual(calls, [['x', 'y', 'z']]);
  b.add(['w']);
  b.cancel();
  await new Promise((r) => setTimeout(r, 40));
  assert.equal(calls.length, 1);
});
