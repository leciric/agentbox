// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { projectLabel } from './projectName.ts';

const projects = [
  { name: 'organic-web-app', displayName: 'Organic Web App' },
  { name: 'pawly', displayName: 'pawly' },
];

test('a project is shown by what it is called, exactly as typed', () => {
  assert.equal(projectLabel(projects[0]), 'Organic Web App');
  assert.equal(projectLabel('organic-web-app', projects), 'Organic Web App');
});

test('a slug no project has is shown as it is', () => {
  assert.equal(projectLabel('gone', projects), 'gone');
  assert.equal(projectLabel('organic-web-app', undefined), 'organic-web-app');
});

test('a daemon without display names shows the slug', () => {
  assert.equal(projectLabel({ name: 'old', displayName: '' }), 'old');
});
