// Run with `npm test`.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mergeViews } from './presence.ts';

test('views merge per project and agent, asking for resources if any does', () => {
  assert.deepEqual(
    mergeViews(
      [
        { project: 'b' },
        { project: 'a', agent: 'a/x' },
        { project: 'a', agent: 'a/x', resources: true },
        { project: 'b' },
        { project: '' },
      ],
      false,
    ),
    [{ project: 'a', agent: 'a/x', resources: true }, { project: 'b' }],
  );
});

test('a hidden window shows nothing', () => {
  assert.deepEqual(mergeViews([{ project: 'a', agent: 'a/x', resources: true }], true), []);
});
