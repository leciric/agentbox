// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import {
  connectorName,
  connectorPresets,
  connectorRequest,
  connectorStatus,
  presetFor,
  RequestConnector,
  tokenLine,
  validConnectorName,
  validConnectorURL,
} from './connectors.ts';

const now = Date.parse('2026-09-29T12:00:00Z');

function connector(over: Partial<T.Connector> = {}): T.Connector {
  return {
    name: 'notion',
    scope: 'project',
    project: 'pawly',
    url: 'https://mcp.notion.com/mcp',
    auth: T.ConnectorOAuth,
    enabled: true,
    status: T.ConnectorConnected,
    updatedAt: '2026-09-29T11:00:00Z',
    agents: [],
    ...over,
  };
}

function question(over: Partial<T.Question> & Record<string, unknown> = {}): T.Question {
  return {
    id: 'q1',
    project: 'pawly',
    agent: 'agent-01',
    ref: 'pawly/agent-01',
    kind: RequestConnector,
    question: 'I need to read the spec in Notion.',
    status: 'pending',
    createdAt: '2026-09-29T11:00:00Z',
    ...over,
  };
}

test('every preset has a valid name and URL, and only Figma takes a token', () => {
  for (const p of connectorPresets) {
    assert.ok(validConnectorName(p.name), p.name);
    assert.ok(validConnectorURL(p.url), p.url);
    assert.equal(p.auth === T.ConnectorSecret, p.id === 'figma', p.id);
  }
  assert.equal(presetFor('figma')?.secret?.header, 'X-Figma-Token');
});

test('presetFor finds a preset by name or by URL, with or without a trailing slash', () => {
  assert.equal(presetFor('notion')?.label, 'Notion');
  assert.equal(presetFor('https://mcp.linear.app/mcp/')?.label, 'Linear');
  assert.equal(presetFor('acme'), undefined);
});

test('connectorName makes a name from the server host', () => {
  assert.equal(connectorName('https://mcp.acme.io/mcp'), 'acme');
  assert.equal(connectorName('https://api.example.com/v1/mcp'), 'example');
  assert.equal(connectorName('http://127.0.0.1:4000/mcp'), '');
  assert.equal(connectorName('http://localhost:4000/mcp'), '');
  assert.equal(connectorName('not a url'), '');
});

test('names are lowercase letters, digits, - and _', () => {
  assert.ok(validConnectorName('my-server_2'));
  assert.ok(!validConnectorName('Notion'));
  assert.ok(!validConnectorName('-x'));
  assert.ok(!validConnectorName(''));
});

test('a URL must be https, or http on this machine', () => {
  assert.ok(validConnectorURL('https://mcp.notion.com/mcp'));
  assert.ok(validConnectorURL('http://127.0.0.1:8080/mcp'));
  assert.ok(validConnectorURL('http://localhost/mcp'));
  assert.ok(!validConnectorURL('http://example.com/mcp'));
  assert.ok(!validConnectorURL('mcp.notion.com'));
});

test('connectorStatus reads each status', () => {
  assert.deepEqual(connectorStatus(connector()), { label: 'connected', tone: 'success' });
  assert.equal(connectorStatus(connector({ status: T.ConnectorConnecting })).tone, 'warning');
  assert.equal(connectorStatus(connector({ status: T.ConnectorError })).tone, 'danger');
  assert.equal(connectorStatus(connector({ status: T.ConnectorDisconnected })).label, 'not connected');
});

test('tokenLine says when an OAuth token runs out', () => {
  assert.equal(tokenLine(connector({ expiresAt: '2026-09-29T12:45:00Z' }), now), 'token expires in 45m · renewed automatically');
  assert.equal(tokenLine(connector({ expiresAt: '2026-09-29T11:59:00Z' }), now), 'token expired · renewed on next use');
  assert.equal(tokenLine(connector(), now), "token doesn't expire");
  assert.equal(tokenLine(connector({ status: T.ConnectorDisconnected, expiresAt: '2026-09-29T12:45:00Z' }), now), undefined);
});

test('tokenLine names the secret a token connector sends, and nothing for a public one', () => {
  assert.equal(tokenLine(connector({ auth: T.ConnectorSecret, secret: 'FIGMA_TOKEN' }), now), 'token from $FIGMA_TOKEN');
  assert.equal(tokenLine(connector({ auth: T.ConnectorNone }), now), undefined);
});

test('connectorRequest reads a request for a preset by name alone', () => {
  const req = connectorRequest(question({ connector: 'Notion' }));
  assert.equal(req?.name, 'notion');
  assert.equal(req?.url, 'https://mcp.notion.com/mcp');
  assert.equal(req?.preset?.id, 'notion');
});

test('connectorRequest takes the name from secretName too, and a custom URL', () => {
  const req = connectorRequest(question({ secretName: 'acme', url: 'https://mcp.acme.io/mcp' }));
  assert.deepEqual(req, { name: 'acme', url: 'https://mcp.acme.io/mcp', preset: undefined });
});

test('connectorRequest makes a name from the URL when none is given', () => {
  assert.equal(connectorRequest(question({ url: 'https://mcp.figma.com/mcp' }))?.preset?.id, 'figma');
  assert.equal(connectorRequest(question({ url: 'https://mcp.acme.io/mcp' }))?.name, 'acme');
});

test('connectorRequest is nothing for another kind, or with nothing to go on', () => {
  assert.equal(connectorRequest(question({ kind: T.CredentialSecret, secretName: 'X' })), undefined);
  assert.equal(connectorRequest(question()), undefined);
});
