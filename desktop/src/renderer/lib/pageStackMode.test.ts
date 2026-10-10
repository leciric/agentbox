import assert from 'node:assert/strict';
import { beforeEach, test } from 'node:test';

const store = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
};
const { pageStackMode, setPageStackMode } = await import('./pageStackMode.ts');

beforeEach(() => store.clear());

test('the stack mode is saved, and a stored value that is not one is the default', () => {
  assert.equal(pageStackMode(), 'new');
  for (const mode of ['always', 'never', 'new'] as const) {
    setPageStackMode(mode);
    assert.equal(pageStackMode(), mode);
    assert.equal(store.get('agentbox.pages.stack'), mode);
  }
});
