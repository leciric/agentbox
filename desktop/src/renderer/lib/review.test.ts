import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { countLabel, goBack, goForward, mergeBlock, nextPending, startWalk, walkItems, queueOf, reviewKeys, reviewShortcut, slidesOf, step, swipeStep } from './review.ts';

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
  assert.equal(countLabel(7), '7');
  assert.equal(countLabel(120), '99+');
});

test('mergeBlock says why a pull request can’t be merged from the review', () => {
  assert.equal(mergeBlock(pr), null);
  assert.equal(mergeBlock({ ...pr, checks: 'pending' }), null, 'pending checks are the confirmation’s to warn about');
  assert.equal(mergeBlock({ ...pr, checks: 'failing' }), 'failing');
  assert.equal(mergeBlock({ ...pr, conflict: true, checks: 'failing' }), 'conflict');
  assert.equal(mergeBlock({ ...pr, draft: true }), 'draft');
  assert.equal(mergeBlock({ ...pr, state: 'merged' }), 'merged');
  assert.equal(mergeBlock({ ...pr, state: 'closed' }), 'closed');
  assert.equal(mergeBlock(pr, { canMergeKnown: true, canMerge: false }), 'noPermission');
  assert.equal(mergeBlock(pr, { canMergeKnown: false, canMerge: false }), null, 'not known is not a no');
});

const waiting = (...refs: string[]) => refs.map((ref) => ({ ref }) as T.ReviewItem);

test('Review all walks what was waiting, then what arrived, skipping what was reviewed', () => {
  const order = ['p/a', 'p/b', 'p/c'];
  assert.deepEqual(queueOf(order, waiting('p/d', 'p/a')), ['p/a', 'p/b', 'p/c', 'p/d']);
  assert.equal(nextPending(order, 'p/a', waiting('p/a', 'p/b', 'p/c'))?.ref, 'p/b');
  // a was just reviewed, and b in another window: c is next.
  assert.equal(nextPending(order, 'p/a', waiting('p/c'))?.ref, 'p/c');
  // An agent that finished meanwhile comes after the rest.
  assert.equal(nextPending(order, 'p/c', waiting('p/d'))?.ref, 'p/d');
  assert.equal(nextPending(order, 'p/c', waiting('p/c')), undefined);
  // Earlier ones still waiting aren't gone back to.
  assert.equal(nextPending(order, 'p/b', waiting('p/a')), undefined);
});

test('reviewShortcut maps keys to actions, and leaves modified ones alone', () => {
  assert.equal(reviewShortcut({ key: 'ArrowRight' }), 'next');
  assert.equal(reviewShortcut({ key: 'ArrowLeft' }), 'previous');
  assert.equal(reviewShortcut({ key: 'n' }), 'nextAgent');
  // Looking is reviewing: there is no key to mark it.
  assert.equal(reviewShortcut({ key: 'R' }), null);
  assert.equal(reviewShortcut({ key: 'd' }), null);
  assert.equal(reviewShortcut({ key: 'm' }), 'merge');
  assert.equal(reviewShortcut({ key: 'P' }), 'previousAgent');
  assert.equal(reviewShortcut({ key: 'c' }), 'requestChanges');
  assert.equal(reviewShortcut({ key: 'End' }), 'last');
  assert.equal(reviewShortcut({ key: 'r', metaKey: true }), null);
  assert.equal(reviewShortcut({ key: 'n', ctrlKey: true }), null);
  assert.equal(reviewShortcut({ key: 'x' }), null);
  // Every key the review lists does what it says.
  for (const k of reviewKeys) for (const key of k.keys) assert.ok(reviewShortcut({ key: key === '←' ? 'ArrowLeft' : key === '→' ? 'ArrowRight' : key }), key);
});

test("a project's walk goes through its agents only, and ends after its last", () => {
  const item = (ref: string) => ({ ref, project: ref.split('/')[0] }) as T.ReviewItem;
  const live = [item('p/a'), item('q/x'), item('p/b')];
  const items = walkItems(live, 'p');
  assert.deepEqual(items.map((i) => i.ref), ['p/a', 'p/b']);
  const order = items.map((i) => i.ref);
  assert.equal(nextPending(order, 'p/a', items)?.ref, 'p/b');
  // Sending changes on the last one ends the walk, and the carousel closes.
  assert.equal(nextPending(order, 'p/b', items), undefined);
  // Opened from everything, it goes on to the other projects.
  assert.equal(walkItems(live).length, 3);
});

test('openReview starts on the agent a card shows, or walks the waiting ones', () => {
  const item = (ref: string) => ({ ref, project: ref.split('/')[0] }) as T.ReviewItem;
  // From an agent's card: on that agent, even when the lists don't have it yet.
  assert.deepEqual(startWalk([], { item: item('p/a') }), { item: item('p/a'), order: null });
  const waiting = [item('q/x'), item('p/a'), item('p/b')];
  assert.deepEqual(startWalk(waiting, { project: 'p' }), { item: item('p/a'), order: ['p/a', 'p/b'], project: 'p' });
  assert.equal(startWalk(waiting)?.order?.length, 3);
  // Nothing waiting: nothing opens, and nothing is marked.
  assert.equal(startWalk([], { project: 'p' }), null);
});

test('Back returns to the agent shown just before, one step at a time', () => {
  const item = (ref: string) => ({ ref, project: 'p' }) as T.ReviewItem;
  const start = startWalk([item('p/a'), item('p/b'), item('p/c')])!;
  // Nothing before the first agent.
  assert.equal(goBack(start), start);
  const atC = goForward(goForward(start, item('p/b')), item('p/c'));
  assert.deepEqual(atC.trail?.map((i) => i.ref), ['p/a', 'p/b']);
  const atB = goBack(atC);
  assert.equal(atB.item.ref, 'p/b');
  assert.equal(goBack(atB).item.ref, 'p/a');
  assert.equal(goBack(goBack(atB)).item.ref, 'p/a');
  assert.deepEqual(atB.order, start.order);
});
