import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { activeProjects, agentSt, changesPending, countSt, filterCount, leadBusy, live, liveOrder, leadLine, matches, narrowing, noFilters, projectRows, sortRows, toggle, type AgentSt, type Filters, type Row } from './leadFirst.ts';

const agent = (name: string, over: Partial<T.Agent> = {}) =>
  ({ name, ref: `${over.project ?? 'p'}/${name}`, project: 'p', title: '', branch: `agentbox/${name}`, state: 'running', chat: 'ready', createdAt: '2026-10-15T10:00:00Z', ...over }) as T.Agent;
const none = { review: new Set<string>(), asking: new Set<string>() };

test('agentSt reads what needs you first, then review, work and the rest', () => {
  assert.equal(agentSt(agent('a', { chat: 'waiting' }), none), 'needs');
  assert.equal(agentSt(agent('a'), { ...none, asking: new Set(['p/a']) }), 'needs');
  assert.equal(agentSt(agent('a', { chat: 'error' }), none), 'error');
  // One that failed and finished is in review.
  assert.equal(agentSt(agent('a', { outcome: 'failed' }), { ...none, review: new Set(['p/a']) }), 'review');
  assert.equal(agentSt(agent('a', { chat: 'running' }), none), 'working');
  assert.equal(agentSt(agent('a', { outcome: 'done' }), none), 'done');
  assert.equal(agentSt(agent('a', { state: 'stopped', chat: '' }), none), 'done');
  // Paused on a usage limit it resumes from by itself: working, not done.
  assert.equal(agentSt(agent('a', { outcome: 'limited', chat: 'ready' }), none), 'working');
  // Working on the changes asked for, it's working, not in review.
  assert.equal(agentSt(agent('a', { chat: 'running', changesRequested: 'Move it' }), { ...none, review: new Set(['p/a']) }), 'working');
});

test('a changes request is pending only while the agent is on it', () => {
  assert.equal(changesPending({ changesRequested: 'x', chat: 'running' }), true);
  assert.equal(changesPending({ changesRequested: 'x', chat: 'ready' }), false);
  assert.equal(changesPending({ changesRequested: undefined, chat: 'running' }), false);
});

test('countSt counts each state', () => {
  assert.deepEqual(countSt(['needs', 'working', 'working', 'done']), { needs: 1, error: 0, review: 0, working: 2, done: 1 });
});

test('matches filters by state, project and a search on title, name, branch and project', () => {
  const row: Row = { agent: agent('agent-7', { title: 'Dark mode' }), st: 'review' };
  assert.equal(matches(row, noFilters, 'Pawly'), true);
  assert.equal(matches(row, { ...noFilters, st: ['working'] }, 'Pawly'), false);
  assert.equal(matches(row, { ...noFilters, st: ['review'] }, 'Pawly'), true);
  assert.equal(matches(row, { ...noFilters, projects: ['other'] }, 'Pawly'), false);
  assert.equal(matches(row, { ...noFilters, q: 'DARK' }, 'Pawly'), true);
  assert.equal(matches(row, { ...noFilters, q: 'agent-7' }, 'Pawly'), true);
  assert.equal(matches(row, { ...noFilters, q: 'agentbox/agent' }, 'Pawly'), true);
  assert.equal(matches(row, { ...noFilters, q: 'pawly' }, 'Pawly'), true);
  assert.equal(matches(row, { ...noFilters, q: 'light' }, 'Pawly'), false);
});

test('matches finds the agent whose pull request is the number typed', () => {
  const row: Row = { agent: agent('agent-7', { title: 'Dark mode' }), st: 'review', pr: 404 };
  for (const q of ['404', '#404', 'pr 404', 'PR#404', ' pr #404 ']) assert.equal(matches(row, { ...noFilters, q }, 'Pawly'), true, q);
  for (const q of ['405', 'pr 40', 'pr']) assert.equal(matches(row, { ...noFilters, q }, 'Pawly'), false, q);
  assert.equal(matches({ ...row, pr: undefined }, { ...noFilters, q: '404' }, 'Pawly'), false);
});

test('sortRows puts what needs you first, then the newest', () => {
  const rows: Row[] = [
    { agent: agent('old', { createdAt: '2026-10-01T00:00:00Z' }), st: 'working' },
    { agent: agent('new', { createdAt: '2026-10-14T00:00:00Z' }), st: 'working' },
    { agent: agent('asks'), st: 'needs' },
    { agent: agent('done'), st: 'done' },
  ];
  assert.deepEqual(sortRows(rows).map((r) => r.agent.name), ['asks', 'new', 'old', 'done']);
});

test('projectRows opens what the filters narrow to and hides the rest', () => {
  const projects = [{ name: 'p', displayName: 'Pawly' }, { name: 'q', displayName: 'Quill' }] as T.Project[];
  const rows: Row[] = [
    { agent: agent('a1', { title: 'Dark mode' }), st: 'review' },
    { agent: agent('a2'), st: 'done' },
    { agent: agent('b1', { project: 'q' }), st: 'working' },
  ];
  const label = (p: T.Project) => p.displayName;
  // No filters: every project, folded unless expanded, with only its live agents.
  let out = projectRows(projects, rows, noFilters, new Map([['q', true]]), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open, p.all.length]), [['p', false, 1], ['q', true, 1]]);
  // A state keeps the projects with a match, folded as the user left them,
  // and drops the others.
  out = projectRows(projects, rows, { ...noFilters, st: ['review'] }, new Map(), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open, p.shown.length]), [['p', false, 1]]);
  // ...but not a pinned one.
  out = projectRows(projects, rows, { ...noFilters, st: ['review'] }, new Map(), label, { busy: new Set<string>(), keep: new Set(['q']) });
  assert.deepEqual(out.map((p) => [p.project.name, p.open]), [['p', false], ['q', false]]);
  // A busy lead counts as working, with no agent at work.
  out = projectRows(projects, [], { ...noFilters, st: ['working', 'review'] }, new Map(), label, { busy: new Set(['q']), keep: new Set<string>() });
  assert.deepEqual(out.map((p) => [p.project.name, p.open]), [['q', false]]);
  // A project filter only hides the other projects.
  out = projectRows(projects, rows, { ...noFilters, projects: ['q'] }, new Map(), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open]), [['q', false]]);
  // A search matching a project's name keeps it, even with no agent matching.
  out = projectRows(projects, rows, { ...noFilters, q: 'quill' }, new Map(), label);
  assert.deepEqual(out.map((p) => p.project.name), ['q']);
});

test('projectRows lists every project, live or not; a state filter narrows to matches and pins', () => {
  const projects = [{ name: 'p', displayName: 'Pawly' }, { name: 'q', displayName: 'Quill' }, { name: 'r', displayName: 'Rook' }, { name: 's', displayName: 'Sage' }] as T.Project[];
  const rows: Row[] = [
    { agent: agent('a1'), st: 'review' },
    { agent: agent('a2', { project: 'q' }), st: 'done' },
  ];
  const label = (p: T.Project) => p.displayName;
  const names = (f: Filters, ctx = { busy: new Set<string>(['r']), keep: new Set<string>(['s']) }) => projectRows(projects, rows, f, new Map(), label, ctx).map((p) => p.project.name);
  // Which of them are idle is orderProjects' split, not a filter.
  assert.deepEqual(names(noFilters), ['p', 'q', 'r', 's']);
  assert.deepEqual(names({ ...noFilters, st: ['review'] }), ['p', 's']);
  assert.deepEqual(names({ ...noFilters, q: 'quill' }), ['q']);
});

test('activeProjects is a busy or asking lead, a live agent or a question; the rest are idle', () => {
  const rows: Row[] = [
    { agent: agent('a1', { project: 'p' }), st: 'review' },
    { agent: agent('a2', { project: 'q' }), st: 'done' },
    { agent: agent('agent-3', { project: 't' }), st: 'done' },
  ];
  const leads = new Map([['p', 'ready'], ['q', 'ready'], ['r', 'running'], ['s', 'waiting'], ['t', 'ready'], ['u', undefined]]);
  // u's question is from an agent that was destroyed: nobody can answer it.
  assert.deepEqual([...activeProjects(rows, leads, ['t/agent-3', 'u/agent-28'])].sort(), ['p', 'r', 's', 't']);
});

test('an agent the VM holds back for memory is still working, so its project stays up', () => {
  const ctx = { review: new Set<string>(), asking: new Set<string>() };
  const working = agent('a1', { state: 'running', chat: 'running' });
  assert.equal(agentSt(working, ctx), 'working');
  // The daemon's memory pressure flips the hold on and off mid-run: it read as
  // done, and its project dropped out of the list and came back with it.
  for (const memory of [{ paused: 1 }, { waiting: 2 }, {}]) assert.equal(agentSt({ ...working, memory } as T.Agent, ctx), 'working');
});

test('projectRows opens a project with an agent waiting on a permission, until the user closes it', () => {
  const projects = [{ name: 'p', displayName: 'Pawly' }, { name: 'q', displayName: 'Quill' }] as T.Project[];
  const rows: Row[] = [
    { agent: agent('a1', { chat: 'waiting' }), st: 'needs' },
    { agent: agent('a2', { chat: 'waiting' }), st: 'needs' },
    { agent: agent('b1', { project: 'q', chat: 'running' }), st: 'working' },
  ];
  const label = (p: T.Project) => p.displayName;
  let out = projectRows(projects, rows, noFilters, new Map(), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open, p.approvals]), [['p', true, 2], ['q', false, 0]]);
  out = projectRows(projects, rows, noFilters, new Map([['p', false]]), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open, p.approvals]), [['p', false, 2], ['q', false, 0]]);
  // They are among its asks.
  assert.equal(countSt(out[0].all.map((r) => r.st)).needs, 2);
});

test('a project folds and unfolds the same under every filter, and keeps it across them', () => {
  const projects = [{ name: 'p', displayName: 'Pawly' }] as T.Project[];
  const rows: Row[] = [
    { agent: agent('a1'), st: 'working' },
    { agent: agent('a2'), st: 'review' },
  ];
  const label = (p: T.Project) => p.displayName;
  const open = (f: Filters, expanded: ReadonlyMap<string, boolean>) => projectRows(projects, rows, f, expanded, label)[0].open;
  for (const f of [noFilters, { ...noFilters, st: ['working'] as AgentSt[] }, { ...noFilters, projects: ['p'] }]) {
    assert.equal(open(f, new Map()), false, JSON.stringify(f));
    assert.equal(open(f, new Map([['p', true]])), true, JSON.stringify(f));
  }
});

test('a lead mid-turn is busy', () => {
  assert.equal(leadBusy('running'), true);
  assert.equal(leadBusy('ready'), false);
  assert.equal(leadBusy(undefined), false);
});

test('done agents leave the sidebar for Past agents', () => {
  assert.equal(live('done'), false);
  assert.equal(live('error'), true);
  assert.deepEqual(liveOrder, ['needs', 'error', 'review', 'working']);
});

test('filters count and narrow as the sidebar shows them', () => {
  assert.equal(filterCount({ st: ['needs'], projects: ['p', 'q'], q: 'x' }), 3);
  assert.equal(filterCount(noFilters), 0);
  assert.equal(narrowing({ ...noFilters, projects: ['p'] }), false);
  assert.equal(narrowing({ ...noFilters, q: ' x ' }), true);
  assert.deepEqual(noFilters.st, []);
  assert.deepEqual(toggle(['a', 'b'], 'a'), ['b']);
  assert.deepEqual(toggle(['a'], 'b'), ['a', 'b']);
});

test('leadLine is one plain line, and says who wrote it', () => {
  assert.deepEqual(leadLine({ kind: 'assistant', text: '**Done.** See [PR #12](https://x/12)\n\n```go\nx\n```\nmerged', at: '' }), { text: 'Done. See PR #12 merged', mine: false });
  assert.deepEqual(leadLine({ kind: 'user', text: 'ship it', at: '' }), { text: 'ship it', mine: true });
  assert.equal(leadLine(undefined), undefined);
  assert.equal(leadLine({ kind: 'assistant', text: '```\n```', at: '' }), undefined);
});

test('leadLine strips tables, rules, lists, emphasis and tags', () => {
  const table = "Here's a table:\n\n| Fruit | Color |\n|-------|:-----:|\n| Apple | Red |\n\n---\n1. __one__ and _two_\n- [x] ~~old~~ done<br>";
  assert.equal(leadLine({ kind: 'assistant', text: table, at: '' })?.text, "Here's a table: Table: Fruit, Color · 1 row one and two old done");
  assert.equal(leadLine({ kind: 'assistant', text: 'snake_case_name stays', at: '' })?.text, 'snake_case_name stays');
});
