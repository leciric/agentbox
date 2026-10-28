import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isPlainHTTP, isTailnet, parsePairingLink } from './pairing.ts';

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

test('a tailnet address is a MagicDNS name or a Tailscale IP', () => {
  for (const origin of ['https://agentbox-vps-3fa9c0.tail1234.ts.net', 'https://AGENTBOX.tail1234.TS.NET:443', 'http://100.101.102.103:7781', 'http://100.64.0.1']) {
    assert.equal(isTailnet(origin), true, origin);
  }
  for (const origin of ['https://x.trycloudflare.com', 'http://192.168.1.20:7780', 'http://100.63.0.1', 'http://100.128.0.1', 'https://ts.network', 'nonsense']) {
    assert.equal(isTailnet(origin), false, origin);
  }
});
