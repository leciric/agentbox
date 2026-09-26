# Changelog

## Unreleased

### Added

- Changing a project's Claude Code or GitHub account now offers to move its
  existing agents that were still on the old account to the new one, both in
  the app and with `agentbox claude-account`/`github-account --move-agents`.
  Settings now says which projects don't reach the default account.

### Changed

- Picking a Claude Code or GitHub account for a stopped or paused agent now
  takes effect right away, instead of requiring the agent to be running.
