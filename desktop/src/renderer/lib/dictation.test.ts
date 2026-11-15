import assert from 'node:assert/strict';
import { test } from 'node:test';
import { dictationSupport, draftAt, heard, micMissing, micRefused, recognitionLang, speak, spoken } from './dictation.ts';

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

test('heard splits settled from interim text and reads the list', () => {
  const results = [
    Object.assign([{ transcript: 'hello ' }], { isFinal: true }),
    Object.assign([{ transcript: 'world ' }], { isFinal: true }),
    Object.assign([{ transcript: 'and mo' }], { isFinal: false }),
  ];
  assert.deepEqual(heard(results), { final: 'hello world ', interim: 'and mo' });
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

test('speech does not overwrite what was typed or cleared meanwhile', () => {
  const draft = draftAt('hi', 2);
  assert.deepEqual(speak(draft, 'hi', 'hi', 'there'), { text: 'hi there', cursor: 8 });
  assert.equal(speak(draft, 'hi there', 'hi there!', 'there again'), null);
  assert.equal(speak(draft, 'hi there', '', 'there again'), null); // sent: the draft was cleared
});

test('a missing mic is told apart from a refused one', () => {
  assert.equal(micMissing('audio-capture'), true);
  assert.equal(micMissing('not-allowed'), false);
  assert.equal(micRefused('audio-capture'), false);
});
