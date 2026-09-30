// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { test } from 'node:test';

// logToClosedPipe runs a process that logs more to stdout and stderr than
// their pipes hold, the way Electron logs each rejected IPC handler, while
// nobody reads them, and then closes them: what's still waiting to be written
// fails with EPIPE, as when the terminal or launcher that started the app is
// gone. It resolves with how the process exited and what it threw.
function logToClosedPipe(guarded: boolean): Promise<{ code: number | null; uncaught: string }> {
  const script = `
    ${guarded ? `const { guardStdio } = await import(${JSON.stringify(new URL('./stdio.ts', import.meta.url).href)}); guardStdio();` : ''}
    let uncaught = '';
    process.on('uncaughtException', (err) => { uncaught ||= err.code ?? err.message; });
    const line = 'x'.repeat(20_000);
    for (let i = 0; i < 20; i++) {
      for (let j = 0; j < 10; j++) { console.error(line); console.log(line); }
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    (await import('node:fs')).writeFileSync(3, uncaught);
  `;
  const child = spawn(process.execPath, ['--input-type=module', '-e', script], {
    stdio: ['ignore', 'pipe', 'pipe', 'pipe'],
  });
  child.stdout!.pause();
  child.stderr!.pause();
  setTimeout(() => {
    child.stdout!.destroy();
    child.stderr!.destroy();
  }, 300);
  let uncaught = '';
  child.stdio[3]!.on('data', (chunk: Buffer) => (uncaught += chunk.toString()));
  return new Promise((resolve) => child.on('close', (code) => resolve({ code, uncaught })));
}

test('logging to a closed pipe throws EPIPE without guardStdio', async () => {
  const { uncaught } = await logToClosedPipe(false);
  assert.equal(uncaught, 'EPIPE');
});

test('guardStdio keeps logging to a closed pipe from throwing', async () => {
  const { code, uncaught } = await logToClosedPipe(true);
  assert.equal(uncaught, '');
  assert.equal(code, 0);
});
