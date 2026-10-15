import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { needsYou } from './inbox.ts';

const agent = (name: string, over: Partial<T.Agent> = {}) => ({ name, ref: `p/${name}`, project: 'p', state: 'running', chat: 'ready', ...over }) as T.Agent;
const notice = (id: string, kind: string, seen = false) => ({ id, kind, project: 'p', agent: 'agent-1', ref: 'p/agent-1', at: '2026-10-15T10:00:00Z', seen }) as T.Notification;
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
    notices: [notice('q1', 'question'), notice('q2', 'question', true), notice('d1', 'done'), notice('m1', 'media', true)],
    // failed finished: it's in review, not counted again as an error.
    review: [ready('failed'), ready('other')],
  });
  assert.deepEqual(needs.permissions.map((a) => a.name), ['asks']);
  assert.deepEqual(needs.errors.map((a) => a.name), ['broke']);
  assert.deepEqual(needs.questions.map((n) => n.id), ['q1']);
  assert.equal(needs.review.length, 2);
  assert.equal(needs.count, 5);
  assert.equal(needs.news, 1);
});

test('needsYou is nothing when nothing waits', () => {
  const needs = needsYou({ agents: [agent('idle')], notices: [notice('d1', 'done', true)], review: [] });
  assert.equal(needs.count, 0);
  assert.equal(needs.news, 0);
});
