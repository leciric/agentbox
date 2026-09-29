// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { detectLanguage } from './language.ts';

test('English and Brazilian Portuguese replies are told apart', () => {
  assert.equal(detectLanguage('I fixed the bug in the parser, and all of the tests pass now.'), 'en');
  assert.equal(detectLanguage('Corrigi o bug no parser, e todos os testes passam agora.'), 'pt');
  assert.equal(detectLanguage('Pronto! Já está no ar.'), 'pt');
  assert.equal(detectLanguage('Done: the build is green.'), 'en');
});

test('code, links and names do not decide it', () => {
  assert.equal(detectLanguage('Rodei os testes:\n```\nthe and is are was to of in it that this for with on\n```\nTudo passou.'), 'pt');
  assert.equal(detectLanguage('Veja `the_function_is_done` em https://example.com/the/and/is'), 'pt');
});

test('nothing to go on: the fallback', () => {
  assert.equal(detectLanguage('OK', 'pt'), 'pt');
  assert.equal(detectLanguage('`npm test`', 'en'), 'en');
});
