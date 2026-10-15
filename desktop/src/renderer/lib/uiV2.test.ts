import assert from 'node:assert/strict';
import { test } from 'node:test';

test('the v2 flag is off by default and stored as it changes', async () => {
  const store = new Map<string, string>();
  (globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  };
  const { setUiV2, uiV2 } = await import('./uiV2.ts');
  assert.equal(uiV2(), false);
  setUiV2(true);
  assert.equal(uiV2(), true);
  assert.equal(store.get('agentbox.ui.v2'), '1');
  setUiV2(false);
  assert.equal(uiV2(), false);
  assert.equal(store.has('agentbox.ui.v2'), false);
});
