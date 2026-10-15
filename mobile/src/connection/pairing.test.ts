import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isPlainHTTP, parsePairingLink } from './pairing.ts';

test('a pairing link gives the daemon and the secret', () => {
  assert.deepEqual(parsePairingLink('http://192.168.1.20:7780/#pair=abc_DEF-1'), { origin: 'http://192.168.1.20:7780', secret: 'abc_DEF-1' });
  assert.deepEqual(parsePairingLink('  https://quiet-fox.trycloudflare.com/#pair=xyz\n'), { origin: 'https://quiet-fox.trycloudflare.com', secret: 'xyz' });
  assert.deepEqual(parsePairingLink('http://box.local:7780/#/p/web&pair=s3'), { origin: 'http://box.local:7780', secret: 's3' });
});

test('anything else is not a pairing link', () => {
  for (const raw of ['', 'hello', 'http://192.168.1.20:7780/', 'http://h/#pair=', 'ftp://h/#pair=x', 'http://h/?pair=x', 'http://h/#notpair=x']) {
    assert.equal(parsePairingLink(raw), null, raw);
  }
});

test('the local network is plain HTTP, the tunnel is not', () => {
  assert.equal(isPlainHTTP('http://192.168.1.20:7780'), true);
  assert.equal(isPlainHTTP('https://x.trycloudflare.com'), false);
});
