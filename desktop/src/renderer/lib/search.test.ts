// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import { highlight, moveActive, searchItems, searchTarget } from './search.ts';
import { lineRange } from './reveal.ts';

const hit = (h: Partial<T.SearchHit>): T.SearchHit => ({ id: 'x', title: 'X', ...h });

test('searchItems walks the groups in order', () => {
  const items = searchItems({
    query: 'a',
    groups: [
      { kind: T.SearchProjects, hits: [hit({ id: 'p1' })] },
      { kind: T.SearchSkills, hits: [hit({ id: 's1' }), hit({ id: 's2' })] },
    ],
  });
  assert.deepEqual(
    items.map((i) => `${i.kind}:${i.hit.id}`),
    ['projects:p1', 'skills:s1', 'skills:s2'],
  );
  assert.deepEqual(searchItems(undefined), []);
});

test('moveActive goes round at either end', () => {
  assert.equal(moveActive(0, -1, 3), 2);
  assert.equal(moveActive(2, 1, 3), 0);
  assert.equal(moveActive(1, 1, 3), 2);
  assert.equal(moveActive(0, 1, 0), 0);
});

test('each kind opens where it lives', () => {
  assert.deepEqual(searchTarget({ kind: T.SearchProjects, hit: hit({ id: 'pawly', project: 'pawly' }) }), {
    open: 'view',
    view: { kind: 'project', project: 'pawly' },
  });
  assert.deepEqual(searchTarget({ kind: T.SearchAgents, hit: hit({ id: 'pawly/agent-01', project: 'pawly', agent: 'agent-01' }) }), {
    open: 'view',
    view: { kind: 'agent', ref: 'pawly/agent-01' },
  });

  // A chat message: the lead's is the project's chat, an agent's its own.
  const lead = searchTarget({ kind: T.SearchChats, hit: hit({ id: 'c1', project: 'pawly' }) });
  assert.deepEqual(lead, { open: 'view', view: { kind: 'project', project: 'pawly', tab: 'chat' }, reveal: { selector: '[data-chat-item-id="c1"]' } });
  const agent = searchTarget({ kind: T.SearchChats, hit: hit({ id: 'c2', project: 'pawly', agent: 'agent-01' }) });
  assert.equal(agent.open === 'view' && agent.view.kind === 'agent' && agent.view.ref, 'pawly/agent-01');

  const memory = { id: 'mem_1', project: 'pawly', kind: 'issue', title: 'T', content: '', importance: 3, createdAt: '', updatedAt: '' };
  const m = searchTarget({ kind: T.SearchMemories, hit: hit({ id: 'mem_1', project: 'pawly', memory }) });
  assert.deepEqual(m, {
    open: 'view',
    view: { kind: 'project', project: 'pawly', tab: 'memory' },
    reveal: { selector: '[data-memory="mem_1"]', memory: { section: 'memories', project: 'pawly', item: memory } },
  });
  const e = searchTarget({ kind: T.SearchEvents, hit: hit({ id: 'evt_1', project: 'pawly' }) });
  assert.equal(e.open === 'view' && e.reveal?.selector, '[data-event="evt_1"]');

  const media = { id: 'm1', agent: 'pawly/agent-01' } as T.MediaItem;
  assert.deepEqual(searchTarget({ kind: T.SearchMedia, hit: hit({ id: 'm1', project: 'pawly', media }) }), { open: 'media', item: media, project: 'pawly' });

  assert.deepEqual(searchTarget({ kind: T.SearchSkills, hit: hit({ id: 'deploy' }) }), {
    open: 'settings',
    section: 'skills',
    reveal: { selector: '[data-skill="deploy"]' },
  });
  // A connector opens at its scope: AgentBox-wide, a project's, an agent's.
  assert.equal(searchTarget({ kind: T.SearchConnectors, hit: hit({ id: 'hatch' }) }).open, 'settings');
  const project = searchTarget({ kind: T.SearchConnectors, hit: hit({ id: 'linear', project: 'pawly' }) });
  assert.deepEqual(project.open === 'view' && project.view, { kind: 'project', project: 'pawly', tab: 'connectors' });
  const own = searchTarget({ kind: T.SearchConnectors, hit: hit({ id: 'linear', project: 'pawly', agent: 'agent-01' }) });
  assert.deepEqual(own.open === 'view' && own.view, { kind: 'agent', ref: 'pawly/agent-01', tab: 'connectors' });

  assert.deepEqual(searchTarget({ kind: T.SearchNotes, hit: hit({ id: '4', project: 'pawly' }) }), {
    open: 'view',
    view: { kind: 'project', project: 'pawly', tab: 'brief' },
    reveal: { selector: '[data-project-notes]', line: 4 },
  });
  assert.deepEqual(searchTarget({ kind: T.SearchPulls, hit: hit({ id: '271', project: 'pawly' }) }), {
    open: 'view',
    view: { kind: 'project', project: 'pawly', tab: 'pulls' },
    reveal: { selector: '[data-pull-request="271"]', pull: { project: 'pawly', number: 271 } },
  });
});

test('a selector is safe whatever the id', () => {
  const t = searchTarget({ kind: T.SearchSkills, hit: hit({ id: 'a"b\\c' }) });
  assert.equal(t.open === 'settings' && t.reveal?.selector, '[data-skill="a\\"b\\\\c"]');
});

test('highlight marks every word, whatever its case', () => {
  assert.deepEqual(highlight('Hatch connector preset', 'pre HATCH'), [
    { text: 'Hatch', match: true },
    { text: ' connector ', match: false },
    { text: 'pre', match: true },
    { text: 'set', match: false },
  ]);
  assert.deepEqual(highlight('a.b', '.'), [
    { text: 'a', match: false },
    { text: '.', match: true },
    { text: 'b', match: false },
  ]);
  assert.deepEqual(highlight('plain', ''), [{ text: 'plain', match: false }]);
  assert.deepEqual(highlight('', 'x'), []);
});

test('lineRange finds a line of the notes', () => {
  const text = '# Notes\n\n- Use pnpm.\n- Hatch needs a token.';
  assert.equal(text.slice(...lineRange(text, 3)), '- Use pnpm.');
  assert.equal(text.slice(...lineRange(text, 4)), '- Hatch needs a token.');
  assert.deepEqual(lineRange(text, 9), [text.length, text.length]);
});
