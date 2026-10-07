// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { agentSizes, waitingLine } from './agentSize.ts';

test('the sizes are auto, light, normal and heavy, each with a tip that says what memory it sets aside, not a limit', () => {
  const sizes = agentSizes();
  assert.deepEqual(
    sizes.map((s) => s.value),
    ['', 'light', 'normal', 'heavy'],
  );
  assert.match(sizes[3].tip, /About 8 GB of memory/);
  for (const s of sizes) assert.doesNotMatch(s.tip, /limit/i);
});

test("auto lets the chat decide in the project's setting", () => {
  assert.match(agentSizes(true)[0].tip, /chat picks/);
});

test('the reason an agent waits drops its "queued:" prefix', () => {
  assert.equal(waitingLine('queued: 6 agents in 2 projects reserve 15 of 18 GB; starts when ~7 GB is free'), '6 agents in 2 projects reserve 15 of 18 GB; starts when ~7 GB is free');
  assert.equal(waitingLine(undefined), '');
});
