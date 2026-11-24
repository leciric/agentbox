# Using AgentBox from another app

An assistant you already talk to — Claude Code, Claude Desktop, Cursor, Claude, ChatGPT, Poke, or
anything else that speaks remote MCP — can see your projects and talk to their leads: "what are my
agents doing?", "ask the shop's lead to fix the checkout bug", "is that pull request green yet?".

It's off until you pair an app, and each pairing says what that app may do, in which projects, and
can be revoked at any time.

## How the app reaches AgentBox

Each pairing is made for one of three routes, and its token works on that route only.

- **On this computer.** An app on the same computer as AgentBox (Claude Code, Claude Desktop,
  Cursor) calls `http://127.0.0.1:7782/mcp`. The port is open only while an app is paired this way,
  and only on the computer's loopback address: nothing on your network can reach it. You need
  nothing else, and this is what pairing means when you have no hub.
- **Through the tunnel.** An app on another computer reaches AgentBox through the Cloudflare tunnel
  [phones use from anywhere](phone.md#from-anywhere), at its address followed by `/mcp`. Turn it on
  for apps in **Settings → Other apps** (or `agentbox remote app tunnel on`); it doesn't turn
  chatting from a phone on. A **quick tunnel**'s address changes every time AgentBox starts, and
  apps paired through it need the new address then. A **named tunnel** of your own, set up under
  **Settings → Phone**, keeps its address.
- **Through your hub.** The app calls your [hub](hub.md), which relays over the connection AgentBox
  already keeps open to it. This is the only route for apps that sign in with OAuth rather than
  take a token, Claude (claude.ai) and ChatGPT, which call from their own servers: for them the
  hub must be on the internet, with https, and updated to a version that has this.

On this computer and through the tunnel, the app sends its pairing's token with every call, as
`Authorization: Bearer <token>`. Only `/mcp` is reachable either way: the rest of AgentBox isn't,
and through the tunnel an address that keeps sending wrong tokens is turned away for a while.

## Pairing an app

In the app: **Settings → Other apps → Pair an app…**

1. Choose the route, then the app.
2. Choose what it may do, and where:
   - **Read only**: see projects, agents, pull requests and the lead's chat. It can't change anything.
   - **Chat with the lead**: also send messages to a project's lead, as you do in its chat.
   - **Full lead tools**: also use the lead's own tools directly: create, direct and retire agents.

   and either all projects, or only the ones you tick.
3. Give the app what the last step shows: the **server URL**, with the **token** on this computer
   or through the tunnel, or the **code** when the app signs in through the hub. For Claude Code,
   Claude Desktop and Cursor it also shows the command or the configuration to paste. A token is
   shown once; **New token** replaces it. A code works once, for 10 minutes; **New code** makes
   another.

The URL of each route, and whether it's ready, is at the top of **Settings → Other apps**, with a
copy button.

From a terminal, on a machine with no screen:

```bash
agentbox remote app add --route local --client claude-code --access chat --project shop
agentbox remote app tunnel on                                                    # let apps use the tunnel
agentbox remote app add --route tunnel --client cursor --access read
agentbox remote app add --route hub --client claude --access chat
agentbox remote app list
agentbox remote app code <id>                                                    # a new token, or a new code through the hub
agentbox remote app revoke <id>
```

### Claude Code, Cursor and Claude Desktop

On this computer or through the tunnel, the last step shows what to paste. For Claude Code:

```bash
claude mcp add --transport http --scope user agentbox http://127.0.0.1:7782/mcp --header "Authorization: Bearer <token>"
```

For Cursor, the `agentbox` entry goes under `mcpServers` in `~/.cursor/mcp.json`, with the URL and
the header. Claude Desktop adds a server by its URL only as a connector that
signs in, so its configuration (`claude_desktop_config.json`) starts [`mcp-remote`](https://www.npmjs.com/package/mcp-remote) with
`npx`, which needs Node.js.

### Claude

**Settings → Connectors → Add custom connector**, name it AgentBox and paste the server URL. Choose
**Connect**: a page of your hub opens and asks for the code. It then shows what the pairing allows,
and **Allow** sends you back to Claude.

### ChatGPT

Turn on **Developer mode** (Settings → Apps & Connectors → Advanced settings), then **Create**: name
it AgentBox, paste the server URL, keep OAuth. The same page asks for the code.

### Poke, and apps that take a token

Poke, and most command-line clients, take a bearer token or an "API key" instead of signing in.
Through the tunnel or on this computer they always get one. Through the hub, choose **Poke** or
**Another app** in the wizard (or `--token`): the last step shows a token as well. It is shown once.

## What the app can do

| Tool | Access | What it does |
|------|--------|--------------|
| `list_projects` | read | The projects shared with it, how many agents each has and what its lead is doing |
| `list_agents` | read | A project's agents: what each is doing, how it ended, its branch |
| `list_pull_requests` | read | A project's pull requests: state, checks, review, which agent opened it |
| `read_lead_chat` | read | The latest messages between you and the lead, waiting for the lead to finish if asked |
| `message_lead` | chat | Sends the lead a message and waits for its reply |
| `lead_tools`, `call_lead_tool` | full | Lists and calls the lead's own tools |

A message an app sends says so in the chat ("Sent from Claude, another app connected to AgentBox
over MCP"), so you and the lead both know where it came from.

## Before you pair one

- **Chat and Full let the app change things.** The lead can start agents, and agents change code and
  open pull requests. An AI assistant reads things you didn't write — web pages, emails, documents —
  and any of them can try to talk it into sending the lead instructions. Pair an assistant you
  trust, give it only the projects it needs, and use **Read only** when that's all you want.
- **A project list limits which leads the app can talk to and read.** A lead it talks to still has
  the lead's own reach, and its first message to another project's lead waits for your approval,
  as always.
- **The hub sees what passes**, through the hub. It relays the app's calls to your computer, as it
  relays the app and your phone. Use a hub you run or trust. Through the tunnel, Cloudflare carries
  them, over https.
- **The token and the code are secrets.** Anyone with a pairing's token has its access until you
  revoke it; a code typed into the wrong page gives that page's app the access. Type a code only
  on a page you opened yourself from the app you're pairing.

## Revoking

**Settings → Other apps → Revoke**, or `agentbox remote app revoke <id>`. The app
loses access at once, even while the hub can't be reached. Disconnecting the computer from its hub
revokes every pairing made through it. Revoking the last app paired on this computer closes its
port, and turning the tunnel off for apps (`agentbox remote app tunnel off`) turns every app away
from it.
