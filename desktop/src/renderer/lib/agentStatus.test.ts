// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { avatarMood, awaiting, chatLabel, isAsking, memoryHold, projectTone, rank, settled, summarizeStatus } from './agentStatus.ts';

const agent = (state: string, chat?: string): T.Agent => ({ state, chat }) as T.Agent;

test('chat waiting for you beats everything else', () => {
  assert.deepEqual(chatLabel(agent('running', 'waiting')), { text: 'Needs you', tone: 'urgent' });
});

test('a broken machine needs attention even mid-chat', () => {
  assert.deepEqual(chatLabel(agent('incomplete')), { text: 'Needs attention', tone: 'error' });
  assert.deepEqual(chatLabel(agent('missing')), { text: 'Missing', tone: 'error' });
});

test('initializing shows as live, not as needing attention', () => {
  assert.deepEqual(chatLabel(agent('initializing')), { text: 'Initializing', tone: 'live' });
});

test('a running chat is Working, an idle one is Idle or Starting', () => {
  assert.deepEqual(chatLabel(agent('running', 'running')), { text: 'Working', tone: 'live' });
  assert.deepEqual(chatLabel(agent('running', 'starting')), { text: 'Starting', tone: 'muted' });
  assert.deepEqual(chatLabel(agent('running')), { text: 'Idle', tone: 'muted' });
});

test('a stalled turn is Stalled, not Working, and a stopped chat is not Idle', () => {
  const stalled = { ...agent('running', 'running'), stalledSince: '2026-09-29T18:00:00Z' } as T.Agent;
  assert.deepEqual(chatLabel(stalled), { text: 'Stalled', tone: 'error' });
  assert.equal(avatarMood(stalled), 'error');
  assert.equal(settled(stalled), false);
  assert.equal(projectTone([agent('running', 'running'), stalled]), 'error');
  assert.deepEqual(chatLabel(agent('running', 'error')), { text: 'Chat stopped', tone: 'error' });
  // Stopped with its machine, the chat's error is old news.
  assert.deepEqual(chatLabel(agent('stopped', 'error')), { text: 'Stopped', tone: 'muted' });
});

test('a queued agent, left by an earlier release, reads like one starting', () => {
  assert.deepEqual(chatLabel(agent('queued')), { text: 'Initializing', tone: 'live' });
});

test('memory pressure holding back a running agent’s commands shows as a warning', () => {
  const since = '2026-10-10T10:00:00Z';
  const paused = { ...agent('running', 'running'), memory: { paused: 1, since } } as T.Agent;
  assert.deepEqual(chatLabel(paused), { text: 'Paused for memory', tone: 'warning' });
  assert.match(memoryHold(paused)?.tip ?? '', /resume by themselves/);
  const waiting = { ...agent('running', 'ready'), memory: { waiting: 2, since } } as T.Agent;
  assert.deepEqual(chatLabel(waiting), { text: 'Waiting for memory', tone: 'warning' });
  // Paused wins over waiting; neither shows once nothing is held or the machine isn't running.
  assert.equal(chatLabel({ ...paused, memory: { paused: 1, waiting: 1, since } } as T.Agent).text, 'Paused for memory');
  assert.deepEqual(chatLabel({ ...paused, memory: { since } } as T.Agent), { text: 'Working', tone: 'live' });
  assert.equal(memoryHold({ ...paused, state: 'stopped' }), undefined);
  // Needing you still comes first, and a hold isn't settled.
  assert.deepEqual(chatLabel({ ...paused, chat: 'waiting' } as T.Agent), { text: 'Needs you', tone: 'urgent' });
  assert.equal(settled(waiting), false);
  assert.equal(projectTone([agent('running'), waiting]), 'warning');
});

test('paused and stopped machines show their own state', () => {
  assert.deepEqual(chatLabel(agent('paused')), { text: 'Paused', tone: 'muted' });
  assert.deepEqual(chatLabel(agent('stopped')), { text: 'Stopped', tone: 'muted' });
});

test('rank puts what needs you first, then running, paused, then the rest', () => {
  assert.equal(rank(agent('running', 'waiting')), 0);
  assert.equal(rank(agent('incomplete')), 0);
  assert.equal(rank(agent('running')), 1);
  assert.equal(rank(agent('paused')), 2);
  assert.equal(rank(agent('stopped')), 3);
});

test('initializing does not jump the queue the way needing you does', () => {
  assert.equal(rank(agent('initializing')), rank(agent('running')));
  assert.ok(rank(agent('initializing')) > rank(agent('incomplete')));
});

test('settled is true only for a quiet, non-starting agent', () => {
  assert.equal(settled(agent('running')), true);
  assert.equal(settled(agent('paused')), true);
  assert.equal(settled(agent('running', 'starting')), false);
  assert.equal(settled(agent('running', 'waiting')), false);
  assert.equal(settled(agent('running', 'running')), false);
});

test('initializing is not settled: it is still moving', () => {
  assert.equal(settled(agent('initializing')), false);
});

test('projectTone picks the most urgent tone among the agents', () => {
  assert.equal(projectTone([agent('running'), agent('running', 'running')]), 'live');
  assert.equal(projectTone([agent('running', 'running'), agent('running', 'waiting')]), 'urgent');
});

test('projectTone is undefined when every agent is merely settled', () => {
  assert.equal(projectTone([agent('running'), agent('paused')]), undefined);
  assert.equal(projectTone([]), undefined);
});

test('avatarMood reads error, sleeping, asking, working and idle from state and chat', () => {
  assert.equal(avatarMood({ state: 'incomplete' }), 'error');
  assert.equal(avatarMood({ state: 'running', chat: 'error' }), 'error');
  assert.equal(avatarMood({ state: 'paused' }), 'sleeping');
  assert.equal(avatarMood({ state: 'running', chat: 'waiting' }), 'asking');
  assert.equal(avatarMood({ state: 'running' }, true), 'asking');
  assert.equal(avatarMood({ state: 'running', chat: 'running' }), 'working');
  assert.equal(avatarMood({ state: 'running', chat: 'starting' }), 'working');
  assert.equal(avatarMood({ state: 'running' }), 'idle');
});

test('initializing does not read as broken the way incomplete does', () => {
  assert.equal(avatarMood({ state: 'incomplete' }), 'error');
  assert.notEqual(avatarMood({ state: 'initializing' }), 'error');
});

const question = (ref: string, status: string, kind?: string): T.Question => ({ ref, status, kind }) as T.Question;

test('isAsking is true for an escalated question, or a pending one with a kind', () => {
  const questions = [question('agent-01', 'escalated'), question('agent-02', 'pending', 'credential'), question('agent-03', 'pending')];
  assert.equal(isAsking(questions, 'agent-01'), true);
  assert.equal(isAsking(questions, 'agent-02'), true);
  assert.equal(isAsking(questions, 'agent-03'), false);
  assert.equal(isAsking(questions, 'agent-04'), false);
  assert.equal(isAsking(undefined, 'agent-01'), false);
});

test('summarizeStatus groups agents by label and counts them', () => {
  const items = summarizeStatus([agent('running'), agent('running'), agent('paused')]);
  assert.deepEqual(items, [
    { text: 'Idle', tone: 'muted', count: 2 },
    { text: 'Paused', tone: 'muted', count: 1 },
  ]);
});

test('summarizeStatus orders groups by urgency, not by first appearance', () => {
  const items = summarizeStatus([agent('paused'), agent('running', 'waiting'), agent('running', 'running')]);
  assert.deepEqual(
    items.map((i) => i.text),
    ['Needs you', 'Working', 'Paused'],
  );
});

test('a turn that ended on background work is Awaiting, kept out of Finished, until the work ends', () => {
  const waiting = { ...agent('running', 'ready'), background: ['Watch CI run 42'] } as T.Agent;
  assert.deepEqual(chatLabel(waiting), { text: 'Awaiting', tone: 'live' });
  assert.ok(awaiting(waiting));
  assert.ok(!settled(waiting), 'an awaiting agent went into Finished');
  // The work ends and wakes it: Working, whatever is still listed.
  assert.deepEqual(chatLabel({ ...waiting, chat: 'running' }), { text: 'Working', tone: 'live' });
  // Its follow-up turn ends with nothing pending: Idle, and Finished.
  const done = { ...waiting, background: undefined };
  assert.deepEqual(chatLabel(done), { text: 'Idle', tone: 'muted' });
  assert.ok(settled(done));
  // A stopped machine has nothing running, whatever it last said.
  assert.equal(chatLabel({ ...waiting, state: 'stopped' }).text, 'Stopped');
});
