import assert from 'node:assert/strict';
import { test } from 'node:test';

// Hermes, the phone's JavaScript engine, has no Intl.PluralRules on Android:
// the home screen's first plural ("2 agents") crashed the app as soon as it
// had synced. Take it away before the catalogs are loaded, as on the phone.
delete (Intl as { PluralRules?: unknown }).PluralRules;
const { setLanguage, t } = await import('./i18n.ts');

test('plurals work on an engine without Intl.PluralRules', () => {
  setLanguage('en-US');
  assert.equal(t('mobile.home.agents', { count: 0 }), 'no agents');
  assert.equal(t('mobile.home.agents', { count: 1 }), '1 agent');
  assert.equal(t('mobile.home.agents', { count: 2 }), '2 agents');
  assert.equal(t('mobile.home.needYou', { count: 3 }), '3 need you');
  setLanguage('pt-BR');
  assert.equal(t('mobile.home.questions', { count: 1 }), '1 pergunta');
  assert.equal(t('mobile.home.questions', { count: 2 }), '2 perguntas');
  setLanguage('en-US');
});
