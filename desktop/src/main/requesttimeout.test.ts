// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { request as httpRequest, createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { test } from 'node:test';
import { DaemonTimeoutError, idleTimeout, readIdleMs, timedOut, writeIdleMs } from './requesttimeout.ts';

test('reads give up sooner than writes', () => {
  assert.equal(idleTimeout('GET'), readIdleMs);
  assert.equal(idleTimeout('HEAD'), readIdleMs);
  assert.equal(idleTimeout('POST'), writeIdleMs);
  assert.ok(readIdleMs < writeIdleMs);
});

test('a timeout answers 504 daemon_timeout, as the daemon would refuse', () => {
  const res = timedOut(new DaemonTimeoutError('GET', '/v1/agents?all=1', 30_000));
  assert.equal(res.status, 504);
  const body = JSON.parse(res.body) as { error: string; code: string };
  assert.equal(body.code, 'daemon_timeout');
  assert.match(body.error, /didn't answer GET \/v1\/agents within 30 s/);
});

// The way daemon.ts uses it: a daemon that never answers ends the request
// with the error, rather than leaving it out for good.
test('a request to a daemon that never answers ends with DaemonTimeoutError', async () => {
  const server = createServer(() => {}).listen(0, '127.0.0.1');
  await new Promise((r) => server.once('listening', r));
  try {
    const err = await new Promise<Error>((resolve) => {
      const req = httpRequest({ port: (server.address() as AddressInfo).port, host: '127.0.0.1', path: '/v1/agents' });
      req.setTimeout(50, () => req.destroy(new DaemonTimeoutError('GET', '/v1/agents', 50)));
      req.on('error', resolve);
      req.end();
    });
    assert.ok(err instanceof DaemonTimeoutError);
  } finally {
    server.closeAllConnections();
    server.close();
  }
});
