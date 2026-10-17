// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import { addFetchedLog } from './events.ts';
import { agentPath, api, isProjectChat } from './api.ts';

test('isProjectChat is true for a bare project ref and for the lead', () => {
  assert.equal(isProjectChat('myproject'), true);
  assert.equal(isProjectChat(`myproject/${T.LeadName}`), true);
});

test('isProjectChat is false for an agent ref', () => {
  assert.equal(isProjectChat('myproject/agent-01'), false);
});

test('agentPath encodes the project and agent into the API path', () => {
  assert.equal(agentPath('myproject/agent-01'), '/v1/agents/myproject/agent-01');
  assert.equal(agentPath('my project/agent 01'), '/v1/agents/my%20project/agent%2001');
});

// The daemon answers a job that hasn't logged a line yet (a lead's memory
// consolidation, a removal) with an empty text/plain body. call turns an empty
// body into undefined, which reached addFetchedLog as its text and crashed the
// page with "Cannot read properties of undefined (reading 'replace')".
test('plain-text endpoints answer an empty body as an empty string', async () => {
  const paths: string[] = [];
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: {
      request: async (_method: string, path: string) => {
        paths.push(path);
        return { status: 200, body: '', contentType: 'text/plain; charset=utf-8' };
      },
    },
  };
  const log = await api.jobLog('job-1');
  assert.equal(log, '');
  assert.doesNotThrow(() => addFetchedLog('job-1', log));
  assert.equal(await api.diffStat('p/agent-01'), '');
  assert.equal(await api.brief('p'), '');
  assert.deepEqual(paths, ['/v1/jobs/job-1/log', '/v1/agents/p/agent-01/diff?stat=true', '/v1/projects/p/brief']);
});

// A daemon from before the review, notification and media lists paged answers
// them with the plain list, and one still running after the app was updated
// crashed it: App's error alert read getQueryData(reviewHeadKey)?.items.map,
// "Cannot read properties of undefined (reading 'map')".
test('paged lists read an older daemon’s plain list as one page', async () => {
  const item = { ref: 'p/agent-01' };
  const answers: Record<string, unknown> = { '/v1/review': [item], '/v1/notifications': [item], '/v1/media': [item] };
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: {
      request: async (_method: string, path: string) => ({ status: 200, body: JSON.stringify(answers[path.split('?')[0]]), contentType: 'application/json' }),
    },
  };
  const q = () => new URLSearchParams({ limit: '20' });
  const review = await api.readyToReviewPage(q());
  assert.deepEqual(review.items.map((i) => i.ref), ['p/agent-01']);
  assert.equal(review.total, 1);
  assert.equal(review.next, undefined);
  assert.deepEqual((await api.reviewedPage(q())).items.length, 1);
  assert.equal((await api.notificationPage(q())).total, 1);
  assert.equal((await api.mediaPage(q())).items.length, 1);
});

test('paged lists read a page with no items as empty', async () => {
  let body = '';
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: { request: async () => ({ status: 200, body, contentType: 'application/json' }) },
  };
  body = JSON.stringify({ items: null, total: 0 });
  assert.deepEqual((await api.readyToReviewPage(new URLSearchParams())).items, []);
  body = JSON.stringify({ total: 3, next: 'x' });
  assert.deepEqual(await api.notificationPage(new URLSearchParams()), { items: [], total: 3, next: 'x' });
  body = '';
  assert.deepEqual((await api.mediaPage(new URLSearchParams())).items, []);
});
