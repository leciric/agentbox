// Run with `npm test` (node's own test runner, which strips the types).
//
// There is no localStorage outside a browser, so this exercises the module's
// in-memory cache: load() and scheduleWrite() both swallow the ReferenceError
// and fall back silently, which is the same thing a browser that refuses
// storage does.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { composed, draftProjects, getDraft, leadProject, projectsWithDraft, setDraft, setDraftAttachments } from './drafts.ts';

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

test('a draft maps to its project only for a lead chat, and whitespace or nothing does not count', () => {
  assert.equal(leadProject('pawly'), 'pawly');
  assert.equal(leadProject('pawly/lead'), 'pawly');
  assert.equal(leadProject('pawly/agent-01'), undefined);
  const out = projectsWithDraft([
    ['a/lead', 'hello'],
    ['b/lead', '  \n '],
    ['c/agent-01', 'for an agent'],
    ['d', ''],
    ['e', 'bare project ref'],
  ]);
  assert.deepEqual([...out].sort(), ['a', 'e']);
});

test('an attachment alone is a draft', () => {
  assert.deepEqual([...projectsWithDraft([['a/lead', '']], new Map([['a/lead', 1], ['b/lead', 0], ['c/agent-01', 2]]))], ['a']);
});

test('draftProjects follows typing, clearing and attaching, and is stable between changes', () => {
  setDraft('live/lead', 'hi');
  const first = draftProjects();
  assert.ok(first.has('live'));
  assert.equal(draftProjects(), first);
  setDraft('live/lead', 'hi there');
  assert.equal(draftProjects(), first);
  setDraft('live/lead', '   ');
  assert.ok(!draftProjects().has('live'));
  setDraftAttachments('live/lead', 1);
  assert.ok(draftProjects().has('live'));
  setDraftAttachments('live/lead', 0);
  assert.ok(!draftProjects().has('live'));
});
