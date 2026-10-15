import assert from 'node:assert/strict';
import { test } from 'node:test';
import { allowed, applyMode, defaults, dndActive, dndEnd, forever, kindOf, kinds, modeOf, parsePrefs } from './notifyPrefs.ts';

const noon = new Date(2026, 9, 14, 12, 0).getTime();

test('everything is on by default, but CI is quiet', () => {
  const p = defaults();
  for (const k of kinds) assert.equal(p.kinds[k].system, true);
  assert.equal(p.kinds.finished.sound, true);
  assert.equal(p.kinds.ci.sound, false);
  assert.equal(dndActive(p, noon), false);
  assert.equal(modeOf(p), 'on');
});

test('parsePrefs keeps what is valid and defaults the rest', () => {
  const p = parsePrefs(JSON.stringify({ kinds: { question: { sound: false }, ci: { system: 'yes' }, bogus: { sound: false } }, dndUntil: 5 }));
  assert.equal(p.kinds.question.sound, false);
  assert.equal(p.kinds.question.system, true);
  assert.equal(p.kinds.ci.system, true);
  assert.equal(p.dndUntil, 5);
  assert.deepEqual(parsePrefs('not json'), defaults());
});

test('the chime’s old switch still decides the finished sound until something is stored', () => {
  assert.equal(parsePrefs(null, 'false').kinds.finished.sound, false);
  assert.equal(parsePrefs(null, 'true').kinds.finished.sound, true);
  assert.equal(parsePrefs(JSON.stringify({}), 'false').kinds.finished.sound, true);
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
  p.kinds.ci.sound = false;
  assert.equal(allowed(p, 'ci', 'sound', noon), false);
  assert.equal(allowed(p, 'ci', 'system', noon), true);
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

test('a notification belongs to a kind', () => {
  assert.equal(kindOf({ kind: 'question' }), 'question');
  assert.equal(kindOf({ kind: 'finished', status: 'done' }), 'finished');
  assert.equal(kindOf({ kind: 'finished', status: 'failed' }), 'error');
  assert.equal(kindOf({ kind: 'finished', status: 'blocked' }), 'error');
  assert.equal(kindOf({ kind: 'media' }), 'finished');
});
