// What the Connectors tab and the request_connector card know about
// connectors that the daemon doesn't tell them: the catalog of servers the app
// offers, how a connector's state reads, and how a request reads.
import * as T from '../../shared/api.ts';
import { t } from '../../shared/i18n/index.ts';
import { timeUntil } from './utils.ts';

// A preset is a server the app knows: its URL, what its tools are called, and
// how it signs in. Checked on 29 September 2026. blurb, tokenLabel and why are
// getters, so they read in the language of the moment they're shown.
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
  { id: 'notion', label: 'Notion', name: 'notion', url: 'https://mcp.notion.com/mcp', auth: T.ConnectorOAuth, get blurb() { return t('project.connectors.blurb.notion'); } },
  { id: 'linear', label: 'Linear', name: 'linear', url: 'https://mcp.linear.app/mcp', auth: T.ConnectorOAuth, get blurb() { return t('project.connectors.blurb.linear'); } },
  { id: 'sentry', label: 'Sentry', name: 'sentry', url: 'https://mcp.sentry.dev/mcp', auth: T.ConnectorOAuth, get blurb() { return t('project.connectors.blurb.sentry'); } },
  // Hatch is ours: its /mcp answers 401 pointing at OAuth metadata with
  // dynamic client registration, so it signs in through the browser.
  { id: 'hatch', label: 'Hatch', name: 'hatch', url: 'https://usehatch.dev/mcp', auth: T.ConnectorOAuth, get blurb() { return t('project.connectors.blurb.hatch'); } },
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
      get tokenLabel() {
        return t('project.connectors.figma.tokenLabel');
      },
      tokenUrl: 'https://help.figma.com/hc/en-us/articles/8085703771159-Manage-personal-access-tokens',
      get why() {
        return t('project.connectors.figma.why');
      },
    },
    get blurb() {
      return t('project.connectors.blurb.figma');
    },
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
  // A second-level domain under a country's (example.co.uk, acme.com.br)
  // names the server one label further left.
  if (parts.length > 2 && /^(co|com|net|org|gov|ac|edu)$/.test(parts[parts.length - 2]) && parts[parts.length - 1].length === 2) parts.pop();
  const name = (parts.length > 1 ? parts[parts.length - 2] : (parts[0] ?? '')).toLowerCase();
  return name.replace(/[^a-z0-9_-]/g, '-').replace(/^-+/, '').slice(0, 40);
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
  host === 'localhost' || host.endsWith('.localhost') || /^127\.|^0\.0\.0\.0$|^169\.254\./.test(host) || ['[::1]', '[::]'].includes(host) || /^\[fe[89ab]/i.test(host);

// connectorURL is what the form sends for what was typed: trimmed, and with
// https:// before an address typed without a scheme, as most are when copied
// from a page that shows mcp.example.com/mcp.
export function connectorURL(typed: string): string {
  const url = typed.trim();
  // Only http and https count as a scheme here: mcp.example.com:8443 is a
  // host and a port.
  if (url === '' || /^https?:/i.test(url) || /^[a-z][a-z0-9+.-]*:\/\//i.test(url)) return url;
  return `https://${url}`;
}

// ── The add form's checks ─────────────────────────────────────────────────
//
// What the daemon would refuse, said next to the field it is about before
// the user presses Add, rather than a button that stays grey for no stated
// reason. Each is a sentence to show, or undefined when the field is fine;
// empty says the field is still to be filled in, rather than wrong.

// The names the daemon keeps for the MCP servers it gives every agent
// (connectors.Reserved), and how long a name may be.
const reservedConnectorNames = ['playwright', 'desktop', 'memory', 'agentbox'];
const maxConnectorName = 40;
// The secret names AgentBox writes into every agent itself (secrets.reserved).
const reservedSecretNames = ['CLAUDE_CODE_OAUTH_TOKEN', 'GH_TOKEN', 'GITHUB_TOKEN', 'COMPOSE_PROJECT_NAME'];
const maxSecretName = 64;
// The headers the relay sets itself (connectors.validHeader).
const relayHeaders = ['content-type', 'accept', 'mcp-session-id', 'mcp-protocol-version', 'host', 'content-length'];

export interface FieldProblem {
  message: string;
  // empty is a field not filled in yet: shown as a hint, not an error.
  empty?: boolean;
}

export function connectorNameProblem(name: string, taken: string[] = []): FieldProblem | undefined {
  if (name === '') return { message: t('project.connectors.problem.nameEmpty'), empty: true };
  if (name.length > maxConnectorName) return { message: t('project.connectors.problem.nameLong', { max: maxConnectorName }) };
  if (!validConnectorName(name)) return { message: t('project.connectors.problem.nameInvalid') };
  if (reservedConnectorNames.includes(name)) return { message: t('project.connectors.problem.nameReserved', { name }) };
  if (taken.includes(name)) return { message: t('project.connectors.nameTaken') };
  return undefined;
}

export function connectorURLProblem(typed: string): FieldProblem | undefined {
  const url = connectorURL(typed);
  if (url === '') return { message: t('project.connectors.problem.urlEmpty'), empty: true };
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return { message: t('project.connectors.problem.urlInvalid') };
  }
  // A browser reads https:/host as https://host; the daemon (Go's url.Parse)
  // finds no host in it.
  if (!/^https?:\/\/[^/]/i.test(url) || u.hostname === '') return { message: t('project.connectors.problem.urlInvalid') };
  if (u.username || u.password) return { message: t('project.connectors.problem.urlUser') };
  if (onThisMachine(u.hostname)) return { message: t('project.connectors.problem.urlLocal') };
  if (u.protocol !== 'https:') return { message: t('project.connectors.problem.urlHttps') };
  return undefined;
}

export function connectorSecretProblem(secret: string): FieldProblem | undefined {
  if (secret === '') return { message: t('project.connectors.problem.secretEmpty'), empty: true };
  if (secret.length > maxSecretName) return { message: t('project.connectors.problem.secretLong', { max: maxSecretName }) };
  if (!/^[A-Z_][A-Z0-9_]*$/.test(secret)) return { message: t('project.connectors.problem.secretInvalid') };
  if (reservedSecretNames.includes(secret)) return { message: t('project.connectors.problem.secretReserved', { name: secret }) };
  return undefined;
}

export function connectorHeaderProblem(header: string): FieldProblem | undefined {
  if (header === '') return { message: t('project.connectors.problem.headerEmpty'), empty: true };
  if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(header)) return { message: t('project.connectors.problem.headerInvalid') };
  if (relayHeaders.includes(header.toLowerCase())) return { message: t('project.connectors.problem.headerReserved', { header }) };
  return undefined;
}

export function connectorTokenProblem(token: string): FieldProblem | undefined {
  return token.trim() === '' ? { message: t('project.connectors.problem.tokenEmpty'), empty: true } : undefined;
}

// secretNameFor is the secret a token is kept as when the user hasn't named
// it: ACME_TOKEN for the connector acme.
export function secretNameFor(connector: string): string {
  const base = connector.toUpperCase().replace(/[^A-Z0-9_]/g, '_').replace(/^[0-9]/, '_$&');
  return base ? `${base}_TOKEN` : '';
}

export interface ConnectorForm {
  name: string;
  url: string;
  auth: string;
  secret: string;
  header: string;
  token: string;
  taken: string[];
}

export type ConnectorFormProblems = Partial<Record<'name' | 'url' | 'token' | 'secret' | 'header', FieldProblem>>;

// connectorFormProblems is every reason the form can't be sent yet, by field;
// the form is ready when there is none.
export function connectorFormProblems(f: ConnectorForm): ConnectorFormProblems {
  const problems: ConnectorFormProblems = {
    name: connectorNameProblem(f.name, f.taken),
    url: connectorURLProblem(f.url),
  };
  if (f.auth === T.ConnectorSecret) {
    problems.token = connectorTokenProblem(f.token);
    problems.secret = connectorSecretProblem(f.secret.trim());
    problems.header = connectorHeaderProblem(f.header.trim());
  }
  for (const k of Object.keys(problems) as (keyof ConnectorFormProblems)[]) if (!problems[k]) delete problems[k];
  return problems;
}

export type StatusTone = 'success' | 'warning' | 'danger' | 'default';

export function connectorStatus(c: T.Connector): { label: string; tone: StatusTone } {
  switch (c.status) {
    case T.ConnectorConnected:
      return { label: t('project.connectors.status.connected'), tone: 'success' };
    case T.ConnectorConnecting:
      return { label: t('project.connectors.status.connecting'), tone: 'warning' };
    case T.ConnectorError:
      return { label: t('project.connectors.status.error'), tone: 'danger' };
    default:
      return { label: t('project.connectors.status.disconnected'), tone: 'default' };
  }
}

// tokenLine says how long the connector's sign-in holds. The daemon renews an
// OAuth token before it runs out, so this is information, not a warning.
export function tokenLine(c: T.Connector, now = Date.now()): string | undefined {
  if (c.status !== T.ConnectorConnected) return undefined;
  if (c.auth === T.ConnectorSecret) return c.secret ? t('project.connectors.token.fromSecret', { secret: c.secret }) : undefined;
  if (c.auth !== T.ConnectorOAuth) return undefined;
  if (!c.expiresAt) return t('project.connectors.token.noExpiry');
  if (Date.parse(c.expiresAt) <= now) return t('project.connectors.token.expired');
  return t('project.connectors.token.expires', { when: timeUntil(c.expiresAt, now) });
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

// A project's say on an AgentBox-wide connector, as its three-way control
// shows it: off, on, or inherit, following AgentBox's switch.
export type ConnectorOverride = 'off' | 'inherit' | 'on';

export const isWide = (c: T.Connector) => c.scope === T.ConnectorWide;

export function overrideOf(c: T.Connector): ConnectorOverride {
  return c.override === 'on' || c.override === 'off' ? c.override : 'inherit';
}

// overrideRequest is what the daemon takes for it: '' to inherit.
export function overrideRequest(o: ConnectorOverride): '' | 'on' | 'off' {
  return o === 'inherit' ? '' : o;
}
