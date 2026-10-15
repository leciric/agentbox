import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { agentSt, changesPending, countSt, filterCount, foldDone, leadLine, matches, narrowing, noFilters, projectRows, sortRows, toggle, type Row } from './leadFirst.ts';

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
  // No filters: every project, folded unless expanded.
  let out = projectRows(projects, rows, noFilters, new Set(['q']), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open, p.all.length]), [['p', false, 2], ['q', true, 1]]);
  // A state opens the projects with a match and drops the others.
  out = projectRows(projects, rows, { ...noFilters, st: ['review'] }, new Set(), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open, p.shown.length]), [['p', true, 1]]);
  // A project filter only hides the other projects.
  out = projectRows(projects, rows, { ...noFilters, projects: ['q'] }, new Set(), label);
  assert.deepEqual(out.map((p) => [p.project.name, p.open]), [['q', false]]);
  // A search matching a project's name keeps it, even with no agent matching.
  out = projectRows(projects, rows, { ...noFilters, q: 'quill' }, new Set(), label);
  assert.deepEqual(out.map((p) => p.project.name), ['q']);
});

test('done agents fold into one line unless a filter or the user asks for them', () => {
  const shown: Row[] = [{ agent: agent('a'), st: 'working' }, { agent: agent('b'), st: 'done' }];
  assert.deepEqual(foldDone(shown, noFilters, false), { rows: [shown[0]], folded: 1 });
  assert.equal(foldDone(shown, noFilters, true).folded, 0);
  assert.equal(foldDone(shown, { ...noFilters, st: ['done'] }, false).rows.length, 2);
});

test('filters count and narrow as the sidebar shows them', () => {
  assert.equal(filterCount({ st: ['needs'], projects: ['p', 'q'], q: 'x' }), 3);
  assert.equal(narrowing({ ...noFilters, projects: ['p'] }), false);
  assert.equal(narrowing({ ...noFilters, q: ' x ' }), true);
  assert.deepEqual(toggle(['a', 'b'], 'a'), ['b']);
  assert.deepEqual(toggle(['a'], 'b'), ['a', 'b']);
});

test('leadLine is one plain line, and says who wrote it', () => {
  assert.deepEqual(leadLine({ kind: 'assistant', text: '**Done.** See [PR #12](https://x/12)\n\n```go\nx\n```\nmerged', at: '' }), { text: 'Done. See PR #12 merged', mine: false });
  assert.deepEqual(leadLine({ kind: 'user', text: 'ship it', at: '' }), { text: 'ship it', mine: true });
  assert.equal(leadLine(undefined), undefined);
  assert.equal(leadLine({ kind: 'assistant', text: '```\n```', at: '' }), undefined);
});
