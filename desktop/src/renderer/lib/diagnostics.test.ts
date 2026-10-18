// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { api } from './api.ts';
import { buckets, formatMs, spanDetails } from './diagnostics.ts';

test('formatMs reads ms, then seconds, then minutes', () => {
  assert.equal(formatMs(4.2), '4 ms');
  assert.equal(formatMs(0.5), '0.50 ms');
  assert.equal(formatMs(1500), '1.50 s');
  assert.equal(formatMs(21_400), '21.4 s');
  assert.equal(formatMs(125_000), '2m 5s');
});

test('buckets averages samples per stretch and leaves gaps empty', () => {
  const now = Date.parse('2026-10-18T12:00:00Z');
  const at = (sAgo: number) => new Date(now - sAgo * 1000).toISOString();
  const samples = [
    { at: at(55), cpu: 10, memory: 0 },
    { at: at(52), cpu: 30, memory: 0 },
    { at: at(5), cpu: 80, memory: 0 },
    { at: at(120), cpu: 99, memory: 0 }, // before the window
  ];
  const out = buckets(samples, (s) => s.cpu, 60_000, 10_000, now);
  assert.equal(out.length, 6);
  assert.deepEqual(
    out.map((b) => [b.samples, b.avg, b.peak]),
    [
      [2, 20, 30],
      [0, 0, 0],
      [0, 0, 0],
      [0, 0, 0],
      [0, 0, 0],
      [1, 80, 80],
    ],
  );
});

test('spanDetails has what a bug report needs', () => {
  const text = spanDetails(
    { name: 'incus start', start: '2026-10-18T12:00:00Z', durationMs: 3200, traceId: 'abc', error: 'boom', attrs: { agent: 'p/agent-01' } },
    '0.9.0',
  );
  assert.match(text, /^span: incus start$/m);
  assert.match(text, /^duration: 3.20 s \(3200 ms\)$/m);
  assert.match(text, /^error: boom$/m);
  assert.match(text, /^agent: p\/agent-01$/m);
  assert.match(text, /^agentbox: 0.9.0$/m);
});

test('a daemon without /v1/diagnostics answers sample data, other errors still throw', async () => {
  let status = 404;
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: { request: async () => ({ status, body: '404 page not found', contentType: 'text/plain' }) },
  };
  const spans = await api.diagnosticSpans('30m');
  assert.equal(spans.sample, true);
  assert.ok(spans.stats.length > 0);
  assert.equal((await api.diagnosticResources('5m')).sample, true);
  status = 500;
  await assert.rejects(api.diagnosticStalls('24h'));
});
