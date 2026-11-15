// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import { addFetchedLog } from './events.ts';
import { agentPath, api, isProjectChat, sessionOfRef } from './api.ts';

test('isProjectChat is true for a bare project ref and for the lead', () => {
  assert.equal(isProjectChat('myproject'), true);
  assert.equal(isProjectChat(`myproject/${T.LeadName}`), true);
});

test('isProjectChat is false for an agent ref', () => {
  assert.equal(isProjectChat('myproject/agent-01'), false);
});

test('sessionOfRef reads the session of a project chat, and of the Home chat', () => {
  assert.equal(sessionOfRef(`web/${T.LeadName}`), 'main');
  assert.equal(sessionOfRef(`web/${T.LeadName}.prod`), 'prod');
  assert.equal(sessionOfRef(`${T.HomeProject}/${T.LeadName}`), 'main');
  assert.equal(sessionOfRef(`${T.HomeProject}/${T.LeadName}.weekly-planning`), 'weekly-planning');
  assert.equal(sessionOfRef('web/agent-01'), undefined);
  assert.equal(sessionOfRef('web'), undefined);
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

// The app updated, but its daemon is still the version before: a request
// newer than that daemon got Go's plain "404 page not found", which the Work
// page and the review list showed as is. It says which versions differ
// instead; a 404 the daemon itself answers (JSON) is left as it is.
test('a route the daemon lacks says the daemon is older than the app', async () => {
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: {
      info: async () => ({ socket: '', version: '0.17.0-nightly.20261028.152', electron: '', packaged: true, platform: 'linux' }),
      request: async (_method: string, path: string) => {
        if (path === '/v1/version') return { status: 200, body: '{"version":"0.15.0"}', contentType: 'application/json' };
        if (path.startsWith('/v1/work')) return { status: 404, body: '404 page not found\n', contentType: 'text/plain; charset=utf-8' };
        return { status: 404, body: '{"error":"project \\"p\\" not found"}', contentType: 'application/json' };
      },
    },
  };
  await assert.rejects(api.workPage(new URLSearchParams()), (err: Error & { code?: string; status?: number }) => {
    assert.equal(err.code, 'daemon_outdated');
    assert.equal(err.status, 404);
    assert.match(err.message, /0\.15\.0/);
    assert.match(err.message, /0\.17\.0-nightly\.20261028\.152/);
    return true;
  });
  await assert.rejects(api.readyToReviewPage(new URLSearchParams(), 'p'), /project "p" not found/);
});

test('a 404 from a daemon of the app’s own version is left as it is', async () => {
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: {
      info: async () => ({ socket: '', version: '0.16.0', electron: '', packaged: true, platform: 'linux' }),
      request: async (_method: string, path: string) =>
        path === '/v1/version'
          ? { status: 200, body: '{"version":"0.16.0"}', contentType: 'application/json' }
          : { status: 404, body: '404 page not found\n', contentType: 'text/plain; charset=utf-8' },
    },
  };
  await assert.rejects(api.workPage(new URLSearchParams()), (err: Error & { code?: string }) => err.code === undefined && /404 page not found/.test(err.message));
});
