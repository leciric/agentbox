// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { VMPower } from '../../preload';
import type * as T from '../../shared/api';
import { machineStatus } from './machineStatus.ts';

const GiB = 1024 ** 3;
const vm = (state: VMPower['state'], used = 5 * GiB, extra: Partial<VMPower> = {}): VMPower => ({
  state,
  memoryUsed: used,
  memoryGranted: 12 * GiB,
  memoryCap: 16 * GiB,
  cpus: 6,
  ...extra,
});
const host = (used: number, total = 32 * GiB) => ({ cpu: 10, cores: 8, memUsed: used, memTotal: total }) as T.HostUsage;
const connected = { state: 'connected' } as const;

test("a running VM's memory is what's in use against its cap, not what it's been granted", () => {
  const s = machineStatus(vm('running', 8 * GiB), connected);
  assert.equal(s.kind, 'running');
  assert.equal(s.tone, 'ok');
  assert.deepEqual(s.memory, { used: 8 * GiB, total: 16 * GiB, percent: 50 });
});

test('memory past 85% of the cap is tight', () => {
  assert.equal(machineStatus(vm('running', 14 * GiB), connected).kind, 'memory');
  assert.equal(machineStatus(vm('running', 13 * GiB), connected).kind, 'running');
});

test("the VM's power state comes before the connection, which can't be up without it", () => {
  const lost = { state: 'disconnected' } as const;
  assert.equal(machineStatus(vm('off'), lost).kind, 'off');
  assert.equal(machineStatus(vm('off'), lost).tone, 'off');
  assert.equal(machineStatus(vm('off', 0, { error: 'no KVM' }), lost).tone, 'bad');
  assert.equal(machineStatus(vm('paused'), lost).kind, 'paused');
  assert.equal(machineStatus(vm('starting'), lost).kind, 'busy');
  assert.equal(machineStatus(vm('stopping'), lost).memory, null);
  assert.equal(machineStatus(vm('running'), lost).kind, 'offline');
  assert.equal(machineStatus(vm('running'), { state: 'connecting' }).kind, 'connecting');
});

test("host mode reads this computer's memory", () => {
  assert.equal(machineStatus(null, connected, host(30 * GiB)).kind, 'memory');
  assert.equal(machineStatus(null, connected, host(8 * GiB)).memory?.percent, 25);
  assert.equal(machineStatus(null, connected).memory, null);
  assert.equal(machineStatus(null, { state: 'disconnected' }, host(30 * GiB)).kind, 'offline');
});
