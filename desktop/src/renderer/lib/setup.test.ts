// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { ConnectionState, HostSetupStatus } from '../../preload';
import { setupCard } from './setup.ts';

const windowsBeforeSetup: HostSetupStatus = {
  pkexec: null,
  user: 'leandro',
  running: false,
  resizing: false,
  vm: null,
  wsl: { wsl: '2.4.13.0', name: 'AgentBox', user: '', exists: false, problem: "AgentBox's WSL distro isn't set up: run agentbox wsl init" },
};

test('the WSL card stays up through every retry before the distro exists', () => {
  const error = "the AgentBox daemon isn't running";
  // What the app sees until the distro is set up: every attempt fails. The
  // main process used to report "connecting" before each one, too.
  const states: ConnectionState[] = [
    { state: 'connecting' },
    { state: 'disconnected', error },
    { state: 'connecting', error },
    { state: 'disconnected', error },
    { state: 'connecting', error },
  ];
  for (const connection of states) {
    assert.deepEqual(setupCard(connection, windowsBeforeSetup), { kind: 'wsl', wsl: windowsBeforeSetup.wsl }, connection.state);
  }
});

test('no card once the daemon answers, or before the status does', () => {
  assert.equal(setupCard({ state: 'connected' }, windowsBeforeSetup), null);
  assert.equal(setupCard({ state: 'disconnected', error: 'x' }, undefined), null);
  assert.equal(setupCard({ state: 'disconnected', error: 'x' }, { ...windowsBeforeSetup, wsl: { ...windowsBeforeSetup.wsl!, problem: undefined } }), null);
});

test("a Mac's VM comes first", () => {
  const vm = { lima: '/opt/homebrew/bin/limactl', name: 'agentbox', exists: false, problem: "AgentBox's Linux VM isn't set up" };
  assert.deepEqual(setupCard({ state: 'connecting' }, { ...windowsBeforeSetup, vm, wsl: null }), { kind: 'vm', vm });
});

const linux = { mode: 'vm' as const, kvm: true, cores: 16, memory: 32 * 1024 ** 3, defaultCpus: 8, defaultMemoryCap: 24 * 1024 ** 3 };
const linuxBeforeSetup: HostSetupStatus = {
  pkexec: '/usr/bin/pkexec',
  user: 'leandro',
  running: false,
  resizing: false,
  vm: null,
  wsl: null,
  linux,
  chv: { mode: 'vm', driver: 'cloud-hypervisor', name: 'agentbox', state: 'missing', since: '0001-01-01T00:00:00Z', problem: "AgentBox's Linux VM isn't set up: run agentbox vm init", memory: { min: 0, cap: 0, granted: 0, used: 0 }, disk: { size: 0, used: 0 } } as unknown as HostSetupStatus['chv'],
};

test('a Linux machine before vm init gets the VM to set up, and nothing else to choose', () => {
  for (const connection of [{ state: 'connecting' }, { state: 'disconnected', error: "AgentBox's Linux VM isn't set up" }] as ConnectionState[]) {
    assert.deepEqual(setupCard(connection, linuxBeforeSetup), { kind: 'linux', linux }, connection.state);
  }
  // Without /dev/kvm it is the same card, which says what's missing.
  const nokvm = { ...linux, kvm: false };
  assert.deepEqual(setupCard({ state: 'connecting' }, { ...linuxBeforeSetup, linux: nokvm }), { kind: 'linux', linux: nokvm });
});

test("no Linux card once the VM is made, or on a machine that runs AgentBox itself", () => {
  assert.equal(setupCard({ state: 'connected' }, linuxBeforeSetup), null);
  // A VM that's made but off is started by the app, not set up again.
  assert.equal(setupCard({ state: 'disconnected', error: 'x' }, { ...linuxBeforeSetup, chv: { ...linuxBeforeSetup.chv!, state: 'off' } }), null);
  // A host-mode installation's daemon is its own: the move is offered once it answers.
  assert.equal(setupCard({ state: 'disconnected', error: 'x' }, { ...linuxBeforeSetup, linux: { ...linux, mode: 'host' }, chv: null }), null);
});
