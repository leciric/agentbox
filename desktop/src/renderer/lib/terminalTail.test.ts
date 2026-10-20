import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { heavy, terminalLines } from './terminalTail.ts';

const tail = (busy: boolean, lines: string[]): T.TerminalTail => ({ ref: 'p/a', window: 'shell', busy, lines });
const tool = (command: string | undefined, output: string): T.ChatItem =>
  ({ id: '1', turn: 't', kind: 'tool', tool: { callId: 'c', title: 'Read file', kind: 'execute', status: 'in_progress', command, output }, createdAt: '', updatedAt: '' }) as T.ChatItem;

test('a terminal running something wins over the chat', () => {
  assert.deepEqual(terminalLines(tail(true, ['a', 'b', 'c', 'd', 'e']), [tool('ls', 'x')], 'said'), ['b', 'c', 'd', 'e']);
});

test('an idle terminal gives way to the chat’s latest command and the end of its output', () => {
  const items = [tool('go test ./...\n# more', '\x1b[32mok\x1b[0m  a\n\nok  b\nok  c\nFAIL d\n'), { id: '2', turn: 't', kind: 'assistant', text: 'hi', createdAt: '', updatedAt: '' } as T.ChatItem];
  assert.deepEqual(terminalLines(tail(false, ['$ ']), items, 'said'), ['$ go test ./...', 'ok  b', 'ok  c', 'FAIL d']);
  assert.deepEqual(terminalLines(undefined, [tool(undefined, '')], undefined), ['Read file']);
});

test('without a command, the idle terminal, then the row’s text', () => {
  assert.deepEqual(terminalLines(tail(false, ['lint@a:~$']), [], 'said'), ['lint@a:~$']);
  assert.deepEqual(terminalLines(undefined, undefined, 'one\n\ntwo'), ['one', 'two']);
  assert.deepEqual(terminalLines(undefined, undefined, undefined), []);
});

test('heavy is over a core or a quarter of the memory', () => {
  const G = 1024 ** 3;
  assert.equal(heavy({ cpu: 170, memory: G }, 16 * G), true);
  assert.equal(heavy({ cpu: 40, memory: 5 * G }, 16 * G), true);
  assert.equal(heavy({ cpu: 40, memory: 3 * G }, 16 * G), false);
  assert.equal(heavy({ memory: 30 * G }), false);
});
