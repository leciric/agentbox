import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isRemoving, removeAgent } from './removing.ts';

test('an agent reads removing while its destroy runs, once, and not after, however it ends', async () => {
  let finish!: () => void;
  const running = removeAgent('pawly/agent-1', () => new Promise<void>((resolve) => (finish = resolve)));
  assert.equal(isRemoving('pawly/agent-1'), true);
  assert.equal(await removeAgent('pawly/agent-1', async () => {}), 'busy');
  finish();
  assert.equal(await running, 'done');
  assert.equal(isRemoving('pawly/agent-1'), false);
  await assert.rejects(removeAgent('pawly/agent-2', async () => Promise.reject(new Error('no'))), /no/);
  assert.equal(isRemoving('pawly/agent-2'), false);
});
