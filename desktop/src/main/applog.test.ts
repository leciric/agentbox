// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { AppLog, captureConsole, maxBytes, reportSections } from './applog.ts';

const info = { version: '0.10.0', electron: '38.1.0', chrome: '140.0', platform: 'linux', arch: 'x64', target: 'this machine', vmMode: true };

test('the log rotates at its cap, and a report reads its end from both files', () => {
  const log = new AppLog(mkdtempSync(join(tmpdir(), 'applog-')));
  const line = 'x'.repeat(1000);
  for (let i = 0; i < (maxBytes / 1000) * 1.5; i++) log.write('LOG', `${i} ${line}`);
  assert.ok(existsSync(`${log.file}.1`), 'rotated to main.log.1');
  assert.ok(readFileSync(log.file, 'utf8').length < maxBytes);
  const tail = log.tail(4 * maxBytes);
  assert.ok(tail.length > maxBytes, 'the tail reaches into main.log.1');
  assert.match(tail.trimEnd().split('\n').at(-1)!, new RegExp(`^\\S+ LOG ${Math.ceil((maxBytes / 1000) * 1.5) - 1} x`));
  assert.equal(log.tail(100).length, 100);
});

test('uncaught errors are kept, with their stack cut to frames and URLs stripped', () => {
  const log = new AppLog(mkdtempSync(join(tmpdir(), 'applog-')));
  const err = new TypeError('x is undefined');
  err.stack = 'TypeError: x is undefined\n    at f (file:///home/lint/app/out/renderer/index.js?v=3#L1:2:3)\n    at https://user:pw@example.com/a.js?token=s:1:1';
  log.error('main', err);
  log.windowError({ name: 'Error', message: 'from the window' });
  log.error('main', 'a string was thrown');
  assert.equal(log.errors.length, 3);
  assert.deepEqual(log.errors[0].stack!.split('\n'), ['at f (file:///home/lint/app/out/renderer/index.js:2:3)', 'at https://example.com/a.js:1:1']);
  assert.equal(log.errors[2].message, 'a string was thrown');
  assert.match(readFileSync(log.file, 'utf8'), /UNCAUGHT window: Error: from the window/);

  const sections = reportSections(log, info);
  assert.deepEqual(sections.map((s) => s.id), ['app', 'app-errors', 'app-log']);
  assert.match(sections[0].content, /App: +0\.10\.0\n.*\nOS: +linux\/x64 .*\nConnected: +this machine\nLinux VM: +yes/);
  assert.match(sections[1].content, /in the main process: TypeError: x is undefined\n  at f/);
});

test('a report without errors leaves their section out', () => {
  const log = new AppLog(mkdtempSync(join(tmpdir(), 'applog-')));
  assert.deepEqual(reportSections(log, { ...info, platform: 'darwin' }).map((s) => s.id), ['app', 'app-log']);
  assert.equal(reportSections(log, info)[1].content, '(empty)');
});

test('console goes to the log as well as where it went', () => {
  const log = new AppLog(mkdtempSync(join(tmpdir(), 'applog-')));
  const out: unknown[][] = [];
  const fake = { log: (...a: unknown[]) => out.push(a), info: (...a: unknown[]) => out.push(a), warn: (...a: unknown[]) => out.push(a), error: (...a: unknown[]) => out.push(a) } as unknown as Console;
  captureConsole(log, fake);
  fake.error('phones:', new Error('boom'));
  fake.log('count %d', 3);
  assert.equal(out.length, 2);
  const text = readFileSync(log.file, 'utf8');
  assert.match(text, /ERROR phones: Error: boom\n +at /);
  assert.match(text, /LOG count 3\n/);
});
