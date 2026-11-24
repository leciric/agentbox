import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chooseRunOn, resetRunOnForTest, sentRunOn } from './runOn.ts';

test('a chat remembers its own choice, and owner is no entry', () => {
  const kept = new Map<string, string>();
  const store = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v) };
  resetRunOnForTest(store);
  Object.defineProperty(globalThis, 'localStorage', { value: store, configurable: true });
  chooseRunOn('r1~web/lead', 'peer');
  chooseRunOn('r1~web/lead-s2', 'owner');
  assert.deepEqual(JSON.parse(kept.get('agentbox.composer.runOn') ?? '{}'), { 'r1~web/lead': 'peer' });
  chooseRunOn('r1~web/lead', 'owner');
  assert.deepEqual(JSON.parse(kept.get('agentbox.composer.runOn') ?? '{}'), {});
});

test('a message asks for this computer only while it can take agents', () => {
  assert.equal(sentRunOn('peer', true), 'peer');
  assert.equal(sentRunOn('peer', false), 'owner');
  assert.equal(sentRunOn('peer', undefined), 'owner');
  assert.equal(sentRunOn('owner', true), 'owner');
});
