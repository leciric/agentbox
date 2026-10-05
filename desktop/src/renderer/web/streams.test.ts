import assert from 'node:assert/strict';
import { test } from 'node:test';
import { webStreams } from './streams.ts';

// FakeSocket stands in for the browser's WebSocket.
class FakeSocket {
  static OPEN = 1;
  static last: FakeSocket;
  readyState = 0;
  binaryType = '';
  sent: unknown[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((m: { data: unknown }) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: ((e: { code: number; reason: string }) => void) | null = null;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeSocket.last = this;
  }
  send(data: unknown) {
    this.sent.push(data);
  }
  close() {
    this.onclose?.({ code: 1000, reason: '' });
  }
}

test('webStreams relays a WebSocket as the bridge stream API', async () => {
  (globalThis as { WebSocket?: unknown }).WebSocket = FakeSocket;
  const { stream, closeAll } = webStreams((path) => `ws://127.0.0.1:7790${path}`);
  const events: string[] = [];
  stream.onOpened((id) => events.push(`opened ${id}`));
  stream.onData((id, data) => events.push(`data ${id} ${[...data].join(',')}`));
  const stop = stream.onExited((id, reason) => events.push(`exited ${id} ${reason}`));

  const id = await stream.open('/api/machines/m/view');
  const ws = FakeSocket.last;
  assert.equal(ws.url, 'ws://127.0.0.1:7790/api/machines/m/view');
  assert.equal(ws.binaryType, 'arraybuffer');

  stream.write(id, new Uint8Array([1])); // not open yet: dropped
  ws.readyState = 1;
  ws.onopen?.();
  ws.onmessage?.({ data: new Uint8Array([7, 8]).buffer });
  stream.write(id, new Uint8Array([2, 3]));
  assert.deepEqual(ws.sent.map((d) => [...(d as Uint8Array)]), [[2, 3]]);

  ws.onclose?.({ code: 1011, reason: 'connect ECONNREFUSED' });
  ws.onclose?.({ code: 1000, reason: '' }); // only once
  assert.deepEqual(events, [`opened ${id}`, `data ${id} 7,8`, `exited ${id} connect ECONNREFUSED`]);

  stop();
  const second = await stream.open('/x');
  closeAll();
  assert.equal(events.length, 3, `${second} exited to a listener that stopped listening`);
});
