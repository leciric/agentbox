import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { HostSetupStatus } from '../../preload';
import type * as T from '../../shared/api';
import {
  filterRepos,
  firstRunStep,
  firstRunTools,
  imageState,
  isFirstRunTool,
  lastLine,
  machineStarted,
  machineState,
  needsDaemon,
  neverSetUp,
  showFirstRun,
  stepDone,
  waitsForMachine,
  type FirstRunProgress,
  type MachineRun,
} from './firstRun.ts';

const none: FirstRunProgress = { machine: false, daemon: false, login: false, github: false, project: false };

test('the first run opens at the first step not done, skipping the ones that are', () => {
  assert.equal(firstRunStep(none), 'tool');
  assert.equal(firstRunStep({ ...none, tool: 'codex' }), 'machine');
  assert.equal(firstRunStep({ ...none, tool: 'codex', machine: true }), 'login');
  // Signed in already, and with a GitHub account: straight to the project.
  assert.equal(firstRunStep({ tool: 'claude', machine: true, daemon: true, login: true, github: true, project: false }), 'project');
  assert.equal(firstRunStep({ tool: 'claude', machine: true, daemon: true, login: true, github: true, project: true }), 'check');
  assert.equal(stepDone('machine', { ...none, tool: 'claude', machine: true }), true);
  assert.equal(stepDone('login', { ...none, tool: 'claude', machine: true }), false);
});

const host = (over: Partial<HostSetupStatus>): HostSetupStatus => ({ pkexec: null, user: 'me', running: false, resizing: false, vm: null, wsl: null, ...over }) as HostSetupStatus;

test('a machine with no VM yet is never set up; one whose VM won\'t start is', () => {
  assert.equal(neverSetUp(host({ vm: { lima: '', name: 'agentbox', exists: false } })), true);
  assert.equal(neverSetUp(host({ vm: { lima: '', name: 'agentbox', exists: true, problem: 'stopped' } })), false);
  assert.equal(neverSetUp(host({ wsl: { wsl: '2', name: 'agentbox', user: 'me', exists: false } })), true);
  assert.equal(neverSetUp(host({ linux: { mode: 'vm' }, chv: { state: 'missing' } } as Partial<HostSetupStatus>)), true);
  assert.equal(neverSetUp(undefined), false);
});

test('the first run shows on a new installation until it is done, never on one that existed', () => {
  const fresh = { firstRun: true } as T.Settings;
  const existing = { firstRun: false } as T.Settings;
  const noVM = host({ vm: { lima: '', name: 'agentbox', exists: false } });
  const vm = host({ vm: { lima: '', name: 'agentbox', exists: true } });
  assert.equal(showFirstRun({ connected: true, settings: fresh, host: undefined, mark: null }), true);
  assert.equal(showFirstRun({ connected: true, settings: existing, host: undefined, mark: null }), false, 'set up before');
  assert.equal(showFirstRun({ connected: true, settings: fresh, host: undefined, mark: 'done' }), false);
  assert.equal(showFirstRun({ connected: true, settings: undefined, host: undefined, mark: 'started' }), true, 'the daemon up, its settings not read yet');
  assert.equal(showFirstRun({ connected: true, settings: undefined, host: undefined, mark: null }), false);
  assert.equal(showFirstRun({ connected: false, settings: undefined, host: noVM, mark: null }), true, 'no daemon yet');
  assert.equal(showFirstRun({ connected: false, settings: undefined, host: vm, mark: 'started' }), true, 'the VM made, its daemon not up yet');
  assert.equal(showFirstRun({ connected: false, settings: undefined, host: vm, mark: null }), false, 'a VM with a problem');
});

test('a search keeps the repositories with every word, in GitHub\'s order', () => {
  const repos = [
    { fullName: 'acme/pawly', description: 'Pet reminders', private: true, cloneUrl: '' },
    { fullName: 'acme/site', description: '', private: false, cloneUrl: '' },
    { fullName: 'me/pawly-api', private: false, cloneUrl: '' },
  ];
  assert.deepEqual(filterRepos(repos, '').map((r) => r.fullName), ['acme/pawly', 'acme/site', 'me/pawly-api']);
  assert.deepEqual(filterRepos(repos, 'PAWLY').map((r) => r.fullName), ['acme/pawly', 'me/pawly-api']);
  assert.deepEqual(filterRepos(repos, 'acme reminders').map((r) => r.fullName), ['acme/pawly']);
});

test('the first run offers every AI tool, Claude Code first', () => {
  assert.deepEqual(firstRunTools, ['claude', 'codex', 'opencode', 'cursor', 'gemini', 'copilot']);
  assert.ok(isFirstRunTool('copilot'));
  assert.ok(isFirstRunTool('gemini'));
  assert.ok(!isFirstRunTool('aider'));
});

test('the machine step is behind once the machine is being made: the next step waits for the daemon, not the user', () => {
  // Started, no daemon yet: on to the sign-in, which says it waits.
  const started = { ...none, tool: 'claude' as const, machine: true };
  assert.equal(firstRunStep(started), 'login');
  assert.equal(waitsForMachine('login', started), true);
  // The daemon answers: the same step, no longer waiting.
  assert.equal(waitsForMachine('login', { ...started, daemon: true }), false);
  // Choosing the tool and the machine never need the daemon; everything after does.
  assert.deepEqual(['tool', 'machine', 'login', 'github', 'project', 'check'].map((s) => needsDaemon(s as never)), [false, false, true, true, true, true]);
});

const idle: MachineRun = { state: 'idle', lines: [] };
const down = { state: 'disconnected' as const, error: "the AgentBox daemon isn't running" };
const noVM = host({ vm: { lima: '', name: 'agentbox', exists: false, problem: "AgentBox's VM isn't set up" } });

test('the machine indicator follows the run in the background, and the daemon once it answers', () => {
  assert.deepEqual(machineState({ run: idle, host: noVM, connection: down }), { kind: 'idle' });
  assert.equal(machineStarted(machineState({ run: idle, host: noVM, connection: down })), false);
  const running = machineState({ run: { state: 'running', lines: ['==> Downloading Debian', 'part 2 of 3\r40%\r61%', ''], since: 5 }, host: noVM, connection: down });
  assert.deepEqual(running, { kind: 'running', line: '61%', since: 5 });
  assert.equal(machineStarted(running), true);
  assert.deepEqual(machineState({ run: { state: 'failed', lines: [], error: 'Lima failed' }, host: noVM, connection: down }), { kind: 'failed', error: 'Lima failed' });
  // Made: its daemon starting is still the machine getting ready.
  const starting = { state: 'connecting' as const, starting: { since: 9, step: 'Starting the AgentBox daemon…' } };
  assert.deepEqual(machineState({ run: { state: 'done', lines: [] }, host: noVM, connection: starting }), { kind: 'running', line: 'Starting the AgentBox daemon…', since: 9 });
  assert.deepEqual(machineState({ run: { state: 'failed', lines: [], error: 'x' }, host: noVM, connection: { state: 'connected' } }), { kind: 'ready' });
});

test('after a reload the main process\'s run still counts, and a VM it left half made is a failure to retry', () => {
  assert.equal(machineState({ run: idle, host: { ...noVM, running: true }, connection: down }).kind, 'running');
  // A resize is no machine being made.
  assert.equal(machineState({ run: idle, host: { ...noVM, running: true, resizing: true }, connection: down }).kind, 'idle');
  const broken = host({ wsl: { wsl: '2', name: 'AgentBox', user: 'me', exists: true, problem: 'WSL 1 distro' } });
  assert.deepEqual(machineState({ run: idle, host: broken, connection: down }), { kind: 'failed', error: 'WSL 1 distro' });
});

test('one line of a run is its latest, without step markers or colours', () => {
  assert.equal(lastLine([]), '');
  assert.equal(lastLine(['==> Installing Incus', '', '  ']), 'Installing Incus');
  assert.equal(lastLine(['\x1b[1mDownloading\x1b[0m 10%\r20%\r']), '20%');
});

test('the base image waits for the machine, downloads, fails to a retry, or waits for the first agent', () => {
  const image = (over: Partial<T.ImageStatus>): T.ImageStatus => ({ ready: false, snapshot: '', build: 'idle', waiting: 0, ...over });
  assert.deepEqual(imageState(false, undefined), { kind: 'waiting' });
  assert.deepEqual(imageState(true, image({ build: 'building', job: 'j1', step: 'Downloading the published image', progress: '40% of 1800 MB' })), {
    kind: 'running',
    step: 'Downloading the published image · 40% of 1800 MB',
    job: 'j1',
  });
  assert.deepEqual(imageState(true, image({ build: 'failed', error: 'disk full', job: 'j1' })), { kind: 'failed', error: 'disk full', job: 'j1' });
  assert.deepEqual(imageState(true, image({})), { kind: 'missing' });
  // A rebuild that failed leaves the image agents use: it's ready.
  assert.deepEqual(imageState(true, image({ ready: true, build: 'failed' })), { kind: 'ready' });
});
