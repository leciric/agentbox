// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { closeSearch, openSearch, searchState, sinceTime, toggleSearch } from './searchOpen.ts';

test('sinceTime reaches back a day, a week or a month, and not at all for any time', () => {
  const now = Date.UTC(2026, 9, 11, 12);
  assert.equal(sinceTime('day', now), '2026-10-10T12:00:00.000Z');
  assert.equal(sinceTime('week', now), '2026-10-04T12:00:00.000Z');
  assert.equal(sinceTime('month', now), '2026-09-11T12:00:00.000Z');
  assert.equal(sinceTime('', now), undefined);
  assert.equal(sinceTime(undefined, now), undefined);
});

test('openSearch opens on a scope, afresh each time; toggleSearch closes it and opens it unscoped', () => {
  const first = searchState().opening;
  openSearch({ query: 'retention', kind: 'chats' });
  openSearch({ query: 'retention', kind: 'chats' });
  assert.deepEqual(searchState(), { open: true, scope: { query: 'retention', kind: 'chats' }, opening: first + 2 });
  toggleSearch();
  assert.equal(searchState().open, false);
  toggleSearch();
  assert.deepEqual(searchState(), { open: true, scope: {}, opening: first + 3 });
  closeSearch();
  assert.equal(searchState().open, false);
});
