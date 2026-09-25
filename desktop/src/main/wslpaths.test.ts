// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { linuxPath, windowsPath } from './wslpaths.ts';

test('linuxPath leaves an already-Linux path alone', () => {
  assert.equal(linuxPath('/home/ana'), '/home/ana');
});

test('linuxPath maps a drive path under /mnt', () => {
  assert.equal(linuxPath('C:\\Users\\ana'), '/mnt/c/Users/ana');
  assert.equal(linuxPath('D:/projects/agentbox/'), '/mnt/d/projects/agentbox');
  assert.equal(linuxPath('C:'), '/mnt/c');
});

test('linuxPath maps this distro\'s UNC path, case-insensitively', () => {
  assert.equal(linuxPath('\\\\wsl.localhost\\AgentBox\\home\\ana', 'AgentBox'), '/home/ana');
  assert.equal(linuxPath('\\\\wsl.localhost\\agentbox\\home\\ana', 'AgentBox'), '/home/ana');
  assert.equal(linuxPath('\\\\wsl$\\AgentBox\\home\\ana', 'AgentBox'), '/home/ana');
});

test('linuxPath is null for another distro or a network share', () => {
  assert.equal(linuxPath('\\\\wsl.localhost\\Ubuntu\\home\\ana', 'AgentBox'), null);
  assert.equal(linuxPath('\\\\server\\share\\file', 'AgentBox'), null);
});

test('windowsPath maps a /mnt path back to a drive letter', () => {
  assert.equal(windowsPath('/mnt/c/Users/ana'), 'C:\\Users\\ana');
  assert.equal(windowsPath('/mnt/d'), 'D:\\');
});

test('windowsPath falls back to the distro\'s UNC path', () => {
  assert.equal(windowsPath('/home/ana', 'AgentBox'), '\\\\wsl.localhost\\AgentBox\\home\\ana');
});
