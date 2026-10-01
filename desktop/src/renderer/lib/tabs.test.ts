// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { agentPlace, projectPlace } from './tabs.ts';

test('a project opens on its chat', () => {
  assert.deepEqual(projectPlace(undefined), { tab: 'chat', section: 'repository' });
  assert.deepEqual(projectPlace('nonsense'), { tab: 'chat', section: 'repository' });
});

test("the project's Agents tab is gone and lands on the chat", () => {
  assert.equal(projectPlace('agents').tab, 'chat');
});

test("a project's old tabs open their section of Settings", () => {
  assert.deepEqual(projectPlace('overview'), { tab: 'settings', section: 'repository' });
  for (const name of ['memory', 'tokens', 'secrets', 'connectors'] as const) {
    assert.deepEqual(projectPlace(name), { tab: 'settings', section: name });
  }
});

test("a project's remaining tabs and sections open as themselves", () => {
  for (const name of ['chat', 'tasks', 'pulls', 'media', 'settings'] as const) assert.equal(projectPlace(name).tab, name);
  assert.deepEqual(projectPlace('general'), { tab: 'settings', section: 'general' });
  assert.deepEqual(projectPlace('brief'), { tab: 'settings', section: 'brief' });
});

test("an agent's old tabs open their section of Settings", () => {
  assert.deepEqual(agentPlace('overview'), { tab: 'settings', section: 'machine' });
  assert.deepEqual(agentPlace('secrets'), { tab: 'settings', section: 'secrets' });
  assert.deepEqual(agentPlace('connectors'), { tab: 'settings', section: 'connectors' });
  assert.deepEqual(agentPlace('code'), { tab: 'settings', section: 'code' });
});

test("an agent's other tabs are kept, and an unknown one leaves the tab unset", () => {
  for (const name of ['chat', 'terminal', 'browser', 'android', 'media', 'snapshots'] as const) assert.equal(agentPlace(name).tab, name);
  assert.equal(agentPlace(undefined).tab, undefined);
  assert.equal(agentPlace('agents').tab, undefined);
});
