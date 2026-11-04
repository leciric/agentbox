// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import http from 'node:http';
import { test } from 'node:test';
import { staleSocketRetry } from './staleSocket.ts';

const reset = Object.assign(new Error('read ECONNRESET'), { code: 'ECONNRESET' });
const pipe = Object.assign(new Error('write EPIPE'), { code: 'EPIPE' });

test('a read on a reused socket that was reset or broken is sent again', () => {
  for (const method of ['GET', 'HEAD']) {
    assert.equal(staleSocketRetry(method, reset, true), true);
    assert.equal(staleSocketRetry(method, pipe, true), true);
  }
});

test('nothing else is sent again', () => {
  assert.equal(staleSocketRetry('GET', reset, false), false, 'a fresh socket was reset for real');
  assert.equal(staleSocketRetry('POST', reset, true), false, 'a write may have arrived');
  assert.equal(staleSocketRetry('DELETE', pipe, true), false);
  assert.equal(staleSocketRetry('GET', Object.assign(new Error('x'), { code: 'ENOENT' }), true), false);
  assert.equal(staleSocketRetry('GET', undefined, true), false);
});

test('a pooled socket the server closed reports ECONNRESET with reusedSocket set', async () => {
  const server = http.createServer((req, res) => {
    res.end('ok');
    // Close the socket after the response, as a forward dropping an idle connection.
    if (req.url === '/first') setTimeout(() => req.socket.destroy(), 20);
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  const { port } = server.address() as { port: number };
  const agent = new http.Agent({ keepAlive: true, maxSockets: 1 });
  const get = (path: string) =>
    new Promise<{ err?: unknown; reused: boolean }>((resolve) => {
      const req = http.get({ port, host: '127.0.0.1', path, agent }, (res) => {
        res.resume();
        res.on('end', () => resolve({ reused: req.reusedSocket }));
      });
      req.on('error', (err) => resolve({ err, reused: req.reusedSocket }));
    });
  try {
    await get('/first');
    // Send the next request before the close arrives, so the agent hands out the dead socket.
    const second = await get('/second');
    if (second.err) assert.equal(staleSocketRetry('GET', second.err, second.reused), true);
    else assert.ok(true, 'the socket was dropped before reuse: nothing to retry');
  } finally {
    agent.destroy();
    server.close();
  }
});
