// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../shared/api';
import { macTitle, offline, oneLine, perProject, summary, trayMenu, trayStatus, type TrayData } from './traymodel.ts';

const agent = (project: string, name: string, over: Partial<T.Agent> = {}): T.Agent => ({ ref: `${project}/${name}`, project, name, title: '', state: 'running', ...over }) as T.Agent;
const project = (name: string, displayName = ''): T.Project => ({ name, displayName }) as T.Project;
const question = (project: string, name: string, text: string): T.Notification => ({ id: `q-${name}`, kind: 'question', project, agent: name, ref: `${project}/${name}`, title: `${name} title`, text, at: '2026-10-24T10:00:00Z' });
const review = (project: string, name: string, pr?: Partial<T.PullRequest>): T.ReviewItem =>
  ({ project, agent: name, ref: `${project}/${name}`, title: `${name} work`, finishedAt: '2026-10-24T10:00:00Z', fingerprint: name, pr }) as T.ReviewItem;

function data(over: Partial<TrayData> = {}): TrayData {
  return { agents: [], projects: [], questions: { items: [], total: 0 }, review: { items: [], total: 0 }, ...over };
}

test('an unreachable AgentBox is offline, and says so', () => {
  assert.equal(trayStatus(undefined), offline);
  assert.equal(summary(offline), 'Not connected to AgentBox');
  assert.equal(trayMenu(offline, { vm: 'none', keepRunning: false })[0].label, 'Not connected to AgentBox');
});

test('nothing going on is idle', () => {
  const s = trayStatus(data({ agents: [agent('web', 'agent-1', { chat: 'idle' })] }));
  assert.equal(s.tone, 'idle');
  assert.equal(s.working, 0);
  assert.equal(s.waiting, 0);
  assert.equal(s.running, 1);
  assert.equal(summary(s), 'No agent is working, and nothing waits for you');
  assert.equal(macTitle(s), '');
});

test('agents mid-turn are working, a stalled one is not', () => {
  const s = trayStatus(
    data({
      agents: [agent('web', 'agent-1', { chat: 'running', title: 'Fix login' }), agent('web', 'agent-2', { chat: 'running', stalledSince: '2026-10-24T09:00:00Z' })],
      projects: [project('web', 'Web app')],
    }),
  );
  assert.equal(s.tone, 'working');
  assert.equal(s.working, 1);
  assert.equal(summary(s), '1 agent working');
  assert.deepEqual(s.projects, [{ name: 'web', label: 'Web app', items: [{ kind: 'working', label: 'Fix login', target: { kind: 'agent', ref: 'web/agent-1' } }], more: 0 }]);
});

test('questions, permission requests and reviews wait for the user, counted from the totals', () => {
  const q = question('api', 'agent-3', 'Should the reminders page\npaginate?');
  const s = trayStatus(
    data({
      agents: [agent('api', 'agent-4', { chat: 'waiting', title: 'Migrate' }), agent('web', 'agent-5', { chat: 'running' })],
      projects: [project('web'), project('api', 'API')],
      // A page of one, of 3 in all.
      questions: { items: [q], total: 3 },
      review: { items: [review('api', 'agent-6', { number: 42, url: 'https://github.com/o/r/pull/42' }), review('web', 'agent-7')], total: 2 },
    }),
  );
  assert.equal(s.tone, 'attention');
  assert.equal(s.waiting, 3 + 1 + 2);
  assert.equal(s.working, 1);
  assert.equal(summary(s), '1 agent working · 6 wait for you');
  assert.equal(macTitle(s), '●6 ▶1');
  // In the projects' order; within one, questions, permissions, reviews, then working.
  assert.deepEqual(
    s.projects.map((p) => [p.label, p.items.map((i) => [i.kind, i.label, i.target])]),
    [
      [
        'web',
        [
          ['review', 'agent-7 work', { kind: 'agent', ref: 'web/agent-7' }],
          ['working', 'agent-5', { kind: 'agent', ref: 'web/agent-5' }],
        ],
      ],
      [
        'API',
        [
          ['question', 'agent-3 title: Should the reminders page paginate?', { kind: 'question', notice: q }],
          ['permission', 'Migrate asks for a permission', { kind: 'agent', ref: 'api/agent-4' }],
          ['review', 'agent-6 work: pull request #42', { kind: 'url', url: 'https://github.com/o/r/pull/42' }],
        ],
      ],
    ],
  );
});

test('with reviewing off, Ready to review is left out', () => {
  const s = trayStatus(data({ review: undefined, agents: [agent('web', 'agent-1', { chat: 'running' })] }));
  assert.equal(s.waiting, 0);
  assert.equal(s.tone, 'working');
});

test('a project lists a few and says how many more, which opens the project', () => {
  const agents = Array.from({ length: perProject + 2 }, (_, i) => agent('web', `agent-${i}`, { chat: 'running' }));
  const s = trayStatus(data({ agents, projects: [project('web')] }));
  assert.equal(s.projects[0].items.length, perProject);
  assert.equal(s.projects[0].more, 2);
  const more = trayMenu(s, { vm: 'none', keepRunning: false }).find((e) => e.label === '2 more…');
  assert.deepEqual(more?.action, { kind: 'go', target: { kind: 'project', project: 'web' } });
});

test('a project the list does not know yet still shows, after the rest', () => {
  const s = trayStatus(data({ agents: [agent('new', 'agent-1', { chat: 'running' }), agent('web', 'agent-2', { chat: 'running' })], projects: [project('web')] }));
  assert.deepEqual(
    s.projects.map((p) => p.name),
    ['web', 'new'],
  );
});

test('the menu has the actions, Pause all only with something to pause, the VM only in VM mode', () => {
  const idle = trayStatus(data());
  const labels = (vm: 'on' | 'off' | 'none') => trayMenu(idle, { vm, keepRunning: true }).filter((e) => e.action && e.action.kind !== 'go');
  assert.deepEqual(
    labels('none').map((e) => [e.label, e.enabled ?? true, e.checked]),
    [
      ['Open AgentBox', true, undefined],
      ['Pause all agents', false, undefined],
      ['Keep running when the window is closed', true, true],
      ['Quit AgentBox', true, undefined],
    ],
  );
  assert.equal(labels('on').find((e) => e.action?.kind === 'stopVM')?.enabled, true);
  assert.equal(labels('off').find((e) => e.action?.kind === 'stopVM')?.enabled, false);
  const busy = trayStatus(data({ agents: [agent('web', 'agent-1')] }));
  assert.equal(trayMenu(busy, { vm: 'none', keepRunning: false }).find((e) => e.action?.kind === 'pauseAll')?.enabled, true);
});

test('labels stay on one line, cut with an ellipsis', () => {
  assert.equal(oneLine('a\n  b\tc'), 'a b c');
  assert.equal(oneLine('x'.repeat(100), 10), `${'x'.repeat(9)}…`);
});
