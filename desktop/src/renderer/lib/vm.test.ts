// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';

const calls: string[] = [];
let state = 'running';
(globalThis as unknown as { window: unknown }).window = {
  agentbox: {
    vm: {
      power: async () => ({ state }),
      act: async (action: string) => {
        calls.push(action);
        state = action === 'stop' ? 'off' : 'running';
        return { state };
      },
    },
  },
};
const { restartVM } = await import('./vm.ts');

test('restartVM stops, starts, and waits for the daemon to answer', async () => {
  calls.length = 0;
  const phases: string[] = [];
  let asked = 0;
  await restartVM(
    (p) => phases.push(p),
    async () => {
      if (++asked < 2) throw new Error('daemon not up');
    },
  );
  assert.deepEqual(calls, ['stop', 'start']);
  assert.deepEqual(phases, ['stopping', 'starting', 'waiting']);
  assert.equal(asked, 2);
});

test('restartVM only starts a VM that is already off', async () => {
  calls.length = 0;
  state = 'off';
  await restartVM(() => {}, async () => {});
  assert.deepEqual(calls, ['start']);
});

test('restartVM fails with the VM command\'s error', async () => {
  state = 'running';
  const bridge = (globalThis as unknown as { window: { agentbox: { vm: { act: (a: string) => Promise<unknown> } } } }).window.agentbox.vm;
  const act = bridge.act;
  bridge.act = async () => {
    throw new Error('vm stop failed: busy');
  };
  await assert.rejects(restartVM(() => {}, async () => {}), /busy/);
  bridge.act = act;
});
