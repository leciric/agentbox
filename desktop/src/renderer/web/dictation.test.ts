import assert from 'node:assert/strict';
import { test } from 'node:test';
import { dictationSupport, draftAt, heard, micRefused, recognitionLang, spoken } from './dictation.ts';

test('support: absent, insecure or ready', () => {
  assert.equal(dictationSupport({}), 'missing');
  assert.equal(dictationSupport({ isSecureContext: true }), 'missing');
  assert.equal(dictationSupport({ webkitSpeechRecognition: class {}, isSecureContext: false }), 'insecure');
  assert.equal(dictationSupport({ SpeechRecognition: class {} }), 'insecure');
  assert.equal(dictationSupport({ webkitSpeechRecognition: class {}, isSecureContext: true }), 'ready');
});

test('the language follows the app', () => {
  assert.equal(recognitionLang('pt-BR'), 'pt-BR');
  assert.equal(recognitionLang('en-US'), 'en-US');
  assert.equal(recognitionLang('fr'), 'en-US');
});

test('heard splits settled from interim text and skips what was taken', () => {
  const results = [
    Object.assign([{ transcript: 'hello ' }], { isFinal: true }),
    Object.assign([{ transcript: 'world ' }], { isFinal: true }),
    Object.assign([{ transcript: 'and mo' }], { isFinal: false }),
  ];
  assert.deepEqual(heard(results), { final: 'hello world ', interim: 'and mo' });
  assert.deepEqual(heard(results, 1), { final: 'world ', interim: 'and mo' });
  assert.deepEqual(heard([]), { final: '', interim: '' });
});

test('speech goes in at the cursor, spaced from the words around it', () => {
  assert.deepEqual(spoken(draftAt('', 0), ' hi there '), { text: 'hi there', cursor: 8 });
  assert.deepEqual(spoken(draftAt('fix', 3), 'the bug'), { text: 'fix the bug', cursor: 11 });
  assert.deepEqual(spoken(draftAt('fix this', 4), 'all'), { text: 'fix all this', cursor: 7 });
  assert.deepEqual(spoken(draftAt('a\n', 2), 'b'), { text: 'a\nb', cursor: 3 });
  assert.deepEqual(spoken(draftAt('abc', 3), '   '), { text: 'abc', cursor: 3 });
});

test('speech replaces a selection, and the cursor is clamped', () => {
  assert.deepEqual(spoken(draftAt('one two three', 4, 7), 'dos'), { text: 'one dos three', cursor: 7 });
  assert.deepEqual(draftAt('abc', 9, 2), { before: 'abc', after: '' });
  assert.deepEqual(draftAt('abc', -1), { before: '', after: 'abc' });
});

test('refusing the mic', () => {
  assert.ok(micRefused('not-allowed'));
  assert.ok(micRefused('service-not-allowed'));
  assert.ok(!micRefused('no-speech'));
});
