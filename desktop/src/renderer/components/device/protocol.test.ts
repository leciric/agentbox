import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fit, keyInput, parseMessage, videoPoint, wheelClicks } from './protocol.ts';

const bytes = (...parts: (number[] | string)[]) =>
  new Uint8Array(parts.flatMap((p) => (typeof p === 'string' ? [...new TextEncoder().encode(p)] : p)));

test('parseMessage reads a config, with its description from base64', () => {
  const msg = parseMessage(bytes([0x01], JSON.stringify({ v: 1, platform: 'android', codec: 'avc1.42c01f', width: 720, height: 1600, description: 'AULAHw==' })));
  assert.equal(msg?.kind, 'config');
  if (msg?.kind !== 'config') return;
  assert.equal(msg.config.codec, 'avc1.42c01f');
  assert.equal(msg.config.width, 720);
  assert.deepEqual([...msg.config.description], [1, 0x42, 0xc0, 0x1f]);
});

test('parseMessage reads a frame: keyframe flag, timestamp and AVCC data', () => {
  // As internal/devicestream encodes one: 1_700_000_000_123_456 µs.
  const ts = [0x00, 0x06, 0x0a, 0x24, 0x18, 0x20, 0x22, 0x40];
  const msg = parseMessage(bytes([0x02, 0x01], ts, [0, 0, 0, 1, 0x65]));
  assert.deepEqual(msg && { ...msg, data: msg.kind === 'frame' ? [...msg.data] : null }, {
    kind: 'frame',
    key: true,
    timestamp: 1_700_000_000_123_456,
    data: [0, 0, 0, 1, 0x65],
  });
  const delta = parseMessage(bytes([0x02, 0x00], ts, [0, 0, 0, 1, 0x41]));
  assert.equal(delta?.kind === 'frame' && delta.key, false);
});

test('parseMessage reads a status, and skips what it does not know', () => {
  assert.deepEqual(parseMessage(bytes([0x03], '{"error":"the emulator stopped"}')), { kind: 'status', error: 'the emulator stopped' });
  assert.equal(parseMessage(bytes([0x09, 1, 2])), null);
  assert.equal(parseMessage(bytes([0x02, 1])), null);
  assert.equal(parseMessage(new Uint8Array()), null);
});

test('videoPoint maps the page to 0..1 across the video, clamped', () => {
  const rect = { left: 100, top: 50, width: 200, height: 400 };
  assert.deepEqual(videoPoint(200, 150, rect), { x: 0.5, y: 0.25 });
  assert.deepEqual(videoPoint(0, 1000, rect), { x: 0, y: 1 });
});

test('fit keeps the video’s shape in the room it has', () => {
  assert.deepEqual(fit(720, 1600, 500, 500), { width: 225, height: 500 });
  assert.deepEqual(fit(1600, 720, 500, 500), { width: 500, height: 225 });
  assert.deepEqual(fit(720, 1600, 0, 500), { width: 0, height: 0 });
});

test('keyInput types characters and names other keys', () => {
  const key = (k: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean }> = {}) => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, ...mods });
  assert.deepEqual(keyInput(key('a'), 'down'), { type: 'text', text: 'a' });
  assert.equal(keyInput(key('a'), 'up'), null);
  assert.deepEqual(keyInput(key('é'), 'down'), { type: 'text', text: 'é' });
  assert.deepEqual(keyInput(key('Enter'), 'down'), { type: 'key', action: 'down', key: 'Enter' });
  assert.deepEqual(keyInput(key('Backspace'), 'up'), { type: 'key', action: 'up', key: 'Backspace' });
  assert.deepEqual(keyInput(key('a', { ctrlKey: true }), 'down'), { type: 'key', action: 'down', key: 'a' });
  assert.equal(keyInput(key('F5'), 'down'), null);
  assert.equal(keyInput(key('Dead'), 'down'), null);
});

test('wheelClicks counts clicks in pixels, lines or pages', () => {
  assert.equal(wheelClicks(100, 0), 1);
  assert.equal(wheelClicks(-3, 1), -1);
  assert.equal(wheelClicks(1, 2), 3);
});
