import assert from 'node:assert/strict';
import { test } from 'node:test';
import { monitorScreen, moodSt, paceWindow, typingPace } from './monitor.ts';

test('each state has its own screen, and the live desktop shows unless it needs you', () => {
  assert.equal(monitorScreen('working'), 'working');
  assert.equal(monitorScreen('needs'), 'needs');
  assert.equal(monitorScreen('error'), 'error');
  assert.equal(monitorScreen('review'), 'review');
  assert.equal(monitorScreen('done'), 'idle');
  assert.equal(monitorScreen('working', { live: true }), 'desktop');
  assert.equal(monitorScreen('done', { live: true }), 'desktop');
  assert.equal(monitorScreen('review', { live: true }), 'desktop');
  // An agent using its desktop that asks, or fails, shows that instead.
  assert.equal(monitorScreen('needs', { live: true }), 'needs');
  assert.equal(monitorScreen('error', { live: true }), 'error');
  // A stopped machine has no desktop to show.
  assert.equal(monitorScreen('done', { live: true, running: false }), 'idle');
});

test('the lines scroll faster the more tool calls it made in the last two minutes', () => {
  const now = Date.parse('2026-10-30T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();
  assert.equal(typingPace([], now), 3.2);
  assert.equal(typingPace([ago(1_000)], now), 3.08);
  assert.equal(typingPace(Array.from({ length: 10 }, (_, i) => ago(i * 1_000)), now), 2);
  assert.equal(typingPace(Array.from({ length: 40 }, (_, i) => ago(i * 1_000)), now), 0.8);
  // Older than the window, from the future or unreadable: not counted.
  assert.equal(typingPace([ago(paceWindow + 1), ago(-5_000), 'soon'], now), 3.2);
  const slow = typingPace([ago(1_000)], now);
  const fast = typingPace([ago(1_000), ago(2_000), ago(3_000)], now);
  assert.ok(fast < slow);
});

test('the phone reads the state from the avatar mood and the review', () => {
  assert.equal(moodSt('asking'), 'needs');
  assert.equal(moodSt('asking', true), 'needs');
  assert.equal(moodSt('error'), 'error');
  assert.equal(moodSt('error', true), 'review');
  assert.equal(moodSt('idle', true), 'review');
  assert.equal(moodSt('working', true), 'working');
  assert.equal(moodSt('working'), 'working');
  assert.equal(moodSt('idle'), 'done');
  assert.equal(moodSt('sleeping'), 'done');
});
