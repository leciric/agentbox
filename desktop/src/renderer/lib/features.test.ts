// Run with `npm test` (node's own test runner, which strips the types).
//
// The lead answers "can AgentBox…?" from the feature catalogue in
// internal/features/features.json. Every tab, panel view and Settings section
// of the app is named in some entry's places, so one added without an entry
// fails here: add it there, with what it does and where it is.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { enUS } from '../../shared/i18n/en-US.ts';
import { leadTabs } from './rightPanel.ts';
import { agentSections, agentTabs, projectSections, projectTabs } from './tabs.ts';

const catalogue: { name: string; places?: string[] }[] = JSON.parse(
  readFileSync(new URL('../../../../internal/features/features.json', import.meta.url), 'utf8'),
);
const named = new Set(catalogue.flatMap((f) => f.places ?? []));

// The app's places, as the catalogue names them. Panel views and the app's
// Settings sections come from their titles' keys, which each has.
function keysLike(pattern: RegExp): string[] {
  return Object.keys(enUS).flatMap((k) => pattern.exec(k)?.[1] ?? []);
}
const places = [
  ...projectTabs.map((t) => `project:${t}`),
  ...projectSections.map((s) => `project:settings/${s}`),
  ...agentTabs.map((t) => `agent:${t}`),
  ...agentSections.map((s) => `agent:settings/${s}`),
  ...leadTabs.map((t) => `lead:${t}`),
  ...keysLike(/^panel\.view\.([a-z]+)$/).map((v) => `panel:${v}`),
  ...keysLike(/^settings\.section\.([a-z]+)\.title$/).map((s) => `settings/${s}`),
];

test('every tab, panel view and Settings section is in the feature catalogue', () => {
  assert.ok(places.length > 40, `found only ${places.length} places: the keys or the lists moved`);
  assert.deepEqual(places.filter((p) => !named.has(p)), []);
});

test('the catalogue names no place the app lacks', () => {
  assert.deepEqual([...named].filter((p) => !places.includes(p)), []);
});
