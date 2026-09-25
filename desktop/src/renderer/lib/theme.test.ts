// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { modeOf, palette } from './theme.ts';

const theme = (over: Partial<T.Theme>): T.Theme => ({ available: true, appearance: 'follow', mode: 'dark', accent: '#336699', background: '#111111', ...over }) as T.Theme;

test('modeOf is dark with no theme to ask at all', () => {
  assert.equal(modeOf(undefined), 'dark');
});

test('modeOf honours an explicit pin over the host', () => {
  assert.equal(modeOf(theme({ appearance: 'light', mode: 'dark' })), 'light');
  assert.equal(modeOf(theme({ appearance: 'dark', mode: 'light' })), 'dark');
});

test('modeOf follows the host theme when set to follow', () => {
  assert.equal(modeOf(theme({ appearance: 'follow', mode: 'light' })), 'light');
  assert.equal(modeOf(theme({ appearance: 'follow', mode: 'dark' })), 'dark');
});

test('modeOf is dark for a theme with nothing usable to follow', () => {
  assert.equal(modeOf(theme({ appearance: 'follow', available: false })), 'dark');
});

test('palette is null unless the theme is being followed', () => {
  assert.equal(palette(undefined), null);
  assert.equal(palette(theme({ appearance: 'dark' })), null);
  assert.equal(palette(theme({ appearance: 'follow', available: false })), null);
});

test('palette is null for an accent that is not a hex colour', () => {
  assert.equal(palette(theme({ accent: 'cornflowerblue' })), null);
});

test('palette resolves the accent into its brand shades', () => {
  const p = palette(theme({}));
  assert.ok(p);
  assert.equal(p!['--ab-brand-500'], '#336699');
  assert.match(p!['--ab-wash-near'], /^rgb\(/);
});

test('palette derives --ab-ink from the background when there is one', () => {
  const p = palette(theme({ background: '#202020' }));
  assert.ok(p!['--ab-ink']);
  const noBg = palette(theme({ background: 'not-a-colour' }));
  assert.equal(noBg!['--ab-ink'], undefined);
});
