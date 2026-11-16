import assert from 'node:assert/strict';
import { test } from 'node:test';
import { arrive, goTo, onLaunch, parseState, placeCard, skip, watchView, stepsOf, tourFirst, tours, welcomeTour, type TourState } from './tours.ts';

const none: TourState = { seen: [], dismissed: [], playing: null };
const whatsNew = tours.map((t) => t.id);

const storage = (init: Record<string, string> = {}) => {
  const store = new Map(Object.entries(init));
  return {
    store,
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
  };
};

test('a first run arrives fresh, and the next launch returns', () => {
  const s = storage();
  assert.equal(arrive(s), 'fresh');
  assert.equal(arrive(s), 'returning');
});

test('an install from before the new layout was the default moves, once', () => {
  const s = storage({ 'agentbox.whatsnew.lastSeenVersion': '0.12.1' });
  assert.equal(arrive(s), 'moved');
  assert.equal(arrive(s), 'returning');
});

test('an install left on Classic moves to the new layout, once', () => {
  const s = storage({ 'agentbox.whatsnew.lastSeenVersion': '0.16.0', 'agentbox.ui.v2': '0' });
  assert.equal(arrive(s), 'moved');
  assert.equal(s.store.get('agentbox.ui.v2'), '1');
  assert.equal(arrive(s), 'returning');
  // Its welcome tour plays once: seen or dismissed, it doesn't again.
  const played = onLaunch(none, 'moved');
  assert.deepEqual(played.playing?.tours, ['welcome']);
  assert.equal(onLaunch(skip(played), 'moved').playing, null);
});

test('an install already on the new layout returns', () => {
  assert.equal(arrive(storage({ 'agentbox.ui.v2': '1' })), 'returning');
});

test('a first run plays the welcome tour, and What\'s new counts as seen', () => {
  for (const arrival of ['fresh', 'moved'] as const) {
    const s = onLaunch(none, arrival);
    assert.deepEqual(s.playing, { tours: ['welcome'], step: 0 });
    assert.deepEqual(s.seen, whatsNew);
  }
});

test('an update plays the tours not seen yet, once', () => {
  const s = onLaunch({ ...none, seen: ['v2'], dismissed: ['review'] }, 'returning');
  assert.deepEqual(s.playing?.tours, whatsNew.filter((id) => id !== 'v2' && id !== 'review'));
  const after = goTo(s, stepsOf(s.playing!).length);
  assert.equal(after.playing, null);
  assert.equal(onLaunch(after, 'returning').playing, null);
});

test('a move the tour made keeps it playing, and any other one leaves it', () => {
  // The tour opens a lead's chat for its step: it plays on.
  let w = watchView({ at: 'home', sent: null }, 'home').watch;
  w = { ...w, sent: 'project:a:chat' };
  let r = watchView(w, 'project:a:chat');
  assert.equal(r.left, false);
  // Re-renders on the same view aren't moves.
  r = watchView(r.watch, 'project:a:chat');
  assert.equal(r.left, false);
  // The user opens Settings: the tour ends, never pulled back to the chat.
  r = watchView(r.watch, 'settings');
  assert.equal(r.left, true);
  // The tour's move counts once: coming back to that chat later is the user's.
  assert.equal(watchView(watchView(w, 'project:a:chat').watch, 'home').left, true);
  assert.equal(watchView({ at: 'settings', sent: null }, 'project:a:chat').left, true);
  assert.equal(skip({ seen: [], dismissed: [], playing: { tours: ['welcome'], step: 6 } }).playing, null);
});

test('a run cut short resumes where it was', () => {
  const s = goTo(onLaunch(none, 'fresh'), 4);
  const back = parseState(JSON.stringify(s));
  assert.deepEqual(onLaunch(back, 'returning').playing, { tours: ['welcome'], step: 4 });
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

test('every tour has steps, and the welcome ends on how to find it again', () => {
  for (const t of [welcomeTour, ...tours]) assert.ok(t.steps.length > 0, t.id);
  const ids = welcomeTour.steps.map((s) => s.id);
  assert.equal(ids.at(-1), 'help');
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
  assert.equal(tourFirst({ ...none, seen: ['welcome'] }, 'returning'), true);
  assert.equal(tourFirst({ ...none, seen: ['welcome', ...whatsNew] }, 'returning'), false);
  assert.equal(tourFirst({ ...none, playing: { tours: ['review'], step: 1 } }, 'returning'), true);
});
