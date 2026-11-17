import assert from 'node:assert/strict';
import { test } from 'node:test';
import { digitPick, dropComposerFocus, isNewTaskShortcut, requestComposerFocus, requestSessionFocus, takeComposerFocus, taskTargets, type TaskTarget } from './newTask.ts';

const projects: TaskTarget[] = [
  { name: 'alpha', label: 'Alpha' },
  { name: 'pawly', label: 'Pawly app', lastAt: '2026-10-14T10:00:00Z' },
  { name: 'hatch', label: 'Hatch', lastAt: '2026-10-15T09:00:00Z' },
  { name: 'zeta', label: 'Zeta' },
];
const home: TaskTarget = { name: '', label: 'Home (no project)', home: true };

test('taskTargets: recent chats first, then the sidebar order, then Home', () => {
  assert.deepEqual(
    taskTargets(projects, home, '').map((t) => t.name || 'home'),
    ['hatch', 'pawly', 'alpha', 'zeta', 'home'],
  );
});

test('taskTargets: a search keeps what holds every word', () => {
  assert.deepEqual(taskTargets(projects, home, 'PAW app').map((t) => t.name), ['pawly']);
  assert.deepEqual(taskTargets(projects, home, 'project').map((t) => t.name || 'home'), ['home']);
  assert.deepEqual(taskTargets(projects, home, 'nothing'), []);
});

test('digitPick: a digit picks, with nothing typed or with Alt', () => {
  const list = taskTargets(projects, home, '');
  assert.equal(digitPick(list, '1', '', false)?.name, 'hatch');
  assert.equal(digitPick(list, '5', '', false)?.home, true);
  assert.equal(digitPick(list, '6', '', false), undefined);
  assert.equal(digitPick(list, '2', 'a', false), undefined);
  assert.equal(digitPick(list, '2', 'a', true)?.name, 'pawly');
  assert.equal(digitPick(list, '0', '', false), undefined);
  assert.equal(digitPick(list, 'a', '', false), undefined);
});

test('isNewTaskShortcut: Ctrl+N or ⌘N, not in a terminal', () => {
  const key = (over: Partial<KeyboardEvent>) => ({ altKey: false, shiftKey: false, ctrlKey: false, metaKey: false, key: 'n', target: null, ...over }) as KeyboardEvent;
  assert.equal(isNewTaskShortcut(key({ ctrlKey: true })), true);
  assert.equal(isNewTaskShortcut(key({ metaKey: true, key: 'N' })), true);
  assert.equal(isNewTaskShortcut(key({})), false);
  assert.equal(isNewTaskShortcut(key({ ctrlKey: true, shiftKey: true })), false);
  const inTerminal = { closest: (s: string) => (s === '.xterm' ? {} : null) } as unknown as EventTarget;
  assert.equal(isNewTaskShortcut(key({ ctrlKey: true, target: inTerminal })), false);
});

test('takeComposerFocus: the asked-for chat, once, while it is fresh', () => {
  requestComposerFocus('pawly/lead', 1000);
  assert.equal(takeComposerFocus('other/lead', 1100), false);
  assert.equal(takeComposerFocus('pawly/lead', 1100), true);
  assert.equal(takeComposerFocus('pawly/lead', 1200), false);
  requestComposerFocus('pawly/lead', 1000);
  assert.equal(takeComposerFocus('pawly/lead', 62_000), false);
});

test("a session asked for is that one, even the main one, not the one you're leaving", () => {
  requestSessionFocus('web/lead', 1000);
  assert.ok(!takeComposerFocus('web/lead.prod', 1001));
  assert.ok(takeComposerFocus('web/lead', 1002));
});

test('a chat slow to show still takes the focus, unless you did something else first', () => {
  requestComposerFocus('pawly/lead.new', 1000, 500);
  dropComposerFocus(400); // the click that asked
  assert.ok(takeComposerFocus('pawly/lead.new', 20_000));
  requestComposerFocus('pawly/lead.new', 1000, 500);
  dropComposerFocus(600); // a click or a key since
  assert.ok(!takeComposerFocus('pawly/lead.new', 1100));
});

test('a request for a project chat is taken by the session on screen', () => {
  requestComposerFocus('web/lead', 1000);
  assert.ok(!takeComposerFocus('api/lead.prod', 1001), 'another project');
  assert.ok(takeComposerFocus('web/lead.prod', 1002));
  requestComposerFocus('web/lead.prod', 2000);
  assert.ok(!takeComposerFocus('web/lead', 2001), 'a session asked for is that one');
  assert.ok(takeComposerFocus('web/lead.prod', 2002));
});
