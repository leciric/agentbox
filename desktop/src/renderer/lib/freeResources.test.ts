// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { VMPower } from '../../preload';
import type * as T from '../../shared/api';
import { coresText, freed, freeMode, freeTargets, progress, restartable, whoText } from './freeResources.ts';

const GiB = 1024 ** 3;
const agent = (name: string, state: string, chat?: string, title = '') => ({ ref: `p/${name}`, name, title, state, chat }) as T.Agent;
const vm = (state: VMPower['state'], granted = 12 * GiB): VMPower => ({ state, memoryUsed: 7 * GiB, memoryGranted: granted, memoryCap: 24 * GiB, cpus: 8 });

const agents = [
  agent('a1', 'running', 'ready'),
  agent('a2', 'running', 'running', 'Fix the login page'),
  agent('a3', 'paused'),
  agent('a4', 'stopped'),
  agent('a5', 'running', 'waiting'),
  agent('a6', 'initializing'),
];

test('the targets are the running and paused agents, the ones being cut off first', () => {
  assert.deepEqual(
    freeTargets(agents).map((t) => [t.ref, t.label, t.doing]),
    [
      ['p/a2', 'Fix the login page', 'working'],
      ['p/a5', 'a5', 'asking'],
      ['p/a1', 'a1', 'idle'],
      ['p/a3', 'a3', 'paused'],
    ],
  );
});

test('the confirmation says who is mid-work', () => {
  assert.equal(whoText(freeTargets(agents)), '1 is working and 1 is waiting on you');
  assert.equal(whoText(freeTargets([agent('a1', 'running', 'running'), agent('a2', 'running', 'running')])), '2 are working');
  assert.equal(whoText(freeTargets([agent('a1', 'running', 'ready')])), null);
});

test('host mode: free while anything runs, start when there is something to bring back, else idle', () => {
  assert.equal(freeMode(agents, null, []), 'free');
  const allStopped = [agent('a1', 'stopped'), agent('a2', 'stopped')];
  assert.equal(freeMode(allStopped, null, ['p/a1']), 'start');
  assert.equal(freeMode(allStopped, null, []), 'idle');
  assert.equal(freeMode(allStopped, null, ['p/gone']), 'idle', 'an agent destroyed since is nothing to start');
});

test('VM mode follows the VM: free while it is up, even with no agent running; start when off', () => {
  assert.equal(freeMode([], vm('running'), []), 'free');
  assert.equal(freeMode([], vm('paused'), []), 'free');
  assert.equal(freeMode([], vm('off'), []), 'start');
  assert.equal(freeMode([], vm('starting'), []), 'busy');
  assert.equal(freeMode([], vm('stopping'), []), 'busy');
});

test('start brings back only the stopped agents still there', () => {
  assert.deepEqual(restartable(agents, ['p/a4', 'p/a1', 'p/gone']), ['p/a4']);
});

test('progress counts the targets the daemon now reports stopped', () => {
  const targets = freeTargets(agents);
  assert.deepEqual(progress(targets, agents), { done: 0, total: 4 });
  const later = agents.map((a) => (a.name === 'a1' || a.name === 'a3' ? { ...a, state: 'stopped' } : a));
  assert.deepEqual(progress(targets, later), { done: 2, total: 4 });
});

test('what was freed: the agents, or in VM mode everything the VM held', () => {
  const result: T.StopAgentsResult = { stopped: [], freedMemory: 5 * GiB, freedCPU: 320, hostMemoryBefore: 20 * GiB, hostMemoryAfter: 14 * GiB };
  assert.deepEqual(freed(result, null, false), { memory: 5 * GiB, cpu: 320, hostBefore: 20 * GiB, hostAfter: 14 * GiB });
  assert.deepEqual(freed(result, vm('running'), true), { memory: 12 * GiB, cpu: 320, vm: 12 * GiB });
  assert.deepEqual(freed(result, vm('running'), false), { memory: 5 * GiB, cpu: 320, hostBefore: 20 * GiB, hostAfter: 14 * GiB }, 'a VM that failed to stop gave back nothing more');
});

test('cores read the way a person says them', () => {
  assert.equal(coresText(320), '3.2 cores');
  assert.equal(coresText(100), '1 core');
  assert.equal(coresText(40), '0.4 of a core');
  assert.equal(coresText(1), 'no CPU');
});
