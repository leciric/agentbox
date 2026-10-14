import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { countLabel, reviewKey, slidesOf, step, swipeStep, withoutAgent } from './review.ts';

const media = (id: string, kind: string, createdAt: string) => ({ id, kind, createdAt }) as T.MediaItem;
const page = (id: string, createdAt: string) => ({ id, createdAt }) as T.Artifact;
const pr = { number: 12, title: 'feat: dark mode', state: 'open', checks: 'passing', url: '' } as T.PullRequest;

test('slidesOf puts what an agent made in order, and its pull request last', () => {
  const slides = slidesOf(
    [media('rec', 'recording', '2026-10-14T10:05:00Z'), media('shot', 'screenshot', '2026-10-14T10:00:00Z'), media('note', 'note', '2026-10-14T09:00:00Z')],
    [page('pg', '2026-10-14T10:02:00Z')],
    pr,
  );
  assert.deepEqual(
    slides.map((s) => [s.kind, s.id]),
    [
      ['screenshot', 'shot'],
      ['page', 'pg'],
      ['recording', 'rec'],
      ['pr', 'pr-12'],
    ],
  );
});

test('slidesOf leaves out notes and logs, and the pull request when there is none', () => {
  assert.deepEqual(slidesOf([media('log', 'log', '2026-10-14T10:00:00Z')], []), []);
});

test('step stops at either end', () => {
  assert.equal(step(0, 3, -1), 0);
  assert.equal(step(1, 3, 1), 2);
  assert.equal(step(2, 3, 1), 2);
  assert.equal(step(0, 0, 1), 0);
});

test('swipeStep only counts a long, mostly sideways drag', () => {
  assert.equal(swipeStep(-80, 10), 1);
  assert.equal(swipeStep(80, 10), -1);
  assert.equal(swipeStep(30, 0), 0);
  assert.equal(swipeStep(-60, 90), 0);
});

test('the list helpers', () => {
  assert.deepEqual(reviewKey(), ['review']);
  assert.deepEqual(reviewKey('pawly'), ['review', 'pawly']);
  assert.equal(countLabel(7), '7');
  assert.equal(countLabel(120), '99+');
  const list = [{ ref: 'p/agent-01' }, { ref: 'p/agent-02' }] as T.ReviewItem[];
  assert.deepEqual(withoutAgent(list, 'p/agent-01')?.map((i) => i.ref), ['p/agent-02']);
  assert.equal(withoutAgent(undefined, 'p/agent-01'), undefined);
});
