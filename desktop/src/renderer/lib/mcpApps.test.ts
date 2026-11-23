import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { canPair, clientsByRoute, mcpSnippet, routeURL } from './mcpApps.ts';

const noHub: T.MCPPairings = {
  available: false,
  reason: 'no-hub',
  local: { port: 7782, url: 'http://127.0.0.1:7782/mcp', listening: false },
  tunnel: { enabled: false, mode: 'quick', state: 'off' },
  pairings: [],
};

test('with no hub, apps pair on this computer, and through the tunnel once it runs for them', () => {
  assert.equal(canPair(noHub, 'local'), true);
  assert.equal(canPair(noHub, 'tunnel'), false);
  assert.equal(canPair(noHub, 'hub'), false);
  assert.equal(routeURL(noHub, 'local'), 'http://127.0.0.1:7782/mcp');
  assert.equal(routeURL(noHub, 'tunnel'), undefined);
  assert.equal(routeURL(noHub, 'hub'), undefined);

  const starting: T.MCPPairings = { ...noHub, tunnel: { enabled: true, mode: 'quick', state: 'starting' } };
  assert.equal(canPair(starting, 'tunnel'), true);
  assert.equal(routeURL(starting, 'tunnel'), undefined);
  const running: T.MCPPairings = { ...noHub, tunnel: { enabled: true, mode: 'quick', state: 'running', url: 'https://quiet-river-1234.trycloudflare.com/mcp' } };
  assert.equal(routeURL(running, 'tunnel'), 'https://quiet-river-1234.trycloudflare.com/mcp');

  const hub: T.MCPPairings = { ...noHub, available: true, reason: undefined, hub: 'hub.example.com', url: 'https://hub.example.com/mcp/abc' };
  assert.equal(canPair(hub, 'hub'), true);
  assert.equal(routeURL(hub, 'hub'), 'https://hub.example.com/mcp/abc');
});

test('apps that sign in with OAuth are offered through the hub only', () => {
  for (const route of ['local', 'tunnel'] as const) {
    const offered: readonly string[] = clientsByRoute[route];
    assert.ok(!offered.includes('claude') && !offered.includes('chatgpt'), route);
  }
});

test('the text each app is given carries the URL and the token', () => {
  const url = 'http://127.0.0.1:7782/mcp';
  const token = 'abx_m_abc';
  assert.equal(
    mcpSnippet('claude-code', url, token),
    'claude mcp add --transport http --scope user agentbox http://127.0.0.1:7782/mcp --header "Authorization: Bearer abx_m_abc"',
  );
  assert.deepEqual(JSON.parse(mcpSnippet('cursor', url, token)!), { mcpServers: { agentbox: { url, headers: { Authorization: 'Bearer abx_m_abc' } } } });
  // mcp-remote takes the header from its environment: no space in an argument.
  const desktop = JSON.parse(mcpSnippet('claude-desktop', url, token)!).mcpServers.agentbox;
  assert.equal(desktop.command, 'npx');
  assert.deepEqual(desktop.args, ['-y', 'mcp-remote', url, '--header', 'Authorization:${AGENTBOX_AUTH}']);
  assert.deepEqual(desktop.env, { AGENTBOX_AUTH: 'Bearer abx_m_abc' });
  assert.ok(desktop.args.every((a: string) => !a.includes(' ')));
  assert.equal(mcpSnippet('poke', url, token), undefined);
  assert.equal(mcpSnippet('other', url, token), undefined);
});
