import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseRoute, routeHash, type Route } from './route.ts';

test('every screen survives its fragment, a name with a space too', () => {
  const routes: Route[] = [
    { kind: 'list' },
    { kind: 'project', project: 'my app' },
    { kind: 'agent', ref: 'my app/agent-12' },
    { kind: 'pulls', project: 'agentbox' },
    { kind: 'pulls', project: 'agentbox', pr: 482 },
  ];
  for (const route of routes) assert.deepEqual(parseRoute(routeHash(route)), route.kind === 'pulls' && !route.pr ? { ...route, pr: undefined } : route);
});

test('a fragment that names no screen is the list', () => {
  for (const hash of ['', '#', '#/', '#/a/only-project', '#/x/y', '#/p/']) assert.deepEqual(parseRoute(hash), { kind: 'list' });
  assert.deepEqual(parseRoute('#/pr/agentbox/not-a-number'), { kind: 'pulls', project: 'agentbox', pr: undefined });
});

test('a fragment that would break the page, or split a name, is the list', () => {
  assert.deepEqual(parseRoute('#/a/%E0%A4%A/x'), { kind: 'list' });
  assert.deepEqual(parseRoute('#/a/agent%20box/a%2Fb'), { kind: 'list' });
});
