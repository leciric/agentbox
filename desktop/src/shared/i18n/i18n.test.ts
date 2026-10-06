// Run with `npm test`. Every language must say everything en-US says, taking
// the same values: a key missing from a catalog would quietly show English, and
// a placeholder renamed in one would show "{name}" on screen.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { enUS } from './en-US.ts';
import { format, placeholders } from './format.ts';
import { formatDate, formatNumber, language, languages, resolveLanguage, setLanguage, t } from './index.ts';

const keys = Object.keys(enUS) as (keyof typeof enUS)[];

for (const lang of languages.filter((l) => l.tag !== 'en-US')) {
  test(`${lang.tag} has every key en-US has`, () => {
    const missing = keys.filter((k) => typeof lang.messages[k] !== 'string' || lang.messages[k] === '');
    assert.deepEqual(missing, [], `${lang.tag} lacks ${missing.length} key(s)`);
  });

  test(`${lang.tag} has no key en-US doesn't`, () => {
    const extra = Object.keys(lang.messages).filter((k) => !(k in enUS));
    assert.deepEqual(extra, []);
  });

  test(`${lang.tag} takes the same values as en-US`, () => {
    const differ = keys.filter((k) => lang.messages[k] !== undefined && placeholders(lang.messages[k]!).join() !== placeholders(enUS[k]).join());
    assert.deepEqual(
      differ.map((k) => `${k}: ${placeholders(enUS[k])} vs ${placeholders(lang.messages[k]!)}`),
      [],
    );
  });
}

test('every language is listed once, with a tag Intl knows', () => {
  const tags = languages.map((l) => l.tag);
  assert.equal(new Set(tags).size, tags.length);
  for (const tag of tags) assert.deepEqual(Intl.getCanonicalLocales(tag), [tag]);
});

test('values, plurals and selects', () => {
  assert.equal(format('Hello, {name}', { name: 'Ana' }, 'en-US'), 'Hello, Ana');
  assert.equal(format('Hello, {name}', {}, 'en-US'), 'Hello, {name}');
  const agents = '{count, plural, =0 {no agents} one {# agent} other {# agents}}';
  assert.equal(format(agents, { count: 0 }, 'en-US'), 'no agents');
  assert.equal(format(agents, { count: 1 }, 'en-US'), '1 agent');
  assert.equal(format(agents, { count: 1200 }, 'en-US'), '1,200 agents');
  assert.equal(format('{count, plural, one {# agente} other {# agentes}}', { count: 1200 }, 'pt-BR'), '1.200 agentes');
  assert.equal(format('{n, plural, one {# in {where}} other {# in {where}}}', { n: 2, where: 'x' }, 'en-US'), '2 in x');
  assert.equal(format('{mode, select, light {claro} other {escuro}}', { mode: 'light' }, 'pt-BR'), 'claro');
  assert.equal(format('Run <code>agentbox</code> now', {}, 'en-US'), 'Run agentbox now');
});

test('placeholders sees through branches without mistaking them for values', () => {
  assert.deepEqual(placeholders('{count, plural, one {agent} other {{count} agents in {where}}} <b>x</b>'), ['<b>', 'count', 'where']);
});

test('a language falls back to en-US and to its primary language', () => {
  assert.equal(resolveLanguage('pt-PT').tag, 'pt-BR');
  assert.equal(resolveLanguage('xx').tag, 'en-US');
  assert.equal(resolveLanguage(undefined).tag, 'en-US');
});

test('messages, numbers and dates follow the language', () => {
  try {
    setLanguage('pt-BR');
    assert.equal(language(), 'pt-BR');
    assert.equal(t('common.cancel'), 'Cancelar');
    assert.equal(formatNumber(1234.5), '1.234,5');
    assert.equal(formatDate('2026-10-05T12:00:00Z', { day: 'numeric', month: 'long', timeZone: 'UTC' }), '5 de outubro');
    setLanguage('en-US');
    assert.equal(t('common.cancel'), 'Cancel');
    assert.equal(formatNumber(1234.5), '1,234.5');
  } finally {
    setLanguage('en-US');
  }
});
