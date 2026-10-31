// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { commandsOnly, looksLikeVerb, paletteRows, pickChoices, rankCommands, scoreCommand, type Command } from './palette.ts';
import { catalog } from './settingsCatalog.ts';
import { enUS } from '../../shared/i18n/en-US.ts';
import { ptBR } from '../../shared/i18n/pt-BR.ts';

const verbs = enUS['palette.verbs'].split(/\s+/);
const action = (id: string, title: string, keywords?: string): Command => ({ id, group: 'actions', title, keywords });
const setting = (id: string, title: string, where?: string, keywords?: string): Command => ({ id, group: 'settings', title, where, keywords });

const commands: Command[] = [
  action('new-agent', 'New agent', 'create spawn start task'),
  action('record', 'Record agent screen', 'video capture desktop'),
  action('android', 'Start Android emulator', 'phone device'),
  action('phone', 'Connect a phone', 'pair qr mobile'),
  action('nesting', 'Turn on nesting', 'incus test agentbox itself'),
  setting('agent-model', 'Model for new agents', 'Models · New agents', 'claude opus sonnet'),
  setting('lead-model', 'Model for the project’s chat', 'Models · Lead'),
  setting('phone-chat', 'Chat from your phone', 'Phone · Phone'),
  setting('language', 'Language', 'General · Appearance', 'idioma português'),
];
const ids = (query: string) => rankCommands(commands, query).map((r) => r.command.id);

test('a command is found by every word typed, from the start of a word', () => {
  assert.deepEqual(ids('record'), ['record']);
  assert.deepEqual(ids('rec scr'), ['record']);
  assert.deepEqual(ids('android'), ['android']);
  assert.deepEqual(ids('record android'), []);
  // Inside a word, from three letters on: "droid" finds Android, "dr" doesn't.
  assert.deepEqual(ids('droid'), ['android']);
  assert.deepEqual(ids('dr'), []);
});

test('the title outranks the keywords, and the start of the title outranks the rest', () => {
  // "phone" is in Connect a phone's title, Chat from your phone's, and only in
  // Start Android emulator's keywords.
  assert.deepEqual(ids('phone'), ['phone', 'phone-chat', 'android']);
  // "model" starts both Model settings' titles; the shorter comes first.
  assert.deepEqual(ids('model'), ['agent-model', 'lead-model']);
  assert.ok(scoreCommand(commands[0], 'new') > scoreCommand(commands[0], 'agent'));
  assert.ok(scoreCommand(commands[0], 'agent') > scoreCommand(commands[0], 'spawn'));
});

test('case and accents are folded, in the query and in the command', () => {
  assert.deepEqual(ids('PORTUGUES'), ['language']);
  assert.deepEqual(ids('Português'), ['language']);
});

test('an empty query, or only ">", lists every command as given', () => {
  assert.deepEqual(ids(''), commands.map((c) => c.id));
  assert.deepEqual(ids('> '), commands.map((c) => c.id));
  assert.deepEqual(commandsOnly('  >record'), { only: true, rest: 'record' });
  assert.deepEqual(commandsOnly('record '), { only: false, rest: 'record' });
});

test('a query looks like a verb by its first word, or the start of one', () => {
  for (const q of ['record', 'Record agent', 'rec', 'start android', 'turn on nesting', 'new', '> open settings']) assert.ok(looksLikeVerb(q, verbs), q);
  for (const q of ['android', 'model', 'phone', 'r', '', 'pawly agent-01']) assert.ok(!looksLikeVerb(q, verbs), q);
  // In Portuguese, with its own verbs, accents or not.
  const pt = ptBR['palette.verbs'].split(/\s+/);
  for (const q of ['gravar tela', 'iniciar android', 'conectar celular', 'ligar aninhamento', 'abrir', 'nóvo agente', 'novo agente']) assert.ok(looksLikeVerb(q, pt), q);
  assert.ok(!looksLikeVerb('modelo', pt));
});

test('actions and settings go above what was found when the query looks like a verb', () => {
  const found = ['chat:1', 'media:2'];
  const layout = (query: string) =>
    paletteRows(rankCommands(commands, query), found, query, verbs).map((r) => (r.kind === 'command' ? r.command.id : r.item));
  // "start" is a verb: Start Android emulator, New agent (by its keyword)…
  assert.deepEqual(layout('start'), ['android', 'new-agent', 'chat:1', 'media:2']);
  // "model" isn't: what was found first, then the settings.
  assert.deepEqual(layout('model'), ['chat:1', 'media:2', 'agent-model', 'lead-model']);
  // Actions before settings, whatever their scores.
  assert.deepEqual(layout('phone'), ['chat:1', 'media:2', 'phone', 'android', 'phone-chat']);
  // ">" is commands only.
  assert.deepEqual(layout('>phone'), ['phone', 'android', 'phone-chat']);
});

test('a pick lists the current one first, then the rest that match', () => {
  const agents = [
    { id: 'pawly/agent-01', title: 'Fix the login' },
    { id: 'pawly/agent-02', title: 'Dark mode' },
    { id: 'shop/agent-03', title: 'Checkout login' },
  ];
  assert.deepEqual(pickChoices(agents, '', 'pawly/agent-02').map((a) => a.id), ['pawly/agent-02', 'pawly/agent-01', 'shop/agent-03']);
  assert.deepEqual(pickChoices(agents, 'login').map((a) => a.id), ['pawly/agent-01', 'shop/agent-03']);
  assert.deepEqual(pickChoices(agents, 'shop').map((a) => a.id), ['shop/agent-03']);
});

// Settings builds its page inside a component, from hooks, so the palette
// keeps its own list: every row there has to be in it.
test('the catalog has every installation setting SettingsView draws', () => {
  const source = readFileSync(new URL('../components/SettingsView.tsx', import.meta.url), 'utf8');
  const body = source.slice(source.indexOf('const sections: SettingSection[] = ['), source.indexOf('...(projects.data ?? []).map(projectSection)'));
  const drawn = [...body.matchAll(/id: "([^"]+)",\s*(?:advanced: true,\s*)?label: t\("([^"]+)"\)/g)].map(([, id, label]) => `${id} ${label}`);
  assert.ok(drawn.length > 40, `only ${drawn.length} settings read from SettingsView.tsx`);
  const listed = new Set(catalog.map((s) => `${s.id} ${s.label}`));
  assert.deepEqual(drawn.filter((d) => !listed.has(d)), []);
  // And every key it names is in the catalog of messages.
  for (const s of catalog) for (const key of [s.label, s.group, s.keywords]) if (key) assert.ok(key in enUS, key);
});
