import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import type { AgentSt, Row } from '../lib/leadFirst.ts';
import { deskOf, deskTone, hueOf, leadTone, officeModel, prLook, type OfficeInputs } from './model.ts';

const agent = (ref: string, over: Partial<T.Agent> = {}): T.Agent =>
  ({ ref, project: ref.split('/')[0], name: ref.split('/')[1], title: `Task ${ref}`, branch: `agentbox/${ref.split('/')[1]}`, ai: 'claude', state: 'running', createdAt: '2026-11-01T10:00:00Z', ...over }) as T.Agent;
const row = (a: T.Agent, st: AgentSt): Row => ({ agent: a, st });
const pr = (over: Partial<T.PullRequest> = {}): T.PullRequest => ({ number: 7, title: 'feat: x', state: 'OPEN', checks: 'passing', url: 'https://github.com/o/r/pull/7', ...over });

const inputs = (over: Partial<OfficeInputs> = {}): OfficeInputs => ({
  projects: [{ name: 'web', displayName: 'Web app' }, { name: 'api', displayName: '' }],
  rows: [],
  fleets: new Map(),
  questions: new Map(),
  leads: new Map(),
  review: [],
  ...over,
});

test('a sidebar state is a desk tone, done split by the machine', () => {
  assert.equal(deskTone(agent('p/a'), 'needs'), 'needs');
  assert.equal(deskTone(agent('p/a'), 'error'), 'error');
  assert.equal(deskTone(agent('p/a'), 'review'), 'finished');
  assert.equal(deskTone(agent('p/a'), 'working'), 'working');
  assert.equal(deskTone(agent('p/a'), 'done'), 'idle');
  assert.equal(deskTone(agent('p/a', { state: 'stopped' }), 'done'), 'stopped');
});

test('a pull request reads as open, draft, merged or closed, with its checks', () => {
  assert.equal(prLook(undefined), undefined);
  assert.deepEqual(prLook(pr()), { number: 7, state: 'open', checks: 'passing', url: 'https://github.com/o/r/pull/7', title: 'feat: x' });
  assert.equal(prLook(pr({ draft: true }))?.state, 'draft');
  assert.equal(prLook(pr({ state: 'MERGED', draft: true }))?.state, 'merged');
  assert.equal(prLook(pr({ state: 'closed' }))?.state, 'closed');
  assert.equal(prLook(pr({ checks: 'failing' }))?.checks, 'failing');
  assert.equal(prLook(pr({ checks: '' }))?.checks, 'none');
});

test('a lead waiting on a permission needs you', () => {
  assert.equal(leadTone('waiting'), 'needs');
  assert.equal(leadTone('running'), 'working');
  assert.equal(leadTone('ready'), 'idle');
  assert.equal(leadTone(undefined), 'idle');
});

test('a project keeps its colour', () => {
  assert.equal(hueOf('web'), hueOf('web'));
  assert.ok(hueOf('web') >= 0 && hueOf('web') < 360);
});

test('areas go in the projects order, desks in the order agents were made', () => {
  const m = officeModel(
    inputs({
      rows: [
        row(agent('api/agent-3', { createdAt: '2026-11-03T00:00:00Z' }), 'working'),
        row(agent('web/agent-2', { createdAt: '2026-11-02T00:00:00Z' }), 'working'),
        row(agent('web/agent-1', { createdAt: '2026-11-01T00:00:00Z' }), 'done'),
      ],
    }),
  );
  assert.deepEqual(m.areas.map((a) => a.project), ['web', 'api']);
  assert.equal(m.areas[0].label, 'Web app');
  assert.equal(m.areas[1].label, 'api');
  assert.deepEqual(m.areas[0].desks.map((d) => d.id), ['web/agent-1', 'web/agent-2']);
});

test('a project with nothing going on has no area; one the list lacks still does', () => {
  const m = officeModel(inputs({ rows: [row(agent('new/agent-1'), 'working')] }));
  assert.deepEqual(m.areas.map((a) => a.project), ['new']);
  const quiet = officeModel(inputs({ leads: new Map([['api', { chat: 'ready', last: { kind: 'assistant', text: 'Hi', at: '' } }]]) }));
  assert.deepEqual(quiet.areas, []);
  const thinking = officeModel(inputs({ leads: new Map([['api', { chat: 'running', last: { kind: 'assistant', text: 'Hi', at: '' } }]]) }));
  assert.deepEqual(thinking.areas.map((a) => a.project), ['api']);
  assert.equal(thinking.areas[0].lead.line, 'Hi');
});

test('the board lists who needs you, then what waits for review', () => {
  const asking = agent('web/agent-1');
  const broken = agent('web/agent-2', { outcome: 'failed', outcomeNote: 'WebSocket closed' });
  const done = agent('web/agent-3');
  const prOpen = agent('web/agent-4');
  const busy = agent('web/agent-5');
  const m = officeModel(
    inputs({
      rows: [row(asking, 'needs'), row(broken, 'error'), row(done, 'review'), row(prOpen, 'done'), row(busy, 'working')],
      questions: new Map([['web', [{ id: 'q1', project: 'web', agent: 'agent-1', ref: 'web/agent-1', question: 'Paginate?', status: 'escalated', createdAt: '' }, { id: 'q2', project: 'web', agent: 'agent-2', ref: 'web/agent-2', question: 'For the lead only', status: 'pending', createdAt: '' }]]]),
      fleets: new Map([['web', { project: 'web', agents: [{ ref: 'web/agent-4', pr: pr({ number: 9 }), changes: { files: 2, insertions: 10, deletions: 1, dirty: false } }, { ref: 'web/agent-5', pr: pr({ number: 10 }) }] } as unknown as T.Fleet]]),
      review: [{ project: 'web', agent: 'agent-3', ref: 'web/agent-3', pr: pr({ number: 8, checks: 'failing' }), finishedAt: '', fingerprint: '' }],
      leads: new Map([['web', { chat: 'waiting' }]]),
    }),
  );
  assert.deepEqual(m.board.needs.map((i) => [i.ref, i.tone, i.text]), [
    ['web/lead', 'needs', undefined],
    ['web/agent-1', 'needs', 'Paginate?'],
    ['web/agent-2', 'error', 'WebSocket closed'],
  ]);
  // A working agent's open PR isn't waiting for you yet.
  assert.deepEqual(m.board.review.map((i) => [i.ref, i.pr?.number]), [
    ['web/agent-3', 8],
    ['web/agent-4', 9],
  ]);
  const d = deskOf(m, 'web/agent-4');
  assert.deepEqual(d?.changes, { files: 2, insertions: 10, deletions: 1 });
  assert.equal(deskOf(m, 'web/agent-3')?.pr?.checks, 'failing');
  assert.equal(deskOf(m, 'nope/agent-1'), undefined);
});

test('an agent asking in its own words, with no question filed, is quoted', () => {
  const m = officeModel(inputs({ rows: [row(agent('web/agent-1', { outcome: 'asking', outcomeNote: 'Launch or stop?' }), 'needs')] }));
  assert.equal(m.board.needs[0].text, 'Launch or stop?');
});
