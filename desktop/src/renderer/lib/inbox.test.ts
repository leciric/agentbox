import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { needsYou } from './inbox.ts';

const agent = (name: string, over: Partial<T.Agent> = {}) => ({ name, ref: `p/${name}`, project: 'p', state: 'running', chat: 'ready', ...over }) as T.Agent;
const question = (id: string) => ({ id, kind: 'question', project: 'p', agent: 'agent-1', ref: 'p/agent-1', at: '2026-10-15T10:00:00Z', seen: false }) as T.Notification;
const ready = (name: string) => ({ project: 'p', agent: name, ref: `p/${name}`, finishedAt: '2026-10-15T10:00:00Z', fingerprint: name }) as T.ReviewItem;

test('needsYou counts what waits on the user, each agent once', () => {
  const needs = needsYou({
    agents: [
      agent('asks', { chat: 'waiting' }),
      agent('broke', { chat: 'error' }),
      agent('failed', { outcome: 'failed' }),
      agent('busy', { chat: 'running' }),
      agent('idle'),
    ],
    questions: [question('q1')],
    // failed finished: it's in review, not counted again as an error.
    review: [ready('failed'), ready('other')],
    totals: { questions: 1, review: 2 },
  });
  assert.deepEqual(needs.permissions.map((a) => a.name), ['asks']);
  assert.deepEqual(needs.errors.map((a) => a.name), ['broke']);
  assert.deepEqual(needs.questions.map((n) => n.id), ['q1']);
  assert.equal(needs.review.length, 2);
  assert.equal(needs.count, 5);
});

test('needsYou counts the whole lists, not the pages loaded', () => {
  const needs = needsYou({ agents: [], questions: [question('q1')], review: [ready('a'), ready('b')], totals: { questions: 45, review: 130 } });
  assert.equal(needs.count, 175);
  // Never less than what is on screen, as the totals are read separately.
  assert.equal(needsYou({ agents: [], questions: [question('q1')], review: [ready('a')], totals: { questions: 0, review: 0 } }).count, 2);
});

test('needsYou is nothing when nothing waits', () => {
  const needs = needsYou({ agents: [agent('idle')], questions: [], review: [], totals: { questions: 0, review: 0 } });
  assert.equal(needs.count, 0);
});
