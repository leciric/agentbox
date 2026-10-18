// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { api } from './api.ts';
import { buckets, formatMs, spanAgent, spanDetails } from './diagnostics.ts';

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
    { name: 'incus start', start: '2026-10-18T12:00:00Z', durationMs: 3200, id: 'abc', parent: 'def', error: 'boom', attrs: { agent: 'p/agent-01', status: 500 } },
    '0.9.0',
  );
  assert.match(text, /^span: incus start$/m);
  assert.match(text, /^duration: 3.20 s \(3200 ms\)$/m);
  assert.match(text, /^error: boom$/m);
  assert.match(text, /^agent: p\/agent-01$/m);
  assert.match(text, /^id: abc \(parent def\)$/m);
  assert.match(text, /^status: 500$/m);
  assert.match(text, /^agentbox: 0.9.0$/m);
  assert.equal(spanAgent({ name: 'x', start: '', durationMs: 0, attrs: { project: 'p' } }), 'p');
});

test('the diagnostics reads ask for their period, and the app posts its spans', async () => {
  const asked: string[] = [];
  (globalThis as unknown as { window: unknown }).window = {
    agentbox: {
      request: async (method: string, path: string, body?: unknown) => {
        asked.push(`${method} ${path}${body ? ` ${JSON.stringify(body)}` : ''}`);
        return { status: 204, body: '', contentType: '' };
      },
    },
  };
  await api.diagnosticSpans('30m');
  await api.diagnosticStalls('24h');
  await api.diagnosticResources('1h');
  await api.sendClientSpans([{ name: 'app long task', start: '2026-10-18T12:00:00Z', durationMs: 250 }]);
  assert.deepEqual(asked, [
    'GET /v1/diagnostics/spans?since=30m',
    'GET /v1/diagnostics/stalls?since=24h',
    'GET /v1/diagnostics/resources?window=1h',
    'POST /v1/diagnostics/client-spans {"spans":[{"name":"app long task","start":"2026-10-18T12:00:00Z","durationMs":250}]}',
  ]);
});
