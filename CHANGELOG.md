# Changelog

All notable, user-facing changes to AgentBox are documented here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing older than 0.3.0 is listed.

## Unreleased

### Added

- **Auto-stop idle agents**: an optional switch in Settings → Every agent, off by default, that
  stops a running or paused agent once it has gone an idle time (2h by default) with nothing
  happening on it — no chat turn, no job, no waiting question or credential request, no terminal
  input and no recording. Stopping keeps its worktree and branch, like stopping it by hand, and the
  agent view shows why: "Stopped after 2h idle".

### Changed

### Fixed
