// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as T from '../../shared/api.ts';
import {
  connectorFormProblems,
  connectorHeaderProblem,
  connectorName,
  connectorNameProblem,
  connectorPresets,
  connectorRequest,
  connectorSecretProblem,
  connectorStatus,
  connectorTokenProblem,
  connectorURL,
  connectorURLProblem,
  isWide,
  overrideOf,
  overrideRequest,
  presetFor,
  RequestConnector,
  secretNameFor,
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

test('a URL must be https, to a server elsewhere', () => {
  assert.ok(validConnectorURL('https://mcp.notion.com/mcp'));
  assert.ok(!validConnectorURL('http://127.0.0.1:8080/mcp'));
  assert.ok(!validConnectorURL('https://localhost/mcp'));
  assert.ok(!validConnectorURL('https://169.254.169.254/'));
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
  assert.equal(req?.host, 'mcp.notion.com');
  assert.equal(req?.preset?.id, 'notion');
});

test('connectorRequest takes a custom URL', () => {
  const req = connectorRequest(question({ connector: 'acme', url: 'https://mcp.acme.io/mcp' }));
  assert.deepEqual(req, { name: 'acme', url: 'https://mcp.acme.io/mcp', host: 'mcp.acme.io', preset: undefined });
});

test("connectorRequest doesn't dress another server up as a preset", () => {
  const req = connectorRequest(question({ connector: 'notion', url: 'https://notion.evil.example/mcp' }));
  assert.equal(req?.preset, undefined);
  assert.equal(req?.name, 'notion');
  assert.equal(req?.host, 'notion.evil.example');
  assert.equal(connectorRequest(question({ connector: 'figma', url: 'https://figma.evil.example/mcp' }))?.preset, undefined);
  assert.equal(connectorRequest(question({ connector: 'notion', url: 'https://mcp.notion.com/mcp/' }))?.preset?.id, 'notion');
});

test('connectorRequest makes a name from the URL when none is given', () => {
  assert.equal(connectorRequest(question({ url: 'https://mcp.figma.com/mcp' }))?.preset?.id, 'figma');
  assert.equal(connectorRequest(question({ url: 'https://mcp.acme.io/mcp' }))?.name, 'acme');
});

test('connectorRequest is nothing for another kind, or with nothing to go on', () => {
  assert.equal(connectorRequest(question({ kind: T.CredentialSecret, secretName: 'X' })), undefined);
  assert.equal(connectorRequest(question()), undefined);
});

test('Hatch is a preset that signs in through the browser', () => {
  const p = presetFor('https://hatch.linting.dev/mcp');
  assert.equal(p?.id, 'hatch');
  assert.equal(p?.auth, T.ConnectorOAuth);
});

test('an AgentBox-wide connector in a project reads its override three ways', () => {
  const wide = connector({ scope: T.ConnectorWide, project: '' });
  assert.ok(isWide(wide));
  assert.ok(!isWide(connector()));
  assert.equal(overrideOf(wide), 'inherit');
  assert.equal(overrideOf({ ...wide, override: 'off' }), 'off');
  assert.equal(overrideOf({ ...wide, override: 'on' }), 'on');
  assert.equal(overrideRequest('inherit'), '');
  assert.equal(overrideRequest('off'), 'off');
});

// The servers people add by hand, as their pages give the address.
const common = [
  'https://mcp.notion.com/mcp',
  'https://mcp.linear.app/mcp',
  'https://api.githubcopilot.com/mcp/',
  'https://mcp.example.com:8443/v1/mcp',
  'https://mcp.deepwiki.com/sse?x=1',
];

test('connectorName names the common servers, and a country-code domain by the one left of it', () => {
  assert.deepEqual(common.map(connectorName), ['notion', 'linear', 'githubcopilot', 'example', 'deepwiki']);
  assert.equal(connectorName('https://mcp.acme.co.uk/mcp'), 'acme');
  assert.equal(connectorName('https://api.loja.com.br/mcp'), 'loja');
  assert.equal(connectorName(`https://${'a'.repeat(60)}.com/mcp`).length, 40);
});

test('connectorURL puts https:// before an address typed without one', () => {
  assert.equal(connectorURL('  mcp.notion.com/mcp '), 'https://mcp.notion.com/mcp');
  assert.equal(connectorURL('https://mcp.notion.com/mcp'), 'https://mcp.notion.com/mcp');
  assert.equal(connectorURL('http://example.com/mcp'), 'http://example.com/mcp');
  assert.equal(connectorURL('mcp.example.com:8443/mcp'), 'https://mcp.example.com:8443/mcp');
  assert.equal(connectorURL(''), '');
});

test('connectorURLProblem takes the common servers and says what is wrong with the rest', () => {
  for (const u of common) assert.equal(connectorURLProblem(u), undefined, u);
  assert.equal(connectorURLProblem('mcp.linear.app/mcp'), undefined);
  assert.deepEqual(connectorURLProblem(''), { message: 'Paste the server’s address, like https://mcp.example.com/mcp.', empty: true });
  assert.match(connectorURLProblem('http://example.com/mcp')!.message, /^Use https/);
  assert.match(connectorURLProblem('https://localhost:3000/mcp')!.message, /on this machine/);
  assert.match(connectorURLProblem('https://127.0.0.1/mcp')!.message, /on this machine/);
  assert.match(connectorURLProblem('https://[::1]/mcp')!.message, /on this machine/);
  assert.match(connectorURLProblem('https://me:pw@example.com/mcp')!.message, /user name/);
  assert.match(connectorURLProblem('https://exa mple.com')!.message, /isn’t a web address/);
  assert.match(connectorURLProblem('ftp://example.com')!.message, /isn’t a web address/);
  assert.match(connectorURLProblem('https:/mcp.notion.com/mcp')!.message, /isn’t a web address/);
  assert.equal(connectorURLProblem('mcp.example.com:8443/mcp'), undefined);
  assert.equal(connectorURLProblem('ftp://example.com')!.empty, undefined);
});

test('connectorNameProblem says why a name is refused, as the daemon would', () => {
  assert.equal(connectorNameProblem('notion'), undefined);
  assert.equal(connectorNameProblem('')?.empty, true);
  assert.match(connectorNameProblem('Notion')!.message, /lowercase/);
  assert.match(connectorNameProblem('a'.repeat(41))!.message, /40/);
  assert.match(connectorNameProblem('memory')!.message, /AgentBox gives every agent/);
  assert.equal(connectorNameProblem('notion', ['notion'])!.message, 'There is one of that name already.');
});

test('the token fields say what they need', () => {
  assert.equal(connectorTokenProblem('ghp_x'), undefined);
  assert.equal(connectorTokenProblem(' ')?.empty, true);
  assert.equal(connectorSecretProblem('GITHUB_COPILOT_TOKEN'), undefined);
  assert.equal(connectorSecretProblem('')?.empty, true);
  assert.match(connectorSecretProblem('1TOKEN')!.message, /not starting with a digit/);
  assert.match(connectorSecretProblem('GH_TOKEN')!.message, /AgentBox sets GH_TOKEN/);
  assert.match(connectorSecretProblem('A'.repeat(65))!.message, /64/);
  assert.equal(connectorHeaderProblem('Authorization'), undefined);
  assert.equal(connectorHeaderProblem('X-Api-Key'), undefined);
  assert.equal(connectorHeaderProblem('')?.empty, true);
  assert.match(connectorHeaderProblem('X Api')!.message, /isn’t a header name/);
  assert.match(connectorHeaderProblem('Mcp-Session-Id')!.message, /AgentBox sets Mcp-Session-Id/);
});

test('secretNameFor names the token after the connector', () => {
  assert.equal(secretNameFor('githubcopilot'), 'GITHUBCOPILOT_TOKEN');
  assert.equal(secretNameFor('my-server'), 'MY_SERVER_TOKEN');
  assert.equal(secretNameFor('1password'), '_1PASSWORD_TOKEN');
  assert.equal(secretNameFor(''), '');
  assert.equal(connectorSecretProblem(secretNameFor('my-server')), undefined);
});

// The form as ConnectorForm fills it in: the name from the URL, the secret
// from the name, until the user types either.
function form(over: Partial<Parameters<typeof connectorFormProblems>[0]> = {}) {
  const url = over.url ?? '';
  const name = over.name ?? connectorName(connectorURL(url));
  return connectorFormProblems({ auth: T.ConnectorOAuth, secret: secretNameFor(name), header: 'Authorization', token: '', taken: [], url, name, ...over });
}

test('a browser sign-in form is ready with just a URL, for every common server', () => {
  for (const url of common) assert.deepEqual(form({ url }), {}, url);
  assert.deepEqual(form({ url: 'mcp.notion.com/mcp' }), {});
  for (const p of connectorPresets.filter((p) => p.auth === T.ConnectorOAuth)) assert.deepEqual(form({ url: p.url, name: p.name }), {}, p.id);
});

test('a public server needs nothing more than a browser sign-in does', () => {
  assert.deepEqual(form({ url: 'https://mcp.deepwiki.com/mcp', auth: T.ConnectorNone }), {});
});

test('a token form is ready once the token is pasted, the secret named after the connector', () => {
  const url = 'https://api.githubcopilot.com/mcp/';
  assert.deepEqual(Object.keys(form({ url, auth: T.ConnectorSecret })), ['token']);
  assert.deepEqual(form({ url, auth: T.ConnectorSecret, token: 'ghp_x' }), {});
  const figma = connectorPresets.find((p) => p.id === 'figma')!;
  assert.deepEqual(form({ url: figma.url, name: figma.name, auth: T.ConnectorSecret, secret: figma.secret!.name, header: figma.secret!.header, token: 'figd_x' }), {});
});

test('an empty form says what each field still needs, and nothing is ready', () => {
  const problems = form({ auth: T.ConnectorSecret });
  assert.deepEqual(Object.keys(problems).sort(), ['name', 'secret', 'token', 'url']);
  assert.ok(Object.values(problems).every((p) => p?.empty));
  assert.deepEqual(Object.keys(form({ header: '', auth: T.ConnectorSecret, url: common[0], token: 'x' })), ['header']);
});

test('an address with no name to give asks for one', () => {
  const problems = form({ url: 'https://203.0.113.7/mcp' });
  assert.deepEqual(Object.keys(problems), ['name']);
  assert.deepEqual(form({ url: 'https://203.0.113.7/mcp', name: 'edge' }), {});
});

test('a name already taken is said under the name', () => {
  assert.equal(form({ url: common[0], taken: ['notion'] }).name?.message, 'There is one of that name already.');
});
