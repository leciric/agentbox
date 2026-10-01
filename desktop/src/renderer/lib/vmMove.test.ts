// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { HostSetupStatus, VMMigration } from '../../preload';
import { movePrompt } from './vmMove.ts';

const linux = { mode: 'host' as const, kvm: true, cores: 16, memory: 32 * 1024 ** 3, defaultCpus: 8, defaultMemoryCap: 24 * 1024 ** 3 };
const hostMode: HostSetupStatus = { pkexec: '/usr/bin/pkexec', user: 'leandro', running: false, resizing: false, vm: null, wsl: null, linux, chv: null };
const available: VMMigration = { state: 'available', projects: ['agentbox'], agents: ['agentbox/agent-1'] };

test('a host-mode installation is told once per version, until it moves', () => {
  assert.equal(movePrompt(hostMode, available, '0.11.0', null), true);
  // Later puts it off until the next update.
  assert.equal(movePrompt(hostMode, available, '0.11.0', '0.11.0'), false);
  assert.equal(movePrompt(hostMode, available, '0.11.1', '0.11.0'), true);
  // A move that stopped half-way is offered again, to carry on.
  assert.equal(movePrompt(hostMode, { state: 'started' }, '0.11.0', null), true);
});

test('nothing to prompt in the VM, after the move, or before the status is known', () => {
  for (const state of ['none', 'verified', 'removed'] as const) {
    assert.equal(movePrompt(hostMode, { state }, '0.11.0', null), false, state);
  }
  assert.equal(movePrompt({ ...hostMode, linux: { ...linux, mode: 'vm' } }, available, '0.11.0', null), false);
  assert.equal(movePrompt({ ...hostMode, linux: null }, available, '0.11.0', null), false); // a Mac, Windows
  assert.equal(movePrompt(undefined, available, '0.11.0', null), false);
  assert.equal(movePrompt(hostMode, null, '0.11.0', null), false);
  assert.equal(movePrompt(hostMode, available, undefined, null), false);
});
