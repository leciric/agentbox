import type * as T from '../../shared/api';

// What Settings → Other apps (components/AppsSettings.tsx) knows about the
// routes apps reach AgentBox by, and the text each app is given, apart from
// how it's drawn.

export const routes = ['local', 'tunnel', 'hub'] as const;
export type Route = (typeof routes)[number];
export const clients = ['claude-code', 'claude-desktop', 'cursor', 'claude', 'chatgpt', 'poke', 'other'] as const;
export type Client = (typeof clients)[number];
// The apps each route is for: claude.ai and ChatGPT sign in with OAuth, which
// only the hub does; the others send a token, which any route takes.
export const clientsByRoute = {
  local: ['claude-code', 'claude-desktop', 'cursor', 'other'],
  tunnel: ['claude-code', 'claude-desktop', 'cursor', 'poke', 'other'],
  hub: ['claude', 'chatgpt', 'poke', 'other'],
} as const satisfies Record<Route, readonly Client[]>;
export type HubClient = (typeof clientsByRoute.hub)[number];
export type DirectClient = Exclude<Client, 'claude' | 'chatgpt'>;
export const accessLevels = ['read', 'chat', 'full'] as const;
export type Access = (typeof accessLevels)[number];

export const isClient = (c: string): c is Client => (clients as readonly string[]).includes(c);
export const asRoute = (r: string): Route => ((routes as readonly string[]).includes(r) ? (r as Route) : 'hub');
export const asAccess = (a: string): Access => ((accessLevels as readonly string[]).includes(a) ? (a as Access) : 'read');
export const forRoute = (route: Route, c: Client): boolean => (clientsByRoute[route] as readonly Client[]).includes(c);

// The text to paste into the app, for the apps that take one: Claude Code's
// command, and Cursor's and Claude Desktop's config. Claude Desktop reaches a
// server by URL only through its own connectors (OAuth, from Anthropic's
// side), so it runs mcp-remote, whose header goes in the environment: a space
// in an argument breaks it on Windows.
export function mcpSnippet(client: string, url: string, token: string): string | undefined {
  switch (client) {
    case 'claude-code':
      return `claude mcp add --transport http --scope user agentbox ${url} --header "Authorization: Bearer ${token}"`;
    case 'cursor':
      return JSON.stringify({ mcpServers: { agentbox: { url, headers: { Authorization: `Bearer ${token}` } } } }, null, 2);
    case 'claude-desktop':
      return JSON.stringify(
        { mcpServers: { agentbox: { command: 'npx', args: ['-y', 'mcp-remote', url, '--header', 'Authorization:${AGENTBOX_AUTH}'], env: { AGENTBOX_AUTH: `Bearer ${token}` } } } },
        null,
        2,
      );
  }
  return undefined;
}

// routeURL is the address an app is given for a route, while it has one.
export function routeURL(st: T.MCPPairings, route: Route): string | undefined {
  switch (route) {
    case 'local':
      return st.local.url;
    case 'tunnel':
      return st.tunnel.enabled && st.tunnel.state === 'running' ? st.tunnel.url : undefined;
    case 'hub':
      return st.available ? st.url : undefined;
  }
}

// canPair says whether an app can be paired for a route now.
export function canPair(st: T.MCPPairings, route: Route): boolean {
  switch (route) {
    case 'local':
      return true;
    case 'tunnel':
      return st.tunnel.enabled;
    case 'hub':
      return st.available;
  }
}
