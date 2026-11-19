// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';

(globalThis as unknown as { window: unknown }).window = { agentbox: {} };
const { atHome, interleave, noFilter, pageKind, pagesIn, peekMediaFilter, requestMediaFilter, scopeOf, scopesOf, showsMedia, showsPages, takeMediaFilter, withScope } = await import('./mediaFilter.ts');

const at = (minute: number) => new Date(Date.UTC(2026, 9, 10, 12, minute)).toISOString();

const item = (id: string, minute: number): T.MediaItem => ({ id, agent: 'shop/agent-1', agentName: 'agent-1', kind: 'screenshot', name: id, size: 1, source: 'agent', meta: {}, createdAt: at(minute) });

const page = (id: string, minute: number, agents: string[], title = id): T.Artifact =>
  ({ id, title, agent: agents[0], agents, version: 1, updatedAt: at(minute), createdAt: at(minute) }) as unknown as T.Artifact;

test('scopes follow where the view was opened', () => {
  assert.deepEqual(scopesOf({ project: 'shop', agent: 'agent-1' }), ['agent', 'project', 'all']);
  assert.deepEqual(scopesOf({ project: 'shop' }), ['project', 'all']);
  assert.deepEqual(scopesOf({}), ['all']);
});

test('withScope widens and narrows from home, keeping the kind and toggles', () => {
  const home = { project: 'shop', agent: 'agent-1' };
  const f = { ...noFilter, kind: 'recording', favorite: true };
  const agent = withScope(f, 'agent', home);
  assert.deepEqual(agent, { ...f, project: 'shop', agent: 'agent-1' });
  assert.equal(scopeOf(agent), 'agent');
  const project = withScope(agent, 'project', home);
  assert.deepEqual(project, { ...f, project: 'shop', agent: '' });
  assert.equal(scopeOf(project), 'project');
  const all = withScope(project, 'all', home);
  assert.deepEqual(all, f);
  assert.equal(scopeOf(all), 'all');
  // Another project's, chosen in Everything, stays its own when widened.
  assert.equal(withScope({ ...noFilter, project: 'blog', agent: 'agent-2' }, 'project', {}).project, 'blog');
});

test('atHome is the agent of an agent tab, not another of its project', () => {
  const home = { project: 'shop', agent: 'agent-1' };
  assert.equal(atHome({ ...noFilter, project: 'shop', agent: 'agent-1' }, 'agent', home), true);
  assert.equal(atHome({ ...noFilter, project: 'shop', agent: 'agent-2' }, 'agent', home), false);
});

test('pages are never favorites, and media have no page kind', () => {
  assert.equal(showsMedia({ ...noFilter, kind: pageKind }), false);
  assert.equal(showsPages({ ...noFilter, kind: pageKind }), true);
  assert.equal(showsPages({ ...noFilter, kind: 'screenshot' }), false);
  assert.equal(showsPages({ ...noFilter, favorite: true }), false);
  assert.equal(showsPages(noFilter), true);
});

test('pagesIn keeps a project, an agent and a search, newest first', () => {
  const entries = [
    { project: 'shop', page: page('p1', 1, ['shop/agent-1'], 'Checkout') },
    { project: 'shop', page: page('p2', 5, ['shop/lead', 'shop/agent-2'], 'Sidebar') },
    { project: 'blog', page: page('p3', 3, ['blog/agent-1'], 'Posts') },
  ];
  const ids = (list: { page: T.Artifact }[]) => list.map((e) => e.page.id);
  assert.deepEqual(ids(pagesIn(entries, noFilter, '')), ['p2', 'p3', 'p1']);
  assert.deepEqual(ids(pagesIn(entries, { ...noFilter, project: 'shop' }, '')), ['p2', 'p1']);
  // An agent's are those it published or updated.
  assert.deepEqual(ids(pagesIn(entries, { ...noFilter, project: 'shop', agent: 'agent-2' }, '')), ['p2']);
  assert.deepEqual(ids(pagesIn(entries, noFilter, 'check')), ['p1']);
  // Every project's are found by the project's name too.
  assert.deepEqual(ids(pagesIn(entries, noFilter, 'blog')), ['p3']);
  assert.deepEqual(pagesIn(entries, { ...noFilter, kind: 'screenshot' }, ''), []);
});

test('interleave holds back pages older than the media read so far', () => {
  const items = [item('m9', 9), item('m6', 6)];
  const pages = [
    { project: 'shop', page: page('p8', 8, ['shop/agent-1']) },
    { project: 'shop', page: page('p2', 2, ['shop/agent-1']) },
  ];
  const ids = (list: ReturnType<typeof interleave>) => list.map((e) => (e.item ? e.item.id : e.page.id));
  assert.deepEqual(ids(interleave(items, pages, false)), ['m9', 'p8', 'm6']);
  assert.deepEqual(ids(interleave(items, pages, true)), ['m9', 'p8', 'm6', 'p2']);
  assert.deepEqual(ids(interleave([], pages, false)), ['p8', 'p2']);
});

test('a filter request is read once, and only while fresh', () => {
  requestMediaFilter({ project: 'shop', kind: pageKind });
  assert.deepEqual(peekMediaFilter(), { project: 'shop', kind: pageKind });
  assert.deepEqual(takeMediaFilter(), { project: 'shop', kind: pageKind });
  assert.equal(takeMediaFilter(), null);
  requestMediaFilter({ project: 'shop' });
  assert.equal(takeMediaFilter(Date.now() + 60_000), null);
});
