# Connectors: remote MCP servers for agents

[← back to the index](README.md)

A **connector** is a remote MCP server — Notion, Linear, Sentry, anything that speaks MCP's
streamable HTTP transport — that a project's agents, and its chat, get as tools. You sign in once,
in your own browser; AgentBox keeps the sign-in and adds it to every request an agent makes to the
server. **No token is ever written into an agent's machine**, and an agent never sees one.

## Adding one

In the app, open a project's **Settings** tab, then **Connectors**, and pick Notion, Linear, Sentry
or Figma, or give the address of any other server. Connecting opens your browser at the server's sign-in page; once
you approve AgentBox there, the browser comes back to `http://127.0.0.1:7777/connectors/callback`
and the connector shows as connected. That address is on your own machine, and it reaches AgentBox
wherever AgentBox runs, its VM included, the same way agents' previews do.

From the command line:

```bash
agentbox connector add pawly notion --url https://mcp.notion.com/mcp
agentbox connector connect pawly notion          # opens the browser, waits for the sign-in
agentbox connector list pawly
agentbox connector add pawly figma --url https://mcp.figma.com/mcp --secret FIGMA_TOKEN --header X-Figma-Token
agentbox connector disconnect pawly notion       # forgets the sign-in, keeps the connector
agentbox connector remove pawly/agent-01 linear
```

A connector's address must be `https`, on a server elsewhere: AgentBox refuses one on your own
machine, since agents' requests would go there.

## Who gets it

A connector belongs to a project, and every agent of it gets it, or to one agent. An agent's own
connector of the same name replaces its project's for that agent, the way an agent's own secret
does. The project's chat gets the project's connectors too.

When the chat creates an agent it can give it only some of the project's connectors, or none.

## How it signs in

- **Browser sign-in** (OAuth), the default, for servers that let AgentBox register itself with them:
  Notion, Linear and Sentry do. AgentBox renews the sign-in before it runs out; if the server stops
  accepting it, the connector says "needs connecting again".
- **A token you paste**, kept as one of the project's secrets (`agentbox secrets`) and sent in a
  header, for a server that won't let AgentBox register: Figma is one, and takes a personal access
  token in `X-Figma-Token`.
- **Nothing**, for a public server.

## When an agent asks for one

An agent that needs a service it has no tools for can ask you for it. The request shows as a card in
the chat and at the top of the project's Settings → Connectors, with the server's address: **check that address**,
since the agent chose it. A preset's name, like Notion, only shows when the address is that
service's own. Adding and signing in from the card gives the agent the connector; you can refuse it
instead.

An AI tool reads its MCP servers when its session starts, so a connector connected while an agent
works reaches its tools with its next session. Until then the agent can use it from its shell
(`agentbox connector tools notion`, `agentbox connector call notion …`).

## What it can't do yet

- A session already running keeps the MCP servers it started with: a connector added, removed or
  turned off reaches an agent's AI tool at its next session (Claude Code's `/mcp` reconnects).
- A server that refuses to let AgentBox register, but would take a client ID registered by hand,
  has no way to be given one.
- Figma's remote server wasn't checked with a real personal access token.
- If something else on your machine already listens on port 7777, the browser sign-in can't come
  back to AgentBox: use a token connector, or free the port.
