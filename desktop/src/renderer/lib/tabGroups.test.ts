import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { sortTabs } from './sessions.ts';
import { dropAt, groupSum, jumpTo, moveBefore, moveGroupBefore, rowSessions, tabRow, withGroup, type DropBox } from './tabGroups.ts';

const session = (id: string, over: Partial<T.LeadSession> = {}): T.LeadSession => ({
  project: 'web',
  id,
  ref: `web/lead.${id}`,
  topic: id,
  color: '',
  chat: 'off',
  agents: 0,
  createdAt: '2026-11-01T00:00:00Z',
  activeAt: '2026-11-01T00:00:00Z',
  ...over,
});
const group = (id: string, over: Partial<T.LeadSessionGroup> = {}): T.LeadSessionGroup => ({ project: 'web', id, name: id, color: 'sky', collapsed: true, position: 0, ...over });
const ids = (list: readonly T.LeadSession[]) => list.map((s) => s.id);

const ui = group('ui');
const win = group('win', { color: 'peach' });
const empty = group('empty');
const groups = [ui, win, empty];
// As the daemon would answer them, and shuffled.
const rel = session('rel', { pinned: true });
const tabs = session('tabs', { group: 'ui' });
const side = session('side', { group: 'ui' });
const wsl = session('wsl', { group: 'win' });
const oauth = session('oauth');
const stray = session('stray', { group: 'gone' });
const shuffled = [oauth, wsl, tabs, rel, stray, side];

test('the row is the pinned tabs, then each group with open sessions, then the loose tabs', () => {
  const row = tabRow(shuffled, groups);
  assert.deepEqual(
    row.map((it) => (it.kind === 'tab' ? it.session.id : `[${it.group.id}: ${ids(it.sessions).join(' ')}]`)),
    ['rel', '[ui: tabs side]', '[win: wsl]', 'oauth', 'stray'],
    'a group none of whose sessions is open shows nothing, and a session of a group that is gone is loose',
  );
  assert.deepEqual(ids(rowSessions(row)), ['rel', 'tabs', 'side', 'wsl', 'oauth', 'stray'], 'the keys walk every session, those in menus too');
  assert.deepEqual(ids(sortTabs(shuffled, groups)), ['rel', 'tabs', 'side', 'wsl', 'oauth', 'stray']);
  // A pinned session says a group, as one from an older daemon might: it is pinned.
  assert.deepEqual(ids(rowSessions(tabRow([session('p', { pinned: true, group: 'ui' })], groups))), ['p']);
});

test('Mod+n counts the row: a group goes to its session on screen, else the one last shown, else its first', () => {
  const row = tabRow(shuffled, groups);
  assert.equal(jumpTo(row, 1, 'oauth')?.id, 'rel');
  assert.equal(jumpTo(row, 2, 'oauth')?.id, 'tabs');
  assert.equal(jumpTo(row, 2, 'oauth', { ui: 'side' })?.id, 'side');
  assert.equal(jumpTo(row, 2, 'side', { ui: 'tabs' })?.id, 'side');
  assert.equal(jumpTo(row, 2, 'oauth', { ui: 'closed-since' })?.id, 'tabs');
  assert.equal(jumpTo(row, 4, 'rel')?.id, 'oauth');
  assert.equal(jumpTo(row, 9, 'rel'), undefined);
});

test("a group's button sums what waits in it, but for the session on screen", () => {
  const needs = session('n', { status: 'needsYou', needsYou: 2 });
  const needs1 = session('n1', { status: 'needsYou' });
  const failed = session('f', { status: 'failed' });
  const working = session('w', { status: 'working' });
  const unread = session('u', { unread: true, last: { kind: 'assistant', text: 'hi', at: '2026-11-02T00:00:00Z' } });
  const calm = { needsYou: 0, failed: false, working: false, unread: false };
  assert.deepEqual(groupSum([needs, needs1, failed, working, unread], undefined, {}), { needsYou: 3, failed: true, working: false, unread: false });
  assert.deepEqual(groupSum([working, unread], undefined, {}), { ...calm, working: true });
  assert.deepEqual(groupSum([unread], undefined, {}), { ...calm, unread: true });
  assert.deepEqual(groupSum([unread], undefined, { [unread.ref]: '2026-11-03T00:00:00Z' }), calm, 'seen here since');
  assert.deepEqual(groupSum([needs, working], 'n', {}), { ...calm, working: true }, 'the session on screen has its own mark');
});

// Three tabs and a group's button, 100 wide each, side by side.
const boxes: DropBox[] = [
  { kind: 'tab', id: 'rel', left: 0, width: 100, pinned: true },
  { kind: 'group', id: 'ui', left: 100, width: 100 },
  { kind: 'tab', id: 'oauth', left: 200, width: 90 },
  { kind: 'tab', id: 'docs', left: 300, width: 90, group: 'win' },
];

test('dropping a tab: on a group joins it, mid-tab merges or joins, elsewhere it moves', () => {
  const loose = { kind: 'tab', id: 'x' } as const;
  assert.deepEqual(dropAt(boxes, 150, loose), { kind: 'into', group: 'ui' });
  assert.deepEqual(dropAt(boxes, 245, loose), { kind: 'merge', with: 'oauth' });
  assert.deepEqual(dropAt(boxes, 345, loose), { kind: 'into', group: 'win' }, "the middle of a spread group's tab");
  assert.deepEqual(dropAt(boxes, 205, loose), { kind: 'gap', before: 'oauth' }, "a tab's edge is a gap");
  assert.deepEqual(dropAt(boxes, 285, loose), { kind: 'gap', before: 'docs' });
  assert.deepEqual(dropAt(boxes, 500, loose), { kind: 'gap', before: undefined }, 'after the last');
  assert.deepEqual(dropAt(boxes, 45, loose), { kind: 'gap', before: 'rel' }, 'a pinned tab is no group to make');
  assert.deepEqual(dropAt(boxes, 150, { kind: 'tab', id: 'p', pinned: true }), { kind: 'gap', before: 'oauth' }, 'a pinned tab joins nothing');
  assert.deepEqual(dropAt(boxes, 150, { kind: 'tab', id: 'x', group: 'ui' }), { kind: 'gap', before: 'oauth' }, 'its own group');
  assert.deepEqual(dropAt(boxes, 245, { kind: 'tab', id: 'oauth' }), { kind: 'gap', before: 'docs' }, 'onto itself');
});

test('dropping a group moves it among the groups only', () => {
  const two: DropBox[] = [...boxes, { kind: 'group', id: 'win', left: 400, width: 100 }];
  assert.deepEqual(dropAt(two, 120, { kind: 'group', id: 'win' }), { kind: 'gap', before: 'ui' });
  assert.deepEqual(dropAt(two, 470, { kind: 'group', id: 'ui' }), { kind: 'gap', before: undefined });
  assert.deepEqual(
    moveGroupBefore(groups, 'win', 'ui').map((g) => g.id),
    ['win', 'ui', 'empty'],
  );
  assert.deepEqual(
    moveGroupBefore(groups, 'ui', undefined).map((g) => g.id),
    ['win', 'empty', 'ui'],
  );
  assert.deepEqual(
    moveGroupBefore(groups, 'gone', 'ui').map((g) => g.id),
    ['ui', 'win', 'empty'],
  );
});

test('a tab moves within its own lot, and into or out of a group last', () => {
  const sorted = sortTabs(shuffled, groups);
  assert.deepEqual(ids(moveBefore(sorted, 'side', 'tabs', groups)), ['rel', 'side', 'tabs', 'wsl', 'oauth', 'stray']);
  assert.deepEqual(ids(moveBefore(sorted, 'oauth', 'rel', groups)), ['rel', 'tabs', 'side', 'wsl', 'oauth', 'stray'], 'not among the pinned');
  assert.deepEqual(ids(moveBefore(sorted, 'oauth', undefined, groups)), ['rel', 'tabs', 'side', 'wsl', 'stray', 'oauth']);
  const joined = withGroup(sorted, 'oauth', 'ui', groups);
  assert.deepEqual(ids(joined), ['rel', 'tabs', 'side', 'oauth', 'wsl', 'stray']);
  assert.equal(joined.find((s) => s.id === 'oauth')?.group, 'ui');
  const left = withGroup(sorted, 'tabs', '', groups);
  assert.deepEqual(ids(left), ['rel', 'side', 'wsl', 'oauth', 'stray', 'tabs']);
  assert.equal(left.find((s) => s.id === 'tabs')?.group, undefined);
  assert.deepEqual(ids(withGroup(sorted, 'rel', 'ui', groups)), ids(sorted), 'a pinned tab joins nothing');
});
