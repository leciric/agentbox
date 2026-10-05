// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { VMPower } from '../../preload';
import type * as T from '../../shared/api';
import { diskGuardView } from './diskGuard.ts';

const GiB = 1024 ** 3;
const disk = (label: string, free: number, level: string): T.DiskGuardDisk => ({ label, free: free * GiB, total: 100 * GiB, floor: 10 * GiB, level });
const guard = (level: string, disks: T.DiskGuardDisk[], paused: string[] = []): T.DiskGuard => ({ level, disks, paused, since: '', message: '' });
const vm = (extra: Partial<VMPower> = {}): VMPower => ({ state: 'running', memoryUsed: 0, memoryGranted: 0, memoryCap: 0, cpus: 4, ...extra });

test('nothing shows while every disk has room', () => {
  assert.equal(diskGuardView(undefined, null), null);
  assert.equal(diskGuardView(guard('ok', [disk('Storage pool', 50, 'ok')]), vm()), null);
});

test('nearing the floor warns, naming the disk', () => {
  const v = diskGuardView(guard('low', [disk('Worktrees', 80, 'ok'), disk('Storage pool', 13, 'low')]), null);
  assert.equal(v?.tone, 'low');
  assert.equal(v?.label, 'Disk low');
  assert.match(v!.body, /^Storage pool has 13\.0 GiB free\. At 10\.0 GiB/);
});

test('at the floor names the worst disk and who was paused', () => {
  const v = diskGuardView(guard('full', [disk('Worktrees', 12, 'low'), disk('Storage pool', 8, 'full')], ['p/agent-01']), null);
  assert.equal(v?.tone, 'full');
  assert.match(v!.body, /Storage pool has 8\.0 GiB free, under the 10\.0 GiB/);
  assert.match(v!.body, /The agent writing the most is paused/);
});

test('a VM paused for the host disk wins over a stale guard', () => {
  const v = diskGuardView(guard('ok', [disk('Storage pool', 50, 'ok')]), vm({ state: 'paused', pausedForDisk: true, hostFree: 1.5 * GiB }));
  assert.equal(v?.label, 'Disk full: VM paused');
  assert.match(v!.body, /only 1\.5 GiB free/);
});

test('names the disk and says what frees it', () => {
  const pool = { ...disk("The agents' disk, the shared caches", 12, 'low'), advice: 'Make it bigger with `agentbox vm resize --disk <size>`.' };
  const v = diskGuardView(guard('low', [disk("The VM's system disk", 15, 'ok'), pool]), null);
  assert.equal(v?.title, "The agents' disk is running low");
  assert.match(v!.body, /^The agents' disk has 12\.0 GiB free/);
  assert.equal(v?.advice, pool.advice);
  const full = diskGuardView(guard('full', [{ ...pool, level: 'full' }]), null);
  assert.equal(full?.title, "The agents' disk is at its floor: new agents are refused");
  assert.doesNotMatch(full!.body, /delete files/);
});
