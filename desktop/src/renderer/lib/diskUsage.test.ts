// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { setLanguage } from '../../shared/i18n/index.ts';
import { diskCategoryLabel, diskItemLabel } from './diskUsage.ts';

test('categories and items are named in the app language, agents by their own name', () => {
  setLanguage('pt-BR');
  try {
    assert.equal(diskCategoryLabel({ kind: 'caches', label: 'Shared caches', bytes: 0 }), 'Caches compartilhados');
    assert.equal(diskItemLabel({ label: 'web base', bytes: 0, kind: 'project-base', name: 'web' }), 'Base salva de web');
    assert.equal(diskItemLabel({ label: 'Package cache: other', bytes: 0, kind: 'package-cache', name: 'other' }), 'Cache de pacotes: outros');
    assert.equal(diskItemLabel({ label: 'web/agent-3', bytes: 0 }), 'web/agent-3');
  } finally {
    setLanguage('en-US');
  }
  assert.equal(diskCategoryLabel({ kind: 'machines', label: 'Agent machines', bytes: 0 }), 'Agent machines');
  assert.equal(diskCategoryLabel({ kind: 'later', label: 'Something new', bytes: 0 }), 'Something new');
});
