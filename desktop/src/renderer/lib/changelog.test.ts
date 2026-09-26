// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseChangelog, sectionsFrom } from './changelog.ts';

const sample = `# Changelog

## Unreleased

### Added

- Something not released yet.

## 0.4.0

### Added

- A thing 0.4.0 added.

## 0.3.0

### Fixed

- A bug 0.3.0 fixed.
`;

test('parseChangelog splits on version headings, dropping the title before the first one', () => {
  const sections = parseChangelog(sample);
  assert.deepEqual(
    sections.map((s) => s.version),
    ['Unreleased', '0.4.0', '0.3.0'],
  );
  assert.match(sections[1].body, /A thing 0.4.0 added/);
});

test('sectionsFrom skips Unreleased and starts at the running version', () => {
  const sections = parseChangelog(sample);
  assert.deepEqual(
    sectionsFrom(sections, '0.4.0').map((s) => s.version),
    ['0.4.0', '0.3.0'],
  );
  assert.deepEqual(
    sectionsFrom(sections, '0.3.0').map((s) => s.version),
    ['0.3.0'],
  );
});

test('sectionsFrom falls back to every released section when the running version is missing', () => {
  const sections = parseChangelog(sample);
  assert.deepEqual(
    sectionsFrom(sections, '9.9.9').map((s) => s.version),
    ['0.4.0', '0.3.0'],
  );
});
