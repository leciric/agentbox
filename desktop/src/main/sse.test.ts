// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseEvents } from './sse.ts';

test('parseEvents is empty with an unterminated buffer', () => {
  assert.deepEqual(parseEvents('data: hello'), { data: [], rest: 'data: hello' });
});

test('parseEvents reads one complete event and keeps the rest', () => {
  assert.deepEqual(parseEvents('data: hello\n\ndata: par'), { data: ['hello'], rest: 'data: par' });
});

test('parseEvents reads several events from one buffer', () => {
  assert.deepEqual(parseEvents('data: one\n\ndata: two\n\n'), { data: ['one', 'two'], rest: '' });
});

test('parseEvents joins a multi-line data field with newlines', () => {
  assert.deepEqual(parseEvents('data: line one\ndata: line two\n\n'), { data: ['line one\nline two'], rest: '' });
});

test('parseEvents ignores non-data lines and events with none', () => {
  assert.deepEqual(parseEvents('event: ping\nid: 1\n\ndata: real\n\n'), { data: ['real'], rest: '' });
});

test('parseEvents strips only a single leading space after the colon', () => {
  assert.deepEqual(parseEvents('data:  two spaces\n\n'), { data: [' two spaces'], rest: '' });
});
