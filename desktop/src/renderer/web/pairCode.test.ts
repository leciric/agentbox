import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pairSecretOf } from './pairCode.ts';

test('pairSecretOf finds the secret in a pairing link, or takes the code alone', () => {
  const cases: [string, string | null][] = [
    ['https://libraries-exotic.trycloudflare.com/#pair=qgQOEvYHAL1tbKHyHt374g', 'qgQOEvYHAL1tbKHyHt374g'],
    ['http://192.168.1.20:7780/#pair=qgQOEvYHAL1tbKHyHt374g', 'qgQOEvYHAL1tbKHyHt374g'],
    ['http://192.168.1.20:7780/#x=1&pair=a-b_c0123456789abcd ', 'a-b_c0123456789abcd'],
    ['  qgQOEvYHAL1tbKHyHt374g\n', 'qgQOEvYHAL1tbKHyHt374g'],
    ['https://example.com/', null],
    ['hello', null],
    ['not a code at all, a sentence', null],
    ['', null],
  ];
  for (const [text, want] of cases) assert.equal(pairSecretOf(text), want, text);
});
