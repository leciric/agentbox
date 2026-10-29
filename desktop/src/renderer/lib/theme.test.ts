// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { contrast, lookOf, modeOf, palette } from './theme.ts';

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

test('Dracula is dark, its own look, and worn whatever the host has', () => {
  const dracula = theme({ appearance: 'dracula', accent: '#bd93f9', background: '#282a36' });
  assert.equal(modeOf(dracula), 'dark');
  assert.equal(lookOf(dracula), 'dracula');
  assert.equal(lookOf(theme({ appearance: 'follow', mode: 'light' })), 'light');
  assert.equal(palette(dracula)!['--ab-brand-500'], '#bd93f9');
});

test('a theme\'s text shades read 4.5:1 on its window, and the button text reads on its accent', () => {
  // Tokyo Night's pale blue: white on it is 2.6:1, and its 300 mixed towards
  // white the old way read 1.9:1 under the light variant.
  for (const mode of ['dark', 'light']) {
    const p = palette(theme({ appearance: 'follow', mode, background: mode === 'light' ? '#e1e2e7' : '#1a1b26', accent: '#7aa2f7' }))!;
    const ink = hexRgb(p['--ab-ink']);
    for (const shade of ['100', '200', '300', '400']) assert.ok(contrast(hexRgb(p[`--ab-brand-${shade}`]), ink) >= 4.5, `${mode} ${shade}`);
    assert.equal(p['--ab-on-brand'], '#09090b');
  }
  assert.equal(palette(theme({ appearance: 'follow', accent: '#5b21b6' }))!['--ab-on-brand'], '#ffffff');
});

function hexRgb(h: string): [number, number, number] {
  const n = Number.parseInt(h.slice(1), 16);
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
}
