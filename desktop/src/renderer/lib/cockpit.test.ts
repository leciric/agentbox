// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { feedOf, fitWall, onlyShown, shownHidden, shownProjects, stagedOf, toggleShown, visualOf } from './cockpit.ts';

const item = (id: string, kind: string, at: string, extra: Partial<T.ChatItem> = {}): T.ChatItem => ({ id, turn: 't', kind, text: id, createdAt: at, updatedAt: at, ...extra });

test('the feed merges the leads’ messages by time, words only, the latest of each', () => {
  const rows = feedOf(
    [
      { project: 'a', ref: 'a/lead', items: [item('a1', 'user', '2026-01-01T10:00:00Z'), item('a-tool', 'tool', '2026-01-01T10:01:00Z'), item('a2', 'assistant', '2026-01-01T10:05:00Z')] },
      {
        project: 'b',
        ref: 'b/lead',
        items: [item('b1', 'assistant', '2026-01-01T10:02:00Z'), item('b-sub', 'assistant', '2026-01-01T10:03:00Z', { parent: 'x' }), item('b-empty', 'assistant', '2026-01-01T10:04:00Z', { text: ' ' })],
      },
      { project: 'c', ref: 'c/lead', items: undefined },
    ],
    12,
  );
  assert.deepEqual(
    rows.map((r) => r.item.id),
    ['a1', 'b1', 'a2'],
  );
  const few = feedOf([{ project: 'a', ref: 'a/lead', items: [item('1', 'user', '1'), item('2', 'assistant', '2'), item('3', 'user', '3')] }], 2);
  assert.deepEqual(
    few.map((r) => r.item.id),
    ['2', '3'],
  );
});

test('the filter keeps at least one project, and Only toggles back to all', () => {
  const all = ['a', 'b', 'c'];
  let hidden = toggleShown(all, new Set(), 'a');
  assert.deepEqual(shownProjects(all, hidden), ['b', 'c']);
  hidden = toggleShown(all, hidden, 'b');
  assert.deepEqual(shownProjects(all, hidden), ['c']);
  hidden = toggleShown(all, hidden, 'c');
  assert.deepEqual(shownProjects(all, hidden), ['c'], 'the last one stays');
  hidden = onlyShown(all, new Set(), 'b');
  assert.deepEqual(shownProjects(all, hidden), ['b']);
  assert.deepEqual(shownProjects(all, onlyShown(all, hidden, 'b')), all);
  // Hiding projects that are gone shows every one there is.
  assert.deepEqual(shownProjects(['d'], new Set(['d'])), ['d']);
});

test('a phone comes before the desktop on the stage, and nothing visual is the terminal', () => {
  assert.equal(visualOf({ android: { running: true }, display: true }), 'android');
  assert.equal(visualOf({ ios: { devices: [{ udid: '1', name: 'x', runtime: 'r', state: 'Booted' }] }, display: true }), 'ios');
  assert.equal(visualOf({ ios: { devices: [{ udid: '1', name: 'x', runtime: 'r', state: 'Shutdown' }] }, display: true }), 'display');
  assert.equal(visualOf({}), 'none');
});

test('the stage keeps the chosen agent, else who needs you first', () => {
  const desks = [
    { id: 'p/1', tone: 'working' },
    { id: 'p/2', tone: 'needs' },
    { id: 'p/3', tone: 'error' },
  ];
  assert.equal(stagedOf(desks, undefined)?.id, 'p/2');
  assert.equal(stagedOf(desks, 'p/1')?.id, 'p/1');
  assert.equal(stagedOf(desks, 'p/gone')?.id, 'p/2');
  assert.equal(stagedOf([], undefined), undefined);
});

test('the wall fits every agent at the biggest size, else scrolls at the smallest', () => {
  // One agent fills a box shaped like it.
  assert.equal(fitWall(1, 1600, 1100), 1);
  // Twelve agents on a 2000×1300 wall: four columns of three rows fit.
  assert.equal(fitWall(12, 2000, 1300), 4);
  // Forty don't fit at any size: as many 300px columns as there's room for.
  assert.equal(fitWall(40, 1000, 400), 3);
  assert.equal(fitWall(0, 1000, 400), 1);
});

test('a filter that shows every project after the visible one went away toggles what it shows', () => {
  // a and b hidden, c the only one shown; then c goes away.
  let hidden = toggleShown(['a', 'b', 'c'], toggleShown(['a', 'b', 'c'], new Set(), 'a'), 'b');
  assert.deepEqual(shownProjects(['a', 'b', 'c'], hidden), ['c']);
  const all = ['a', 'b'];
  assert.deepEqual(shownProjects(all, hidden), ['a', 'b'], 'both show');
  assert.deepEqual([...shownHidden(all, hidden)], []);
  // Clicking a, shown checked, hides a, not b.
  hidden = toggleShown(all, hidden, 'a');
  assert.deepEqual(shownProjects(all, hidden), ['b']);
  // Only, on the same stale choice, keeps the one clicked.
  assert.deepEqual(shownProjects(all, onlyShown(all, new Set(['a', 'b', 'c']), 'a')), ['a']);
  // A project gone since it was hidden doesn't count.
  assert.deepEqual([...shownHidden(['a', 'b'], new Set(['x', 'a']))], ['a']);
});
