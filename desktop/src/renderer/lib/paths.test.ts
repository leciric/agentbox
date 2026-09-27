// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { joinPath, parentOf } from './paths.ts';

test('a name goes under its parent, whatever slashes the parent ends in', () => {
  assert.equal(joinPath('/home/ana/code', 'my-app'), '/home/ana/code/my-app');
  assert.equal(joinPath('/home/ana/code//', 'my-app'), '/home/ana/code/my-app');
  assert.equal(joinPath('/', 'my-app'), '/my-app');
});

test('there is no path until both halves are there', () => {
  assert.equal(joinPath('', 'my-app'), '');
  assert.equal(joinPath('/home/ana/code', ''), '');
});

test("a project's parent folder is where the new one starts", () => {
  assert.equal(parentOf('/home/ana/code/agentbox'), '/home/ana/code');
  assert.equal(parentOf('/home/ana/code/agentbox/'), '/home/ana/code');
  assert.equal(parentOf('/agentbox'), '/');
});
