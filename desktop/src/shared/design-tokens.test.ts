import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
// @ts-expect-error a script, with no types of its own
import { generate } from '../../scripts/tokens.mjs';

// Both apps' tokens are generated from design-tokens.json. A value changed
// there without running the generator, or a generated file edited by hand,
// fails here.
test('tokens.css and tokens.rs are what design-tokens.json generates', () => {
  for (const [path, text] of Object.entries(generate() as Record<string, string>)) {
    assert.equal(readFileSync(path, 'utf8'), text, `${path} is stale: run node desktop/scripts/tokens.mjs`);
  }
});

// luminance and contrast are WCAG's.
function luminance(hex: string): number {
  const n = parseInt(hex.slice(1, 7), 16);
  const [r, g, b] = [n >> 16, (n >> 8) & 255, n & 255].map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

test('every text role reads 4.5:1 on the window, the chat and the composer', () => {
  const { color } = JSON.parse(readFileSync(new URL('./design-tokens.json', import.meta.url), 'utf8'));
  for (const mode of ['dark', 'light']) {
    const c = { ...color.dark, ...color[mode] };
    const text = ['title', 'primary', 'secondary', 'tertiary', 'muted', 'subtle', 'faint', 'brand-100', 'brand-200', 'brand-300', 'brand-400', 'emerald-300', 'amber-300', 'rose-300', 'sky-300'];
    for (const ground of ['ink', 'chat', 'composer']) {
      for (const role of text) {
        const ratio = contrast(c[role], c[ground]);
        assert.ok(ratio >= 4.5, `${mode}: ${role} on ${ground} is ${ratio.toFixed(2)}:1`);
      }
    }
  }
});
