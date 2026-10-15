import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isSettingsShortcut, isSheetShortcut, nextRegion } from './shortcuts.ts';

const key = (k: string, over: Partial<KeyboardEvent> = {}) => ({ key: k, altKey: false, shiftKey: false, ctrlKey: true, metaKey: false, target: null, ...over }) as KeyboardEvent;

test('Ctrl+/ opens the sheet and Ctrl+, Settings, not in a terminal', () => {
  assert.ok(isSheetShortcut(key('/')));
  assert.ok(isSheetShortcut(key('/', { ctrlKey: false, metaKey: true })));
  assert.ok(!isSheetShortcut(key('/', { ctrlKey: false })));
  assert.ok(!isSheetShortcut(key('/', { target: { closest: () => ({}) } as unknown as EventTarget })));
  assert.ok(isSettingsShortcut(key(',')));
  assert.ok(!isSettingsShortcut(key(',', { altKey: true })));
});

test('F6 goes round the places on screen, and back with Shift', () => {
  assert.equal(nextRegion(-1, [true, true, true], false), 0);
  assert.equal(nextRegion(0, [true, true, true], false), 1);
  assert.equal(nextRegion(2, [true, true, true], false), 0);
  assert.equal(nextRegion(1, [true, true, false], false), 0);
  assert.equal(nextRegion(0, [true, true, false], true), 1);
  assert.equal(nextRegion(-1, [false, true, true], false), 1);
  assert.equal(nextRegion(-1, [true, true, true], true), 2);
  assert.equal(nextRegion(-1, [false, false, false], false), -1);
});
