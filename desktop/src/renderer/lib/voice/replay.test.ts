// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { ReaderState } from './reader.ts';
import { replayUtterances, replaying, toggleReplay, type Reader } from './replay.ts';
import type { VoiceLanguage } from './speakable.ts';

// A reader that writes down what it's told.
function fake() {
  const calls: string[] = [];
  const reader: Reader = {
    speak: (text: string, language: VoiceLanguage, owner?: string) => void calls.push(`speak ${language} ${owner}: ${text}`),
    stop: () => void calls.push('stop'),
  };
  return { reader, calls };
}

const idle: ReaderState = { status: 'idle' };

test('a reply is read whole, as speakable sentences, in its own language', () => {
  assert.deepEqual(replayUtterances('Fixed **the bug**. See `go test`.\n\n```go\nx := 1\n```\nDone!', 'pt'), [
    { text: 'Fixed the bug.', language: 'en' },
    { text: 'See go test.', language: 'en' },
    { text: 'The code is in the chat.', language: 'en' },
    { text: 'Done!', language: 'en' },
  ]);
  assert.deepEqual(replayUtterances('Corrigi o erro. Está pronto.', 'en'), [
    { text: 'Corrigi o erro.', language: 'pt' },
    { text: 'Está pronto.', language: 'pt' },
  ]);
  assert.deepEqual(replayUtterances('Ok', 'pt'), [{ text: 'Ok', language: 'pt' }]);
  assert.deepEqual(replayUtterances('', 'en'), []);
});

test('replaying stops what is being read first, then reads the reply as its own', () => {
  const { reader, calls } = fake();
  toggleReplay(reader, { status: 'speaking' }, 'a1', 'One. Two.', 'en');
  assert.deepEqual(calls, ['stop', 'speak en a1: One.', 'speak en a1: Two.']);
});

test('replaying another reply while one is read switches to it', () => {
  const { reader, calls } = fake();
  toggleReplay(reader, { status: 'speaking', owner: 'a1' }, 'a2', 'Other.', 'en');
  assert.deepEqual(calls, ['stop', 'speak en a2: Other.']);
});

test('clicked while its reply is read, it only stops', () => {
  const { reader, calls } = fake();
  toggleReplay(reader, { status: 'speaking', owner: 'a1' }, 'a1', 'One. Two.', 'en');
  toggleReplay(reader, { status: 'loading', owner: 'a2' }, 'a2', 'One.', 'en');
  assert.deepEqual(calls, ['stop', 'stop']);
});

test('a reply is being replayed only while the reader is on its sentences', () => {
  assert.equal(replaying({ status: 'speaking', owner: 'a1' }, 'a1'), true);
  assert.equal(replaying({ status: 'loading', owner: 'a1' }, 'a1'), true);
  assert.equal(replaying({ status: 'speaking', owner: 'a1' }, 'a2'), false);
  // The live stream's sentences, queued after the replay's, are nobody's.
  assert.equal(replaying({ status: 'speaking' }, 'a1'), false);
  assert.equal(replaying({ ...idle, owner: 'a1' }, 'a1'), false);
});
