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
