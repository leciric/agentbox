// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import { arrangeItems, customizationCSS, isCustomized, ownFonts, sidebarFoot, sidebarTop } from './customization.ts';

const custom = (over: Partial<T.Customization> = {}): T.Customization => ({
  version: 1,
  theme: { dark: {}, light: {} },
  fonts: {},
  sidebar: {},
  ...over,
});

// The declarations of one selector's block, by property.
function blockOf(css: string, selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {\n`);
  if (start < 0) return {};
  const body = css.slice(start + selector.length + 3, css.indexOf('\n}', start));
  return Object.fromEntries(
    body
      .split('\n')
      .map((line) => /^\s*(--[\w-]+): (.*) !important;$/.exec(line))
      .filter((m): m is RegExpExecArray => m !== null)
      .map((m) => [m[1], m[2]]),
  );
}
const dark = ':root:not([data-appearance="light"])';
const light = ':root[data-appearance="light"], [data-appearance-scope="light"]';

test('AgentBox’s own look is no stylesheet at all', () => {
  assert.equal(customizationCSS(undefined), '');
  assert.equal(customizationCSS(custom()), '');
  assert.equal(isCustomized(custom()), false);
});

test('an accent is shaded for each mode and wins over a followed theme’s', () => {
  const css = customizationCSS(custom({ theme: { accent: '#22c55e', dark: {}, light: {} } }));
  for (const selector of [dark, light]) {
    const props = blockOf(css, selector);
    assert.equal(props['--ab-brand-500'], '#22c55e', selector);
    assert.ok(props['--ab-brand-300'], selector);
    assert.equal(props['--ab-ink'], undefined, 'the accent alone leaves the window’s colour be');
  }
  // !important: theme.ts sets the followed theme's accent inline.
  assert.match(css, /--ab-brand-500: #22c55e !important;/);
  // Shades are readable on each window, so they differ between modes.
  assert.notEqual(blockOf(css, dark)['--ab-brand-200'], blockOf(css, light)['--ab-brand-200']);
});

test('a colour role paints every property it stands for, in its own mode', () => {
  const css = customizationCSS(custom({ theme: { dark: { background: '#0b1020', text: '#e6e9ff', line: '#ffffff' }, light: { mutedText: '#52525b' } } }));
  const d = blockOf(css, dark);
  assert.equal(d['--ab-ink'], '#0b1020');
  assert.equal(d['--ab-chat'], '#0b1020');
  for (const role of ['--ab-title', '--ab-primary', '--ab-secondary', '--ab-tertiary']) assert.equal(d[role], '#e6e9ff', role);
  assert.equal(d['--ab-line'], 'color-mix(in srgb, #ffffff 7%, transparent)');
  assert.equal(d['--ab-line-heavy'], 'color-mix(in srgb, #ffffff 25%, transparent)');
  assert.equal(d['--ab-muted'], undefined, 'a light colour stays out of the dark window');
  const l = blockOf(css, light);
  assert.equal(l['--ab-muted'], '#52525b');
  assert.equal(l['--ab-ink'], undefined);
});

test('fonts go in front of AgentBox’s own, and nothing ends a declaration', () => {
  const css = customizationCSS(custom({ fonts: { ui: '"IBM Plex Sans", sans-serif', mono: 'Iosevka; color: red {}' } }));
  const root = blockOf(css, ':root');
  assert.equal(root['--font-sans'], `"IBM Plex Sans", sans-serif, ${ownFonts.ui}`);
  assert.equal(root['--font-mono'], `Iosevka color: red , ${ownFonts.mono}`);
});

test('AgentBox’s own font stacks are the stylesheet’s', () => {
  const styles = readFileSync(new URL('../styles.css', import.meta.url), 'utf8');
  assert.ok(styles.includes(`--font-sans: ${ownFonts.ui};`), 'styles.css --font-sans changed: copy it to ownFonts');
  assert.ok(styles.includes(`--font-mono: ${ownFonts.mono};`), 'styles.css --font-mono changed: copy it to ownFonts');
});

test('a text size scales every text size, and density and corners their units', () => {
  const root = blockOf(customizationCSS(custom({ fonts: { size: 16 }, density: 'compact', theme: { radius: 'sharp', dark: {}, light: {} } })), ':root');
  assert.equal(root['--text-body'], '1rem');
  assert.equal(root['--text-micro'], '0.7857rem');
  assert.equal(root['--text-sm'], '1rem');
  assert.equal(root['--spacing'], '0.2rem');
  assert.equal(root['--radius-lg'], '0rem');
  assert.equal(blockOf(customizationCSS(custom({ density: 'spacious', theme: { radius: 'round', dark: {}, light: {} } })), ':root')['--radius-sm'], '0.4375rem');
  // AgentBox's own size and density set nothing.
  assert.equal(customizationCSS(custom({ fonts: { size: 14 }, density: 'comfortable' })), '');
});

test('custom CSS and the sidebar alone are a customization too', () => {
  assert.equal(isCustomized(custom({ css: '.x { color: red }' })), true);
  assert.equal(isCustomized(custom({ css: '  ' })), false);
  assert.equal(isCustomized(custom({ sidebar: { hidden: ['jobs'] } })), true);
});

test('arrangeItems puts the named items first, the rest after, and leaves the hidden out', () => {
  const own = ['settings', 'work', 'media', 'jobs', 'running', 'update'];
  assert.deepEqual(arrangeItems(own, undefined, undefined), own);
  assert.deepEqual(arrangeItems(own, ['media', 'settings'], undefined), ['media', 'settings', 'work', 'jobs', 'running', 'update']);
  assert.deepEqual(arrangeItems(own, ['media', 'home', 'media'], ['jobs', 'update']), ['media', 'settings', 'work', 'running']);
});

test('the sidebar’s items are the daemon’s', () => {
  assert.deepEqual(sidebarTop, ['new-task', 'home', 'office', 'filters']);
  assert.deepEqual(sidebarFoot, ['settings', 'work', 'media', 'jobs', 'running', 'update']);
});
