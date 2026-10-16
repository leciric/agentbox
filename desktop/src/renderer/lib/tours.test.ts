import assert from 'node:assert/strict';
import { test } from 'node:test';
import { goTo, onLaunch, onSwitchToV2, parseState, placeCard, skip, stepsOf, tourFirst, tours, welcomeTour, type TourState } from './tours.ts';

const none: TourState = { seen: [], dismissed: [], playing: null };
const whatsNew = tours.map((t) => t.id);

test('a first run plays the welcome tour, and What\'s new counts as seen', () => {
  for (const arrival of ['fresh', 'moved'] as const) {
    const s = onLaunch(none, arrival);
    assert.deepEqual(s.playing, { tours: ['welcome'], step: 0 });
    assert.deepEqual(s.seen, whatsNew);
  }
});

test('an update plays the tours not seen yet, once', () => {
  const s = onLaunch({ ...none, seen: ['v2'], dismissed: ['review'] }, 'v2');
  assert.deepEqual(s.playing?.tours, whatsNew.filter((id) => id !== 'v2' && id !== 'review'));
  const after = goTo(s, stepsOf(s.playing!).length);
  assert.equal(after.playing, null);
  assert.equal(onLaunch(after, 'v2').playing, null);
});

test('Classic plays nothing until v2 is chosen, then the welcome once', () => {
  assert.equal(onLaunch(none, 'classic').playing, null);
  const s = onSwitchToV2(none);
  assert.deepEqual(s.playing?.tours, ['welcome']);
  assert.equal(onSwitchToV2(skip(s)).playing, null);
});

test('a run cut short resumes where it was', () => {
  const s = goTo(onLaunch(none, 'fresh'), 4);
  const back = parseState(JSON.stringify(s));
  assert.deepEqual(onLaunch(back, 'v2').playing, { tours: ['welcome'], step: 4 });
});

test('skipping dismisses, finishing marks seen', () => {
  const s = onLaunch(none, 'fresh');
  assert.deepEqual(skip(s).dismissed, ['welcome']);
  assert.ok(goTo(s, welcomeTour.steps.length).seen.includes('welcome'));
  assert.equal(goTo(s, -3).playing?.step, 0);
});

test('stored state is read defensively', () => {
  assert.deepEqual(parseState('nonsense'), none);
  assert.deepEqual(parseState('{"seen":["v2",3],"playing":{"tours":["gone"],"step":2}}'), { seen: ['v2'], dismissed: [], playing: null });
});

test('every tour has steps, and the welcome ends on how to go back to Classic and find it again', () => {
  for (const t of [welcomeTour, ...tours]) assert.ok(t.steps.length > 0, t.id);
  const ids = welcomeTour.steps.map((s) => s.id);
  assert.deepEqual(ids.slice(-2), ['classic', 'help']);
  assert.equal(new Set(tours.map((t) => t.id)).size, tours.length);
});

test('the card sits beside what it points at, or docks when it can\'t', () => {
  const vp = { width: 1440, height: 900 };
  const card = { width: 360, height: 300 };
  // A sidebar row: to its right.
  assert.deepEqual(placeCard({ left: 8, top: 120, width: 270, height: 48 }, card, vp), { left: 292, top: 120 });
  // The right-hand panel: to its left.
  assert.deepEqual(placeCard({ left: 900, top: 56, width: 540, height: 844 }, card, vp), { left: 526, top: 56 });
  // Nothing to point at: the middle.
  assert.deepEqual(placeCard(null, card, vp), { left: 540, top: 300 });
  // A narrow window: docked at the bottom, or the top for a low target.
  assert.deepEqual(placeCard({ left: 10, top: 100, width: 300, height: 40 }, card, { width: 480, height: 800 }), { left: 60, top: 488 });
  assert.deepEqual(placeCard({ left: 10, top: 700, width: 300, height: 40 }, card, { width: 480, height: 800 }), { left: 60, top: 12 });
});

test('an update with a tour to play holds the release notes back; one without leaves them to open', () => {
  assert.equal(tourFirst({ ...none, seen: ['welcome'] }, 'v2'), true);
  assert.equal(tourFirst({ ...none, seen: ['welcome', ...whatsNew] }, 'v2'), false);
  assert.equal(tourFirst({ ...none, playing: { tours: ['review'], step: 1 } }, 'v2'), true);
  assert.equal(tourFirst(none, 'classic'), false);
});
