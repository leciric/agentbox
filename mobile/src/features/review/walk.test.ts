import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { advance, goBack, goForward, nextPending, position, startWalk } from './walk.ts';

const item = (agent: string): T.ReviewItem => ({ project: 'web', agent, ref: `web/${agent}`, finishedAt: '', fingerprint: agent });
const [a, b, c] = [item('a'), item('b'), item('c')];

test('a walk starts on the first and steps to the next waiting', () => {
  const w = startWalk([a, b, c])!;
  assert.equal(startWalk([]), null);
  assert.equal(nextPending(w, [a, b, c])?.ref, 'web/b');
  // a is reviewed (gone from the list): b is next, and the position holds
  assert.equal(advance(w, [b, c])?.item.ref, 'web/b');
  assert.deepEqual(position(w, [b, c]), { n: 1, count: 3 });
});

test('back returns to the agent shown before, reviewed or not', () => {
  const w = goForward(startWalk([a, b])!, b);
  assert.equal(goBack(w).item.ref, 'web/a');
  assert.equal(goBack(goBack(w)).item.ref, 'web/a');
});

test('at the end it wraps to what was skipped, then finishes', () => {
  let w = goForward(startWalk([a, b])!, b);
  w = advance(w, [a, b])!; // b is last; a was skipped
  assert.equal(w.item.ref, 'web/a');
  assert.equal(advance(w, [a]), null);
});

test('an agent that finished on the way joins the end', () => {
  const w = startWalk([a])!;
  assert.equal(advance(w, [a, c])?.item.ref, 'web/c');
});
