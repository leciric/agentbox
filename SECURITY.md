# Security

## Reporting a vulnerability

Please report vulnerabilities privately, not in a public issue: open the repository's
[Security tab](https://github.com/leciric/agentbox/security) and choose **Report a vulnerability**
(GitHub's private vulnerability reporting). Say what you found, how to reproduce it, and what it
lets someone do. You'll get an answer there, and the fix and its advisory are published once a
release carries it.

## What's in scope

- **The daemon**: `agentbox` and its HTTP API on the unix socket, the state it keeps, and anything
  that lets another local user or process drive it.
- **The agents' machines and their sandboxing**: an agent reaching the host, another agent's
  machine, worktree or network, or anything outside what it was given.
- **Tokens and credentials**: GitHub tokens, AI tools' logins and API keys, and anything else
  AgentBox stores, passes to agents or could leak through logs, media or the app.

Bugs in the AI tools themselves (Claude Code, Codex, OpenCode) belong to their own projects.

## Supported versions

Only the [latest release](https://github.com/leciric/agentbox/releases/latest) gets security fixes.
