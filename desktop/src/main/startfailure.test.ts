// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import type { ExecFileException } from 'node:child_process';
import { test } from 'node:test';
import { progressStep, startFailure } from './startfailure.ts';

const bin = 'C:\\Users\\l.uiz\\AppData\\Local\\AgentBox\\bin\\agentbox.exe';
const failed = (fields: Partial<ExecFileException>): ExecFileException =>
  Object.assign(new Error(`Command failed: ${bin} daemon start\n`), fields) as ExecFileException;

test('what the command said on stderr is the reason', () => {
  const err = failed({ code: 1 });
  assert.equal(startFailure(err, '', "error: AgentBox's WSL distro isn't set up: run agentbox wsl init\n", bin, 300_000), "AgentBox's WSL distro isn't set up: run agentbox wsl init");
});

test('a silent exit says its code, and what was printed on stdout', () => {
  assert.equal(startFailure(failed({ code: 1 }), '', '', bin, 300_000), `${bin} daemon start failed with exit code 1, without saying why`);
  // wsl.exe's own errors go to stdout, with a Windows error code.
  assert.equal(
    startFailure(failed({ code: -1 }), 'There is no distribution with the supplied name.\r\nError code: Wsl/Service/WSL_E_DISTRO_NOT_FOUND\r\n', '', bin, 300_000),
    `${bin} daemon start failed with exit code 0xFFFFFFFF, without saying why: Error code: Wsl/Service/WSL_E_DISTRO_NOT_FOUND`,
  );
  assert.equal(startFailure(failed({ code: 3221225477 }), '', '', bin, 300_000), `${bin} daemon start failed with exit code 0xC0000005, without saying why`);
});

test('a command the app gave up on says so, with what it said last', () => {
  assert.equal(startFailure(failed({ killed: true, signal: 'SIGTERM' }), '', '', bin, 300_000), `${bin} daemon start didn't finish in 300 seconds`);
  assert.equal(
    startFailure(failed({ killed: true, signal: 'SIGTERM' }), '', 'Installed agentbox in AgentBox\n', bin, 180_000),
    `${bin} daemon start didn't finish in 180 seconds: Installed agentbox in AgentBox`,
  );
});

test('a missing binary says where it looked', () => {
  const err = Object.assign(new Error('spawn ENOENT'), { code: 'ENOENT' }) as unknown as ExecFileException;
  assert.match(startFailure(err, '', '', bin, 300_000), /couldn't find .*agentbox\.exe/);
});

test('a signal says which', () => {
  assert.equal(startFailure(failed({ signal: 'SIGKILL' }), '', '', bin, 300_000), `${bin} daemon start was stopped (SIGKILL)`);
});

test('progress above the error is left out of it', () => {
  const stderr = 'Starting AgentBox, AgentBox\'s WSL distro…\nInstalled agentbox in AgentBox (41s)\nerror: the AgentBox daemon exited as it started in AgentBox: database is locked\n';
  assert.equal(startFailure(failed({ code: 1 }), '', stderr, bin, 900_000), 'the AgentBox daemon exited as it started in AgentBox: database is locked');
});

test('progressStep is the last thing the start said it was doing', () => {
  assert.equal(progressStep(''), '');
  assert.equal(progressStep('Starting AgentBox, AgentBox\'s WSL distro…\r\nInstalled agentbox in AgentBox (41s)\r\n'), 'Installed agentbox in AgentBox (41s)');
  assert.equal(progressStep('Starting the AgentBox daemon in AgentBox…\nerror: it broke\n'), 'Starting the AgentBox daemon in AgentBox…');
});
