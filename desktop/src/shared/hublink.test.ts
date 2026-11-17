// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseHubLink } from './hublink.ts';

test('parseHubLink keeps a hub\'s origin, and a setup link\'s secret', () => {
  assert.deepEqual(parseHubLink(' https://hub.example.com/ '), { url: 'https://hub.example.com' });
  assert.deepEqual(parseHubLink('hub.example.com'), { url: 'https://hub.example.com' });
  assert.deepEqual(parseHubLink('http://localhost:8080/hub/approve?code=BCDF-GHJK'), { url: 'http://localhost:8080' });
  assert.deepEqual(parseHubLink('https://h.example/hub/setup#abx_l_s3cret'), { url: 'https://h.example', secret: 'abx_l_s3cret' });
  assert.deepEqual(parseHubLink('https://h.example/#other'), { url: 'https://h.example' });
});

test('parseHubLink refuses what isn\'t a hub\'s address', () => {
  for (const input of ['', '   ', 'ftp://hub.example.com', 'https://']) assert.equal(parseHubLink(input), null, input);
});
