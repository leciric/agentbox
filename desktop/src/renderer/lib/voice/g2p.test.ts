// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { espeakPhonemes } from './g2p.ts';

// What eSpeak NG 1.52 (Echogarden's build) says for these, clause by clause.
const said: Record<string, string[]> = {
  Olá: ['o_l_ˈa'],
  'Terminei a tarefa': ['t_ˌe_ɾ_ə_m_i_n_ˈeɪ_ a t_ˌa_ɾ_ˈɛ_f_æ'],
  'tudo pronto': ['t_ˈu_d_ʊ p_r_ˈo_ŋ_t_ʊ'],
  'veja o código': ['v_ˈe_ʒ_æ_ ʊ k_ˈɔ_dʒ_i_ɡ_ʊ'],
};
const phones = async (text: string) => {
  const lines = said[text];
  if (!lines) throw new Error(`unexpected piece "${text}"`);
  return lines;
};

test('punctuation stays, and phones are joined into words', async () => {
  assert.equal(await espeakPhonemes('Olá! Terminei a tarefa, tudo pronto.', phones), 'olˈa! tˌeɾəminˈA a tˌaɾˈɛfæ, tˈudʊ prˈoŋtʊ.');
});

test('tied pairs become the symbols Kokoro knows them by, stress kept', async () => {
  assert.equal(await espeakPhonemes('(veja o código)', phones), '(vˈeʒæ ʊ kˈɔʤiɡʊ)');
});
