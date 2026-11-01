// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { browserInUse, contextualAgentTabs, hiddenTabs } from './agentTabs.ts';

const nothing = { browser: false, android: false, media: false, snapshots: false };

test('an idle agent hides every contextual tab', () => {
  assert.deepEqual(hiddenTabs(contextualAgentTabs, nothing, 'chat'), ['browser', 'android', 'media', 'snapshots']);
});

test('a tab with something in it shows', () => {
  assert.deepEqual(hiddenTabs(contextualAgentTabs, { ...nothing, media: true, snapshots: true }, 'chat'), ['browser', 'android']);
});

test('the open tab stays though it is empty', () => {
  assert.deepEqual(hiddenTabs(contextualAgentTabs, nothing, 'android'), ['browser', 'media', 'snapshots']);
});

test('a browser with only a blank page is not in use', () => {
  assert.equal(browserInUse(undefined), false);
  assert.equal(browserInUse({ display: true, running: true, pages: [{ id: '1', url: 'about:blank', title: '' }] }), false);
  assert.equal(browserInUse({ display: true, running: false, pages: [{ id: '1', url: 'http://localhost:3000/', title: '' }] }), false);
  assert.equal(browserInUse({ display: true, running: true, pages: [{ id: '1', url: 'http://localhost:3000/', title: 'App' }] }), true);
});
