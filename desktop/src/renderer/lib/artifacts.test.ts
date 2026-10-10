// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { QueryClient } from '@tanstack/react-query';
import * as T from '../../shared/api.ts';
import { artifactPagePath, artifactsFor, artifactsKey, byState, expiryOf, isPageCall, onChatItem } from './artifacts.ts';

const at = (iso: string) => new Date(iso).getTime();
const artifact = (a: Partial<T.Artifact>): T.Artifact => ({
  id: 'x', title: 'X', url: 'https://hatch.linting.dev/p/x', agent: 'pawly/lead', item: 'i', agents: ['pawly/lead'], version: 1,
  createdAt: '2026-10-09T12:00:00Z', updatedAt: '2026-10-09T12:00:00Z', ...a,
});

test('the project chat shows every artifact, an agent its own, and nothing without Hatch', () => {
  const list: T.Artifacts = {
    connector: 'hatch',
    artifacts: [artifact({ id: 'a' }), artifact({ id: 'b', agent: 'pawly/agent-02', agents: ['pawly/agent-02', 'pawly/lead'] }), artifact({ id: 'c', agent: 'pawly/agent-03', agents: ['pawly/agent-03'] })],
  };
  assert.deepEqual(artifactsFor(list, 'pawly/lead').map((a) => a.id), ['a', 'b', 'c']);
  assert.deepEqual(artifactsFor(list, 'pawly').map((a) => a.id), ['a', 'b', 'c']);
  assert.deepEqual(artifactsFor(list, 'pawly/agent-02').map((a) => a.id), ['b']);
  assert.deepEqual(artifactsFor(list, 'pawly/agent-09'), []);
  assert.deepEqual(artifactsFor({ ...list, connector: '' }, 'pawly/lead'), []);
  assert.deepEqual(artifactsFor(undefined, 'pawly/lead'), []);
});

test('isPageCall knows publish_page and update_page whatever the AI tool calls them', () => {
  assert.ok(isPageCall({ name: 'mcp__hatch__publish_page', title: '' }));
  assert.ok(isPageCall({ title: 'hatch.update_page' }));
  assert.ok(isPageCall({ name: 'Hatch_Publish_Page', title: 'x' }));
  assert.ok(!isPageCall({ name: 'mcp__hatch__get_page', title: 'Get page' }));
  assert.ok(!isPageCall({ name: 'mcp__hatch__republish_page', title: '' }));
  assert.ok(!isPageCall(undefined));
});

test('expiryOf', () => {
  const now = at('2026-10-09T12:00:00Z');
  assert.equal(expiryOf({ expired: true }, now), 'expired');
  assert.equal(expiryOf({ permanent: true }, now), 'permanent');
  assert.equal(expiryOf({}, now), 'unknown');
  assert.equal(expiryOf({ expiresAt: '2026-10-09T11:59:00Z' }, now), 'expired');
  assert.equal(expiryOf({ expiresAt: '2026-10-09T12:30:00Z' }, now), 'soon');
  assert.equal(expiryOf({ expiresAt: '2026-10-10T12:00:00Z' }, now), 'later');
});

test('byState puts the expired ones last, keeping the order', () => {
  const now = at('2026-10-09T12:00:00Z');
  const list = [artifact({ id: 'a', expired: true }), artifact({ id: 'b' }), artifact({ id: 'c', expiresAt: '2026-10-09T11:00:00Z' }), artifact({ id: 'd', permanent: true })];
  assert.deepEqual(byState(list, now).map((a) => a.id), ['b', 'd', 'a', 'c']);
});

test('a finished publish refetches its project\'s artifacts, nothing else does', () => {
  const client = new QueryClient();
  let fetched = 0;
  client.getQueryCache().subscribe((e) => {
    if (e.type === 'updated' && e.action.type === 'invalidate') fetched++;
  });
  client.setQueryData(artifactsKey('pawly'), { connector: 'hatch', artifacts: [] });
  const item = (tool: Partial<T.ChatTool>): T.ChatItem => ({ id: 'i', turn: 't', kind: 'tool', createdAt: '', updatedAt: '', tool: { callId: 'c', title: '', kind: 'other', status: 'completed', ...tool } });
  onChatItem(client, 'pawly/agent-02', item({ name: 'mcp__hatch__get_page' }));
  onChatItem(client, 'pawly/agent-02', item({ name: 'mcp__hatch__publish_page', status: 'in_progress' }));
  assert.equal(fetched, 0);
  onChatItem(client, 'pawly/agent-02', item({ name: 'mcp__hatch__publish_page' }));
  assert.equal(fetched, 1);
  assert.equal(client.getQueryState(artifactsKey('pawly'))?.isInvalidated, true);
});

test('artifactPagePath', () => {
  assert.equal(artifactPagePath('my app', 'EUv_1'), '/v1/projects/my%20app/artifacts/EUv_1/page');
});
