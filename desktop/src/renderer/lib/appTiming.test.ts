// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ApiError, api } from './api.ts';
import { clientSpansPath, flush, pending, recordLongTask, recordRequest, routeName, startAppTiming, stopAppTiming } from './appTiming.ts';
import type { ClientSpan } from './diagnostics.ts';

test('routeName keeps the route and drops the names and ids in it', () => {
  assert.equal(routeName('GET', '/v1/agents'), 'GET /v1/agents');
  assert.equal(routeName('GET', '/v1/agents/pawly/agent-01/chat?limit=50'), 'GET /v1/agents/{}/{}/chat');
  assert.equal(routeName('PATCH', '/v1/projects/pawly'), 'PATCH /v1/projects/{}');
  assert.equal(routeName('DELETE', '/v1/projects/pawly/schedules/s1'), 'DELETE /v1/projects/{}/schedules/{}');
  assert.equal(routeName('GET', '/v1/agents/p/a/files/list?path=src'), 'GET /v1/agents/{}/{}/files/list');
  assert.equal(routeName('GET', '/v1/agents/p/a/chat/files/shot.png'), 'GET /v1/agents/{}/{}/chat/files/{}');
});

test('nothing is recorded until the timing is started', () => {
  stopAppTiming();
  recordRequest('GET', '/v1/agents', performance.now(), 200);
  recordLongTask(0, 500);
  assert.equal(pending().length, 0);
});

test('requests and long tasks over the threshold are batched and sent together', async () => {
  const sent: ClientSpan[][] = [];
  startAppTiming(async (spans) => void sent.push(spans));
  recordRequest('GET', '/v1/agents/p/a', performance.now() - 12, 200);
  recordRequest('POST', clientSpansPath, performance.now(), 204);
  recordLongTask(100, 120);
  recordLongTask(100, 260);
  assert.equal(pending().length, 2);
  await flush();
  assert.equal(sent.length, 1);
  assert.deepEqual(
    sent[0].map((s) => [s.kind, s.name]),
    [
      ['request', 'app GET /v1/agents/{}/{}'],
      ['longtask', 'app long task'],
    ],
  );
  assert.ok(sent[0][0].durationMs >= 12);
  stopAppTiming();
});

test('a daemon without the endpoint turns the timing off', async () => {
  let calls = 0;
  startAppTiming(async () => {
    calls++;
    throw new ApiError('404 page not found', 404);
  });
  recordRequest('GET', '/v1/agents', performance.now(), 200);
  await flush();
  recordRequest('GET', '/v1/agents', performance.now(), 200);
  assert.equal(pending().length, 0);
  await flush();
  assert.equal(calls, 1);
});

test('a batch that fails to send for another reason is dropped and timing goes on', async () => {
  startAppTiming(async () => {
    throw new ApiError('connection refused', 0);
  });
  recordRequest('GET', '/v1/agents', performance.now(), 200);
  await flush();
  assert.equal(pending().length, 0);
  recordRequest('GET', '/v1/agents', performance.now(), 200);
  assert.equal(pending().length, 1);
  stopAppTiming();
});

test('api calls are timed, but not the posts of the spans themselves', async () => {
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: { request: async () => ({ status: 200, body: '[]', contentType: 'application/json' }) },
  };
  startAppTiming(async () => {});
  await api.agents();
  await api.sendClientSpans([]);
  assert.deepEqual(
    pending().map((s) => [s.name, s.status]),
    [['app GET /v1/agents', 200]],
  );
  stopAppTiming();
});
