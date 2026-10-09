// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { setLanguage } from '../../shared/i18n/index.ts';
import { failureMessage, updateHint, updateLabel } from './appUpdate.ts';

test('the update item says how far the update got', () => {
  setLanguage('en-US');
  assert.equal(updateLabel(null), null);
  assert.equal(updateLabel('preparing'), 'Preparing the update…');
  assert.equal(updateLabel({ phase: 'download', version: '0.12.0', received: 50, total: 200 }), 'Downloading… 25%');
  assert.equal(updateLabel({ phase: 'download', version: '0.12.0', received: 50, total: 0 }), 'Downloading… 0%');
  assert.equal(updateLabel({ phase: 'restart', version: '0.12.0' }), 'Restarting…');
});

test('its tooltip says whether it updates in place or opens the release page, and why', () => {
  setLanguage('en-US');
  assert.equal(updateHint(undefined, '0.12.0'), '');
  assert.match(updateHint({ inPlace: true, kind: 'mac' }, '0.12.0'), /^Downloads 0\.12\.0, installs it in place/);
  assert.match(updateHint({ inPlace: false, reason: 'linuxPackage' }, '0.12.0'), /\.deb or \.pacman/);
});

test('a failure says why, in the app\'s language', () => {
  setLanguage('en-US');
  assert.equal(failureMessage({ ok: false, code: 'busy', detail: '2' }), '2 jobs are running, which restarting would cut short. Update once they’re done.');
  assert.equal(failureMessage({ ok: false, code: 'upToDate', detail: '0.12.0' }), 'This is already 0.12.0, the latest release.');
  setLanguage('pt-BR');
  assert.equal(failureMessage({ ok: false, code: 'download', detail: 'ECONNRESET' }), 'O download falhou: ECONNRESET');
  setLanguage('en-US');
});
