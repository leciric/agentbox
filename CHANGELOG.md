# Changelog

## Unreleased

### Added

- The top bar's "Host memory" and "Host CPU" meters now open a popover breaking the total down by agent, largest first, with a link to each agent and a Stop button. Memory shows each agent's RAM and swap from its cgroup, its limit, and says when a paused agent is still holding memory; when swap is zram, it also shows what that swap really costs in RAM. CPU shows each agent's current use and its configured vs. effective core cap.
