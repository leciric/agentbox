import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isBackToLeadShortcut, isSettingsShortcut, isSheetShortcut, nextRegion } from './shortcuts.ts';

const key = (k: string, over: Partial<KeyboardEvent> = {}) => ({ key: k, altKey: false, shiftKey: false, ctrlKey: true, metaKey: false, target: null, ...over }) as KeyboardEvent;

test('Ctrl+/ opens the sheet and Ctrl+, Settings, not in a terminal', () => {
  assert.ok(isSheetShortcut(key('/')));
  assert.ok(isSheetShortcut(key('/', { ctrlKey: false, metaKey: true })));
  assert.ok(!isSheetShortcut(key('/', { ctrlKey: false })));
  assert.ok(!isSheetShortcut(key('/', { target: { closest: () => ({}) } as unknown as EventTarget })));
  assert.ok(isSettingsShortcut(key(',')));
  assert.ok(!isSettingsShortcut(key(',', { altKey: true })));
});

test('Alt+← goes back to the lead, but not from a terminal, a desktop, or a Mac’s text field', () => {
  const alt = (over: Partial<KeyboardEvent> = {}) => key('ArrowLeft', { ctrlKey: false, altKey: true, ...over });
  const inside = (match: string) => ({ closest: (s: string) => (s.includes(match) ? {} : null) }) as unknown as EventTarget;
  assert.ok(isBackToLeadShortcut(alt(), false));
  assert.ok(isBackToLeadShortcut(alt(), true));
  assert.ok(!isBackToLeadShortcut(alt({ shiftKey: true }), false));
  assert.ok(!isBackToLeadShortcut(alt({ ctrlKey: true }), false));
  assert.ok(!isBackToLeadShortcut(alt({ key: 'ArrowRight' }), false));
  assert.ok(!isBackToLeadShortcut(alt({ target: inside('.xterm') }), false));
  assert.ok(!isBackToLeadShortcut(alt({ target: inside('canvas') }), false));
  assert.ok(isBackToLeadShortcut(alt({ target: inside('textarea') }), false));
  assert.ok(!isBackToLeadShortcut(alt({ target: inside('textarea') }), true));
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
