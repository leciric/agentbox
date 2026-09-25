// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { relayRefused } from './relay.ts';

test('relayRefused is true only for the relay\'s own not-listening answer', () => {
  assert.equal(relayRefused(503, { 'x-agentbox-relay': 'not-listening' }), true);
});

test('relayRefused is false for any other status or header value', () => {
  assert.equal(relayRefused(200, { 'x-agentbox-relay': 'not-listening' }), false);
  assert.equal(relayRefused(503, {}), false);
  assert.equal(relayRefused(503, { 'x-agentbox-relay': 'something-else' }), false);
});
