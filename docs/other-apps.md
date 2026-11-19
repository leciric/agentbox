# Using AgentBox from another app

An assistant you already talk to — Claude, ChatGPT, Poke, or anything else that speaks remote MCP —
can see your projects and talk to their leads: "what are my agents doing?", "ask the shop's lead to
fix the checkout bug", "is that pull request green yet?". The app reaches your computer through your
[hub](hub.md), over the connection AgentBox already keeps open to it. Nothing is opened on your
computer or your network.

It's off until you pair an app, and each pairing says what that app may do, in which projects, and
can be revoked at any time.

## What you need

- A hub your computer is connected to ([A hub](hub.md)), updated to a version that has this.
- A hub address the app can reach. Claude and ChatGPT call from their own servers, so for them the
  hub must be on the internet, with https. An app running on your own machine (Claude Code, a
  desktop client) can use a hub on your network or your tailnet.

## Pairing an app

In the app: **Settings → Other apps → Pair an app…**

1. Choose the app.
2. Choose what it may do, and where:
   - **Read only**: see projects, agents, pull requests and the lead's chat. It can't change anything.
   - **Chat with the lead**: also send messages to a project's lead, as you do in its chat.
   - **Full lead tools**: also use the lead's own tools directly: create, direct and retire agents.

   and either all projects, or only the ones you tick.
3. Give the app what the last step shows: the **server URL**, and the **code** when the app signs
   in. The code works once, for 10 minutes; **New code** makes another.

From a terminal, on a machine with no screen:

```bash
agentbox remote app add --client claude --access chat --project shop
agentbox remote app list
agentbox remote app code <id>      # a new code, to sign the app in again
agentbox remote app revoke <id>
```

### Claude

**Settings → Connectors → Add custom connector**, name it AgentBox and paste the server URL. Choose
**Connect**: a page of your hub opens and asks for the code. It then shows what the pairing allows,
and **Allow** sends you back to Claude.

### ChatGPT

Turn on **Developer mode** (Settings → Apps & Connectors → Advanced settings), then **Create**: name
it AgentBox, paste the server URL, keep OAuth. The same page asks for the code.

### Poke, and apps that take a token

Poke, and most command-line clients, take a bearer token or an "API key" instead of signing in.
Choose **Poke** or **Another app** in the wizard (or `--token`): the last step shows a token as well.
It is shown once.

```bash
claude mcp add --transport http agentbox https://hub.example.com/mcp      # Claude Code signs in with the code
```

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
- **The hub sees what passes.** It relays the app's calls to your computer, as it relays the app and
  your phone. Use a hub you run or trust.
- **The token and the code are secrets.** Anyone with a pairing's token has its access until you
  revoke it; a code typed into the wrong page gives that page's app the access. Type a code only
  on a page you opened yourself from the app you're pairing.

## Revoking

**Settings → Other apps → Revoke**, or `agentbox remote app revoke <id>`. The app
loses access at once, even while the hub can't be reached. Disconnecting the computer from its hub
revokes every pairing made through it.
