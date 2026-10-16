// Run with `npm test` (node's own test runner, which strips the types).
//
// There is no localStorage outside a browser, so this exercises the module's
// in-memory cache: load() and scheduleWrite() both swallow the ReferenceError
// and fall back silently, which is the same thing a browser that refuses
// storage does.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { composed, getDraft, setDraft } from './drafts.ts';

test('a ref with nothing typed has an empty draft', () => {
  assert.equal(getDraft('agent-never-seen'), '');
});

test('setDraft is read back by getDraft', () => {
  setDraft('agent-01', 'hello there');
  assert.equal(getDraft('agent-01'), 'hello there');
});

test('setting an empty draft clears it', () => {
  setDraft('agent-02', 'something');
  assert.equal(getDraft('agent-02'), 'something');
  setDraft('agent-02', '');
  assert.equal(getDraft('agent-02'), '');
});

test('text composed into a chat goes before what was typed there, not over it', () => {
  assert.equal(composed('Ask @pawly’s lead ', ''), 'Ask @pawly’s lead ');
  assert.equal(composed('Ask @pawly’s lead ', '  about the release notes'), 'Ask @pawly’s lead about the release notes');
});
