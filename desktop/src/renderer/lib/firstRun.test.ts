import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { HostSetupStatus } from '../../preload';
import type * as T from '../../shared/api';
import { filterRepos, firstRunStep, firstRunTools, isFirstRunTool, neverSetUp, showFirstRun, stepDone, type FirstRunProgress } from './firstRun.ts';

const none: FirstRunProgress = { machine: false, login: false, github: false, project: false };

test('the first run opens at the first step not done, skipping the ones that are', () => {
  assert.equal(firstRunStep(none), 'tool');
  assert.equal(firstRunStep({ ...none, tool: 'codex' }), 'machine');
  assert.equal(firstRunStep({ ...none, tool: 'codex', machine: true }), 'login');
  // Signed in already, and with a GitHub account: straight to the project.
  assert.equal(firstRunStep({ tool: 'claude', machine: true, login: true, github: true, project: false }), 'project');
  assert.equal(firstRunStep({ tool: 'claude', machine: true, login: true, github: true, project: true }), 'check');
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
  assert.deepEqual(firstRunTools, ['claude', 'codex', 'opencode', 'cursor', 'gemini']);
  assert.ok(isFirstRunTool('gemini'));
  assert.ok(!isFirstRunTool('aider'));
});
