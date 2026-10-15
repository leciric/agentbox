import assert from 'node:assert/strict';
import { test } from 'node:test';
import { allowed, applyMode, defaults, dndActive, dndEnd, forever, kinds, modeOf, parsePrefs } from './notifyPrefs.ts';

const noon = new Date(2026, 9, 14, 12, 0).getTime();

test('everything is on by default, and only what needs you has a switch', () => {
  const p = defaults();
  assert.deepEqual([...kinds], ['review', 'question', 'error']);
  for (const k of kinds) assert.equal(p.kinds[k].system && p.kinds[k].sound, true);
  assert.equal(dndActive(p, noon), false);
  assert.equal(modeOf(p), 'on');
});

test('parsePrefs keeps what is valid and defaults the rest', () => {
  const p = parsePrefs(JSON.stringify({ kinds: { question: { sound: false }, error: { system: 'yes' }, bogus: { sound: false } }, dndUntil: 5 }));
  assert.equal(p.kinds.question.sound, false);
  assert.equal(p.kinds.question.system, true);
  assert.equal(p.kinds.error.system, true);
  assert.equal(p.dndUntil, 5);
  assert.deepEqual(parsePrefs('not json'), defaults());
});

test('the chime’s old switch still decides the review sound until something is stored', () => {
  assert.equal(parsePrefs(null, 'false').kinds.review.sound, false);
  assert.equal(parsePrefs(null, 'true').kinds.review.sound, true);
  assert.equal(parsePrefs(JSON.stringify({}), 'false').kinds.review.sound, true);
});

test('what was stored for finishing carries over to Ready to review', () => {
  const p = parsePrefs(JSON.stringify({ kinds: { finished: { sound: false, system: false }, ci: { sound: false } } }));
  assert.deepEqual(p.kinds.review, { sound: false, system: false });
  assert.deepEqual(Object.keys(p.kinds).sort(), ['error', 'question', 'review']);
  // Its own entry wins once there is one.
  assert.equal(parsePrefs(JSON.stringify({ kinds: { finished: { sound: false }, review: { sound: true } } })).kinds.review.sound, true);
});

test('do not disturb overrides every switch until it ends', () => {
  const p = { ...defaults(), dndUntil: noon + 1000 };
  assert.equal(allowed(p, 'question', 'system', noon), false);
  assert.equal(allowed(p, 'question', 'sound', noon), false);
  assert.equal(allowed(p, 'question', 'sound', noon + 1000), true);
  assert.equal(allowed({ ...defaults(), dndUntil: forever }, 'error', 'system', noon * 100), false);
});

test('a switch off stays off', () => {
  const p = defaults();
  p.kinds.error.sound = false;
  assert.equal(allowed(p, 'error', 'sound', noon), false);
  assert.equal(allowed(p, 'error', 'system', noon), true);
});

test('durations end when they say', () => {
  assert.equal(dndEnd('hour', noon), noon + 3600_000);
  assert.equal(dndEnd('forever', noon), forever);
  assert.equal(dndEnd('tomorrow', noon), new Date(2026, 9, 15, 8, 0).getTime());
  // Before 8 in the morning, "tomorrow" is the same morning's 8.
  const early = new Date(2026, 9, 14, 6, 0).getTime();
  assert.equal(dndEnd('tomorrow', early), new Date(2026, 9, 14, 8, 0).getTime());
});

test('the first run’s answer sets every kind and keeps do not disturb', () => {
  const base = { ...defaults(), dndUntil: 7 };
  const sound = applyMode(base, 'sound');
  assert.equal(modeOf(sound), 'sound');
  assert.ok(kinds.every((k) => sound.kinds[k].sound && !sound.kinds[k].system));
  const silent = applyMode(base, 'silent');
  assert.equal(modeOf(silent), 'silent');
  assert.equal(silent.dndUntil, 7);
  assert.deepEqual(applyMode(silent, 'on').kinds, defaults().kinds);
});
