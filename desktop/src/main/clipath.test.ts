// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { firstCli } from './clipath.ts';

const app = 'C:\\Users\\ana\\AppData\\Local\\Programs\\AgentBox\\AgentBox.exe';

test('the app itself is never taken for the agentbox command', () => {
  // Started from its shortcut, the app's folder is the current directory, which where.exe searches first.
  const out = 'C:\\Users\\ana\\AppData\\Local\\Programs\\AgentBox\\AgentBox.exe\r\nC:\\Users\\ana\\AppData\\Local\\AgentBox\\bin\\agentbox.exe\r\n';
  assert.equal(firstCli(out, app), 'C:\\Users\\ana\\AppData\\Local\\AgentBox\\bin\\agentbox.exe');
  assert.equal(firstCli('c:/users/ana/appdata/local/programs/agentbox/AGENTBOX.EXE\r\n', app), null);
});

test('where.exe finding nothing, or one agentbox, says so', () => {
  assert.equal(firstCli(null, app), null);
  assert.equal(firstCli('', app), null);
  assert.equal(firstCli('C:\\tools\\agentbox.exe', app), 'C:\\tools\\agentbox.exe');
});
