import assert from 'node:assert/strict';
import { test } from 'node:test';
import { arrive } from './uiV2.ts';

const storage = (init: Record<string, string> = {}) => {
  const store = new Map(Object.entries(init));
  return {
    store,
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  };
};

test('a first run starts on v2', () => {
  const s = storage();
  assert.equal(arrive(s), 'fresh');
  assert.equal(s.store.get('agentbox.ui.v2'), '1');
});

test('an install that never touched the flag moves to v2, once', () => {
  const s = storage({ 'agentbox.whatsnew.lastSeenVersion': '0.12.1' });
  assert.equal(arrive(s), 'moved');
  assert.equal(s.store.get('agentbox.ui.v2'), '1');
  assert.equal(arrive(s), 'v2');
});

test('an install that turned v2 off keeps Classic', () => {
  const s = storage({ 'agentbox.whatsnew.lastSeenVersion': '0.12.1', 'agentbox.v2.projects': '{}' });
  assert.equal(arrive(s), 'classic');
  assert.equal(s.store.get('agentbox.ui.v2'), '0');
  assert.equal(arrive(s), 'classic');
});

test('a choice made stays', () => {
  assert.equal(arrive(storage({ 'agentbox.ui.v2': '1' })), 'v2');
  assert.equal(arrive(storage({ 'agentbox.ui.v2': '0', 'agentbox.v2.projects': '{}' })), 'classic');
});

test('the layout is v2 by default and stored as it changes', async () => {
  const s = storage();
  (globalThis as { localStorage?: unknown }).localStorage = s;
  const { launchArrival, setUiV2, uiV2 } = await import('./uiV2.ts');
  assert.equal(uiV2(), true);
  assert.equal(launchArrival(), 'fresh');
  setUiV2(false);
  assert.equal(uiV2(), false);
  assert.equal(s.store.get('agentbox.ui.v2'), '0');
  setUiV2(true);
  assert.equal(s.store.get('agentbox.ui.v2'), '1');
});
