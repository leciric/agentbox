// What the Connectors tab and the request_connector card know about
// connectors that the daemon doesn't tell them: the catalog of servers the app
// offers, how a connector's state reads, and how a request reads.
import * as T from '../../shared/api.ts';
import { timeUntil } from './utils.ts';

// A preset is a server the app knows: its URL, what its tools are called, and
// how it signs in. Checked on 29 September 2026.
export interface ConnectorPreset {
  id: string;
  label: string;
  // name is what the agents' tools are called by: mcp__notion__* in Claude Code.
  name: string;
  url: string;
  // oauth signs in through the browser. secret is for a server that won't
  // let AgentBox register for that: the user pastes a token instead, stored
  // as a secret and sent in a header.
  auth: typeof T.ConnectorOAuth | typeof T.ConnectorSecret;
  secret?: { name: string; header: string; scheme: string; tokenLabel: string; tokenUrl: string; why: string };
  blurb: string;
}

export const connectorPresets: ConnectorPreset[] = [
  { id: 'notion', label: 'Notion', name: 'notion', url: 'https://mcp.notion.com/mcp', auth: T.ConnectorOAuth, blurb: 'Pages, databases and comments' },
  { id: 'linear', label: 'Linear', name: 'linear', url: 'https://mcp.linear.app/mcp', auth: T.ConnectorOAuth, blurb: 'Issues, projects and cycles' },
  { id: 'sentry', label: 'Sentry', name: 'sentry', url: 'https://mcp.sentry.dev/mcp', auth: T.ConnectorOAuth, blurb: 'Errors, issues and releases' },
  {
    id: 'figma',
    label: 'Figma',
    name: 'figma',
    url: 'https://mcp.figma.com/mcp',
    // Figma's registration endpoint answers 403 to every client it hasn't
    // approved, so the browser sign-in can't work: a personal access token
    // in X-Figma-Token, the header Figma's own API reads it from.
    auth: T.ConnectorSecret,
    secret: {
      name: 'FIGMA_TOKEN',
      header: 'X-Figma-Token',
      scheme: '',
      tokenLabel: 'Personal access token',
      tokenUrl: 'https://help.figma.com/hc/en-us/articles/8085703771159-Manage-personal-access-tokens',
      why: "Figma only lets apps it has approved sign in, so it takes a personal access token instead. Make one in Figma's settings, under Security.",
    },
    blurb: 'Files, frames and components',
  },
];

export const presetFor = (nameOrUrl: string): ConnectorPreset | undefined =>
  connectorPresets.find((p) => p.name === nameOrUrl || p.url === nameOrUrl.replace(/\/+$/, ''));

// A connector's name is what its tools are known by; the daemon takes
// lowercase letters, digits, - and _.
export const validConnectorName = (name: string) => /^[a-z0-9][a-z0-9_-]*$/.test(name);

// connectorName makes a name from a server's address, for a custom URL:
// https://mcp.acme.io/mcp is "acme". An address on this machine has no name
// to give, and gets none.
export function connectorName(url: string): string {
  let host: string;
  try {
    host = new URL(url).hostname;
  } catch {
    return '';
  }
  if (host === 'localhost' || /^[\d.]+$|^\[/.test(host)) return '';
  const parts = host.split('.').filter((p) => !['www', 'mcp', 'api'].includes(p));
  const name = (parts.length > 1 ? parts[parts.length - 2] : (parts[0] ?? '')).toLowerCase();
  return name.replace(/[^a-z0-9_-]/g, '-').replace(/^-+/, '');
}

// validConnectorURL is what the daemon takes: https, to a server elsewhere.
export const validConnectorURL = (url: string) => {
  try {
    const u = new URL(url);
    return u.protocol === 'https:' && !onThisMachine(u.hostname);
  } catch {
    return false;
  }
};

const onThisMachine = (host: string) =>
  host === 'localhost' || host.endsWith('.localhost') || /^127\.|^0\.0\.0\.0$|^169\.254\./.test(host) || host === '[::1]';

export type StatusTone = 'success' | 'warning' | 'danger' | 'default';

export function connectorStatus(c: T.Connector): { label: string; tone: StatusTone } {
  switch (c.status) {
    case T.ConnectorConnected:
      return { label: 'connected', tone: 'success' };
    case T.ConnectorConnecting:
      return { label: 'waiting for the browser', tone: 'warning' };
    case T.ConnectorError:
      return { label: 'needs connecting again', tone: 'danger' };
    default:
      return { label: 'not connected', tone: 'default' };
  }
}

// tokenLine says how long the connector's sign-in holds. The daemon renews an
// OAuth token before it runs out, so this is information, not a warning.
export function tokenLine(c: T.Connector, now = Date.now()): string | undefined {
  if (c.status !== T.ConnectorConnected) return undefined;
  if (c.auth === T.ConnectorSecret) return c.secret ? `token from $${c.secret}` : undefined;
  if (c.auth !== T.ConnectorOAuth) return undefined;
  if (!c.expiresAt) return "token doesn't expire";
  if (Date.parse(c.expiresAt) <= now) return 'token expired · renewed on next use';
  return `token expires in ${timeUntil(c.expiresAt, now)} · renewed automatically`;
}

// ── request_connector ─────────────────────────────────────────────────────
//
// An agent asks for a connector the way it asks for a credential: a question
// of kind T.QuestionConnector, with the connector's name and, when it isn't
// one the app knows, the server's URL. It is answered on the credential route
// (T.AnswerCredentialRequest's connector, or refuse).

export const RequestConnector = T.QuestionConnector;

export interface ConnectorRequest {
  name: string;
  url: string;
  // host is the server's, which the card shows as prominently as the name:
  // an agent chooses both, and what is signed in to is the URL.
  host: string;
  // preset is the server the app knows, only when the request is for that
  // very server: a request named "notion" for another URL is not Notion.
  preset?: ConnectorPreset;
}

const sameServer = (a: string, b: string) => a.replace(/\/+$/, '') === b.replace(/\/+$/, '');

export function connectorRequest(q: T.Question): ConnectorRequest | undefined {
  if (q.kind !== RequestConnector) return undefined;
  const asked = (q.connector ?? '').trim().toLowerCase();
  const given = q.url?.trim() ?? '';
  const named = presetFor(asked);
  let preset: ConnectorPreset | undefined;
  if (given) preset = connectorPresets.find((p) => sameServer(p.url, given));
  else preset = named;
  const url = given || preset?.url || '';
  const name = asked || preset?.name || connectorName(url);
  if (!name) return undefined;
  return { name, url, host: hostOf(url), preset };
}

export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return '';
  }
}
