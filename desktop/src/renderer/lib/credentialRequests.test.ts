// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { groupRequests, requestTarget } from './credentialRequests.ts';

let n = 0;
const ask = (q: Partial<T.Question>): T.Question => ({
  id: `q${++n}`,
  project: 'p',
  agent: `agent-${n}`,
  ref: `p/agent-${n}`,
  question: 'why',
  status: 'escalated',
  createdAt: new Date(Date.UTC(2026, 9, 29, 12, n)).toISOString(),
  ...q,
});

test('waiting requests for any GitHub account are one group, oldest first', () => {
  const later = ask({ kind: 'github', createdAt: '2026-10-29T13:00:00Z' });
  const earlier = ask({ kind: 'github', createdAt: '2026-10-29T12:00:00Z' });
  const groups = groupRequests([later, earlier]);
  assert.equal(groups.length, 1);
  assert.deepEqual(groups[0].questions.map((q) => q.id), [earlier.id, later.id]);
});

test('secrets group by name, and not with a GitHub account', () => {
  const groups = groupRequests([
    ask({ kind: 'secret', secretName: 'STRIPE_KEY' }),
    ask({ kind: 'secret', secretName: 'STRIPE_KEY', status: 'pending' }),
    ask({ kind: 'secret', secretName: 'OTHER' }),
    ask({ kind: 'github' }),
  ]);
  assert.deepEqual(groups.map((g) => g.questions.length), [2, 1, 1]);
});

test('connectors group by name and server, so a lookalike stays apart', () => {
  const a = ask({ kind: 'connector', connector: 'notion', url: 'https://mcp.notion.com/mcp' });
  const b = ask({ kind: 'connector', connector: 'notion', url: 'https://mcp.notion.com/mcp/' });
  const c = ask({ kind: 'connector', connector: 'notion', url: 'https://notion.evil.example/mcp' });
  assert.equal(requestTarget(a), requestTarget(b));
  assert.notEqual(requestTarget(a), requestTarget(c));
  assert.equal(groupRequests([a, c]).length, 2);
});

test('a settled request leaves the waiting group, and groups only with ones settled alike', () => {
  const groups = groupRequests([
    ask({ kind: 'github' }),
    ask({ kind: 'github', status: 'answered', answer: 'account work' }),
    ask({ kind: 'github', status: 'answered', answer: 'account work' }),
    ask({ kind: 'github', status: 'answered', answer: 'refused' }),
  ]);
  assert.deepEqual(groups.map((g) => g.questions.length), [1, 2, 1]);
});

test('a secret request with no name is its own', () => {
  assert.equal(groupRequests([ask({ kind: 'secret' }), ask({ kind: 'secret' })]).length, 2);
});
