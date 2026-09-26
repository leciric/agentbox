# Changelog

All notable, user-facing changes to AgentBox are documented here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing older than 0.3.0 is listed.

## [0.6.0](https://github.com/leciric/agentbox/compare/v0.5.0...v0.6.0) (2026-09-26)


### Added

* a shared agent budget, one memory, swap and CPU pool for every agent ([#73](https://github.com/leciric/agentbox/issues/73)) ([8720f39](https://github.com/leciric/agentbox/commit/8720f390d8c634f8ecb3726a5447ad03f0cbbabe))
* count down the five-hour window in the top bar's Claude meter ([#74](https://github.com/leciric/agentbox/issues/74)) ([44a6243](https://github.com/leciric/agentbox/commit/44a62430f494ba7fd44fad33a6e0fe26ccd37a77))
* nesting, a real Incus daemon inside an agent ([#71](https://github.com/leciric/agentbox/issues/71)) ([6d02b15](https://github.com/leciric/agentbox/commit/6d02b15286365688d4514b0a484f097c28371f20))
* warn in Setup when agent storage isn't btrfs or zfs ([#75](https://github.com/leciric/agentbox/issues/75)) ([71bdee0](https://github.com/leciric/agentbox/commit/71bdee09e291a4639b10d993c9cddf37dcf7e8ce))


### Fixed

* lock agent worktrees against prune ([#78](https://github.com/leciric/agentbox/issues/78)) ([ba37362](https://github.com/leciric/agentbox/commit/ba37362cd9013ebbd723146df68a2633b36d798c))
* long agent names no longer overflow the info card and media cards ([#82](https://github.com/leciric/agentbox/issues/82)) ([11c3ebb](https://github.com/leciric/agentbox/commit/11c3ebb43660b6caa2bba23b2940a4ba2b46446a))
* make git use the agent's GitHub account over HTTPS ([#77](https://github.com/leciric/agentbox/issues/77)) ([ec09c95](https://github.com/leciric/agentbox/commit/ec09c95206eca780f7db9ec660bb905d2c349b87))
* never reuse an agent's name, and close what reuse broke ([#84](https://github.com/leciric/agentbox/issues/84)) ([397f336](https://github.com/leciric/agentbox/commit/397f336246929757ebd51a22d68e7fcc2f19d024))
* release-please's changelog-path can't traverse out of its package ([#81](https://github.com/leciric/agentbox/issues/81)) ([c2fa683](https://github.com/leciric/agentbox/commit/c2fa683560e5588cc80209190fbf5e46deaa049b))
* talk to Incus through its API instead of starting the incus command for every step ([#83](https://github.com/leciric/agentbox/issues/83)) ([92d49fd](https://github.com/leciric/agentbox/commit/92d49fd9e264f3f536887b6a0a22dfbfdf82e882))
* track every chat goroutine a turn spawns, not only the adapter's ([#80](https://github.com/leciric/agentbox/issues/80)) ([9c91ed1](https://github.com/leciric/agentbox/commit/9c91ed19d45822a4706a98a9137ed41a9cad2b06))

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
