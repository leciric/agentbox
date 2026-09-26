# Changelog

All notable, user-facing changes to AgentBox are documented here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing older than 0.3.0 is listed.

## Unreleased

### Added

- **Nesting**, a per-project switch in the project's settings under "Testing AgentBox itself", off
  by default: its new agents get a real Incus daemon of their own, so agents working on AgentBox
  can test limits, the GPU, image builds and agent creation for real. It needs a base image built
  with `agentbox image build --incus`, a new optional component. (#71)

### Changed

### Fixed

## 0.5.0

### Added

- **Memory and CPU breakdowns.** The top bar's Host memory and Host CPU meters open a popover
  listing every agent, largest first, with a link and a Stop button. Memory shows each agent's RAM,
  swap and limit, says when a paused agent still holds its memory, and what zram swap really costs
  in RAM; CPU shows each agent's use and its configured and effective cores. (#70)
- **Auto-stop idle agents**, an optional switch in Settings → Every agent, off by default: stops a
  running or paused agent after an idle time (2h by default) with no chat turn, job, waiting
  question or credential request, terminal input or recording. The worktree and branch are kept,
  and the agent's Overview says "Stopped after 2h idle". (#68)
- **Agent info.** Hovering an agent row, or its context menu's new Info item, shows its model,
  effort, context window, average tokens per second, accounts, AI tool, branch, state, uptime,
  limits, tokens and cost, and pull request. (#69)
- **Tokens per second** in the Tokens tab: in the headline, per agent, per model and per turn.
  Turns from before this version have no duration and are left out. (#69)
- Changing a project's Claude Code or GitHub account asks whether to move its agents still on the
  old one too, in the app and with `--move-agents` on the CLI. Settings says which projects don't
  follow the default account. (#65)
- **What's new**: this changelog, in Settings → This app, and shown once after an update. (#64)

### Changed

- A stopped or paused agent's Claude Code or GitHub account can be changed; it takes effect when
  the agent next starts. (#65)

### Fixed

- An agent that asked for the same credential again right after asking could be left waiting on
  the first request. (#67)
- The daemon could go on checking Claude accounts in the background after it had stopped. (#67)

## 0.4.0

### Added

- **Never freeze my CPU**: a switch in Settings → Every agent that keeps a chosen number of cores
  (1 by default) free for the desktop, recomputing every running agent's CPU limit as agents are
  made, started, stopped or destroyed. ([#62](https://github.com/leciric/agentbox/pull/62))
- New agents get a memory limit — 8 GiB, or half the host's memory if that's less — and an agent
  with a limit can no longer push its pages into the host's swap.
  ([#55](https://github.com/leciric/agentbox/pull/55))
- **Initializing**: a new agent shows a distinct "Initializing" state while it's being made,
  instead of "Needs attention", which is now left for a create that failed or was interrupted.
  ([#60](https://github.com/leciric/agentbox/pull/60))

### Fixed

- Opus and Sonnet offer the 1M context window again, instead of being stuck at 200k after a
  session compacted with no compact window set.
  ([#61](https://github.com/leciric/agentbox/pull/61))
- The `.pacman` package installs on current Arch again: it no longer depends on `http-parser`,
  which Arch dropped. ([#59](https://github.com/leciric/agentbox/pull/59))

## 0.3.0

### Added

- Agent tools (Claude Code, Codex, OpenCode, the GitHub CLI, the ACP adapters and the rest) update
  in place in the existing base image instead of requiring a rebuild.
  ([#44](https://github.com/leciric/agentbox/pull/44))
- A right-click context menu on agent rows: open its chat or terminal, start, stop, pause, resume,
  retire, copy its branch name, open its pull request, or destroy it.
  ([#35](https://github.com/leciric/agentbox/pull/35))
- Clicking **Storage pool** in the top bar shows a breakdown of what AgentBox uses on disk.
  ([#36](https://github.com/leciric/agentbox/pull/36))
- Anonymous feature-usage stats, sent once a day with the update check; off wherever the update
  check is off. ([#51](https://github.com/leciric/agentbox/pull/51))
- A project's pull requests list shows who opened each one.
  ([#45](https://github.com/leciric/agentbox/pull/45))
- A [SECURITY.md](https://github.com/leciric/agentbox/blob/main/SECURITY.md) for reporting
  vulnerabilities privately. ([#28](https://github.com/leciric/agentbox/pull/28))
- New agents default to 2 CPU cores instead of every core but two.
  ([#41](https://github.com/leciric/agentbox/pull/41))

### Changed

- The top bar's usage meter follows the Claude account of the page you're on.
  ([#42](https://github.com/leciric/agentbox/pull/42))
- Settings and a project's Overview share one layout, with Settings' tabs grouped as Lead, New
  agents, Every agent and This app. ([#52](https://github.com/leciric/agentbox/pull/52))
- The right rail no longer shows agent messages or unread counts; a waiting agent shows "Asks you
  something". ([#43](https://github.com/leciric/agentbox/pull/43))
- The agents' desktop has a modern dock, and recordings made with `agentbox media record start
  --input desktop` show click ripples and key captions.
  ([#47](https://github.com/leciric/agentbox/pull/47))
- The release workflow builds Linux, Windows and Mac in parallel.
  ([#40](https://github.com/leciric/agentbox/pull/40))

### Fixed

- A new project's chat can have its model, effort, context window and mode chosen before its
  first message. ([#49](https://github.com/leciric/agentbox/pull/49))
- Opus offers a 1M context window again, instead of being stuck at 200k after a chat compacted
  once. ([#39](https://github.com/leciric/agentbox/pull/39))
- The desktop app builds on a Mac-hosted agent's worktree.
  ([#27](https://github.com/leciric/agentbox/pull/27))
