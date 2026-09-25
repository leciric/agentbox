// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { avatarMood, chatLabel, isAsking, projectTone, rank, settled, summarizeStatus } from './agentStatus.ts';

const agent = (state: string, chat?: string): T.Agent => ({ state, chat }) as T.Agent;

test('chat waiting for you beats everything else', () => {
  assert.deepEqual(chatLabel(agent('running', 'waiting')), { text: 'Needs you', tone: 'urgent' });
});

test('a broken machine needs attention even mid-chat', () => {
  assert.deepEqual(chatLabel(agent('incomplete')), { text: 'Needs attention', tone: 'error' });
  assert.deepEqual(chatLabel(agent('missing')), { text: 'Missing', tone: 'error' });
});

test('a running chat is Working, an idle one is Idle or Starting', () => {
  assert.deepEqual(chatLabel(agent('running', 'running')), { text: 'Working', tone: 'live' });
  assert.deepEqual(chatLabel(agent('running', 'starting')), { text: 'Starting', tone: 'muted' });
  assert.deepEqual(chatLabel(agent('running')), { text: 'Idle', tone: 'muted' });
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

test('settled is true only for a quiet, non-starting agent', () => {
  assert.equal(settled(agent('running')), true);
  assert.equal(settled(agent('paused')), true);
  assert.equal(settled(agent('running', 'starting')), false);
  assert.equal(settled(agent('running', 'waiting')), false);
  assert.equal(settled(agent('running', 'running')), false);
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
