import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { needRows, needsYou } from './inbox.ts';

const agent = (name: string, over: Partial<T.Agent> = {}) => ({ name, ref: `p/${name}`, project: 'p', state: 'running', chat: 'ready', ...over }) as T.Agent;
const question = (id: string) => ({ id, kind: 'question', project: 'p', agent: 'agent-1', ref: 'p/agent-1', at: '2026-10-15T10:00:00Z', seen: false }) as T.Notification;
const ready = (name: string) => ({ project: 'p', agent: name, ref: `p/${name}`, finishedAt: '2026-10-15T10:00:00Z', fingerprint: name }) as T.ReviewItem;

test('needsYou counts what waits on the user, each agent once', () => {
  const needs = needsYou({
    agents: [agent('asks', { chat: 'waiting' }), agent('broke', { chat: 'error' }), agent('failed', { outcome: 'failed' }), agent('busy', { chat: 'running' }), agent('idle')],
    questions: [question('q1')],
    // failed finished: it's in review, not counted again as an error.
    review: [ready('failed'), ready('other')],
    totals: { questions: 1, review: 2 },
  });
  assert.deepEqual(
    needs.permissions.map((a) => a.name),
    ['asks'],
  );
  assert.deepEqual(
    needs.errors.map((a) => a.name),
    ['broke'],
  );
  assert.deepEqual(
    needs.questions.map((n) => n.id),
    ['q1'],
  );
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

test('needRows puts every kind in one shape: permissions, questions newest first, errors', () => {
  const agents = [agent('asks', { chat: 'waiting', title: 'Pagination' }), agent('broke', { chat: 'error', outcomeNote: 'tests failed' })];
  const old = { ...question('q1'), at: '2026-10-15T09:00:00Z', text: 'Cursor?' };
  const recent = { ...question('q2'), at: '2026-10-15T11:00:00Z' };
  const needs = needsYou({ agents, questions: [old, recent], review: [], totals: { questions: 2, review: 0 } });
  const rows = needRows(needs, agents);
  assert.deepEqual(
    rows.map((r) => r.key),
    ['permission:p/asks', 'question:q2', 'question:q1', 'error:p/broke'],
  );
  assert.equal(rows[0]!.title, 'Pagination');
  assert.equal(rows[2]!.detail, 'Cursor?');
  assert.equal(rows[2]!.notice, old);
  assert.equal(rows[3]!.detail, 'tests failed');
  assert.ok(rows.every((r) => r.ref && r.project));
});

test('needsYou leaves finishes already opened out of the count, not the list', () => {
  const a = ready('a');
  const b = ready('b');
  const needs = needsYou({ agents: [], questions: [], review: [a, b], totals: { questions: 0, review: 2 }, seen: new Set([`${a.ref}:${a.fingerprint}`]) });
  assert.equal(needs.review.length, 2);
  assert.equal(needs.count, 1);
});
