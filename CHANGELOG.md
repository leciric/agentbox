# Changelog

All notable, user-facing changes to AgentBox are documented here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing older than 0.3.0 is listed.

## [0.11.0](https://github.com/leciric/agentbox/compare/v0.10.0...v0.11.0) (2026-10-01)


### Added

* AgentBox always runs in its VM on Linux, and installations from before are prompted to move ([#146](https://github.com/leciric/agentbox/issues/146)) ([370c47d](https://github.com/leciric/agentbox/commit/370c47d987ad592b91d2dde0affc58070be61587))
* AgentBox never fills your disk: it keeps a floor of free space, refuses new agents and pauses the busiest writers at it ([#148](https://github.com/leciric/agentbox/issues/148)) ([41865b4](https://github.com/leciric/agentbox/commit/41865b424b2abddc42cf14b4c495e3140f1ed2f2))
* an agent queue with a running limit per project, and a lead that rechecks its agents ([#138](https://github.com/leciric/agentbox/issues/138)) ([fee4611](https://github.com/leciric/agentbox/commit/fee4611485d7149a6c95550cdcec69f1f2a60218))
* chat from your phone from anywhere, through a Cloudflare Tunnel ([#137](https://github.com/leciric/agentbox/issues/137)) ([3e8d8ab](https://github.com/leciric/agentbox/commit/3e8d8abb534b417a53090713cc694de8bd76c95c))
* nightly builds, and a Stable/Nightly update channel in Settings ([#136](https://github.com/leciric/agentbox/issues/136)) ([23a6c50](https://github.com/leciric/agentbox/commit/23a6c50a90306ff2577ac40320bbfac96e8a5458))
* report a problem from the app or agentbox report, and opt-in error reports ([#149](https://github.com/leciric/agentbox/issues/149)) ([c0465df](https://github.com/leciric/agentbox/commit/c0465df510dbab04d0c1915d60e1c21a052171a5))


### Fixed

* AgentBox no longer hangs when Incus dies as the Linux VM starts ([#139](https://github.com/leciric/agentbox/issues/139)) ([e062346](https://github.com/leciric/agentbox/commit/e06234699a9c3dc4470e6aeebe1884eaf289b635))
* logging in to Claude Code works when AgentBox runs in its Linux VM, and a pasted code is submitted ([#142](https://github.com/leciric/agentbox/issues/142)) ([a50d027](https://github.com/leciric/agentbox/commit/a50d02722c8aeb72bcf7e097ca4d4497a70a3db4))
* messages in a chat no longer disappear when a turn ends or the chat is read again ([#144](https://github.com/leciric/agentbox/issues/144)) ([7034fa1](https://github.com/leciric/agentbox/commit/7034fa1f43f90ef9afe362037b72939076b6559d))
* recordings in Media play again on Linux, with GPU voice transcription (Vulkan) now an opt-in setting ([#150](https://github.com/leciric/agentbox/issues/150)) ([d42a8e2](https://github.com/leciric/agentbox/commit/d42a8e278d8a9353eec090205f780b258402a2bf))
* recordings, and other large media, are no longer saved corrupted and play as black ([#134](https://github.com/leciric/agentbox/issues/134)) ([53c6ddd](https://github.com/leciric/agentbox/commit/53c6ddd6e5a5ed6c53c8195acdc181331baa8cec))
* restarting AgentBox no longer leaves several daemons running and the app without its socket ([#141](https://github.com/leciric/agentbox/issues/141)) ([b73f49a](https://github.com/leciric/agentbox/commit/b73f49aa4674b059baca33dbaf9120f9538ee07f))
* the desktop app no longer shows "write EPIPE" error dialogs when the VM stops or starts ([#132](https://github.com/leciric/agentbox/issues/132)) ([0b5aee8](https://github.com/leciric/agentbox/commit/0b5aee805fd3047b680f45b06a2c2089f7499f32))
* the Tasks tab is a list only you manage, with a Backlog and a Queue ([#145](https://github.com/leciric/agentbox/issues/145)) ([b8080a2](https://github.com/leciric/agentbox/commit/b8080a2b79d9e90f059aa334b20a5a3047b53fd3))
* the top bar's resource controls show only the VM's memory ([#147](https://github.com/leciric/agentbox/issues/147)) ([ac2e2a2](https://github.com/leciric/agentbox/commit/ac2e2a2963b68c98532207447858cabef5d012ff))

## [0.10.0](https://github.com/leciric/agentbox/compare/v0.9.1...v0.10.0) (2026-09-29)


### Added

* an experimental Mac VM that runs without Lima ([#126](https://github.com/leciric/agentbox/issues/126)) ([8bd7800](https://github.com/leciric/agentbox/commit/8bd7800fb479e217015fa09553624ee7bc2f9e7e))
* chat from your phone's browser on your local network, paired with a QR code ([#130](https://github.com/leciric/agentbox/issues/130)) ([46690b4](https://github.com/leciric/agentbox/commit/46690b4217c0d86945351b3b6d99c52038237551))
* the Mac VM gives memory back when krunkit is installed ([#125](https://github.com/leciric/agentbox/issues/125)) ([bce201a](https://github.com/leciric/agentbox/commit/bce201a1abf1ad22a932aac8df285a650bdba155))


### Fixed

* agents given a 1M context window really get it ([#128](https://github.com/leciric/agentbox/issues/128)) ([d4fc514](https://github.com/leciric/agentbox/commit/d4fc5147f8b9539403c5cc07cc285640eb0987bf))
* agents start reliably in the Linux VM, git over ssh works there, and the VM can't be broken from inside itself ([#129](https://github.com/leciric/agentbox/issues/129)) ([aa21ef1](https://github.com/leciric/agentbox/commit/aa21ef1bc7f063f9429ea3058f5bb715fb13b18d))
* merging a pull request no longer empties the list ([#127](https://github.com/leciric/agentbox/issues/127)) ([8d7eabd](https://github.com/leciric/agentbox/commit/8d7eabdc7d60dd8bf6c24fc29443fc1734546634))

## [0.9.1](https://github.com/leciric/agentbox/compare/v0.9.0...v0.9.1) (2026-09-29)


### Fixed

* the project chat can reach GitHub when AgentBox runs in a VM ([#122](https://github.com/leciric/agentbox/issues/122)) ([39c88d6](https://github.com/leciric/agentbox/commit/39c88d6b651fe5d9e2f3f7d83c18accecbdd7fde))
* the project chat has the GitHub CLI ([#124](https://github.com/leciric/agentbox/issues/124)) ([25dc944](https://github.com/leciric/agentbox/commit/25dc944343d1b5b9b6d7475a02f3ea4948b31622))

## [0.9.0](https://github.com/leciric/agentbox/compare/v0.8.0...v0.9.0) (2026-09-29)


### Added

* have the chat read the agents' replies aloud, with a local voice in English or Brazilian Portuguese ([#116](https://github.com/leciric/agentbox/issues/116)) ([8936a9e](https://github.com/leciric/agentbox/commit/8936a9e92981239736ed300437c492e55cd8d4fc))
* push-to-talk in every chat, transcribed on your own GPU by Whisper ([#118](https://github.com/leciric/agentbox/issues/118)) ([28e72c5](https://github.com/leciric/agentbox/commit/28e72c55fbc100276fac1f80bdaaa39e954cff98))
* run AgentBox on Linux inside one lightweight VM, with no host setup and no sudo ([#119](https://github.com/leciric/agentbox/issues/119)) ([b7664aa](https://github.com/leciric/agentbox/commit/b7664aa89a221943115fea9e623d4de285336d00))


### Fixed

* agents attach PR screenshots with gh instead of pushing them to a media branch ([#114](https://github.com/leciric/agentbox/issues/114)) ([3a1d5c5](https://github.com/leciric/agentbox/commit/3a1d5c57571cc785a4ab78e96b13d3fa72fb3a94))
* agents no longer show a card offering to raise their memory ([#113](https://github.com/leciric/agentbox/issues/113)) ([1b1204d](https://github.com/leciric/agentbox/commit/1b1204d95add9c549572527952e1b553d50bff7f))
* the agents' rail no longer warns that they're short of memory at the shared budget ([#115](https://github.com/leciric/agentbox/issues/115)) ([f774873](https://github.com/leciric/agentbox/commit/f774873a96d44c83f1d0a25c171f553b7041ca71))
* the app shows loading placeholders instead of empty lists while it loads ([#117](https://github.com/leciric/agentbox/issues/117)) ([9b61bde](https://github.com/leciric/agentbox/commit/9b61bded28e6da771688f604bf8f5fce54a62975))
* the shared agent budget protects your apps' memory instead of fencing agents in, no longer throttles the disk, and is off unless you turn it on ([#110](https://github.com/leciric/agentbox/issues/110)) ([f3a35ff](https://github.com/leciric/agentbox/commit/f3a35ffd9995444a1e963fbecc3c5e1b2f3dd66d))

## [0.8.0](https://github.com/leciric/agentbox/compare/v0.7.0...v0.8.0) (2026-09-28)


### Added

* a project base shows when the base image has moved on, and Refresh catches it up ([#104](https://github.com/leciric/agentbox/issues/104)) ([4b0737e](https://github.com/leciric/agentbox/commit/4b0737ec1d5319f17ac03a51de075fa39cc64662))
* a reorganised Settings page, with sections, search and plain explanations ([#102](https://github.com/leciric/agentbox/issues/102)) ([71bb7ee](https://github.com/leciric/agentbox/commit/71bb7ee2308c991926b1aa4660e9470ca0d25cbe))
* disk IO and a stall warning next to CPU and memory, on Home, the top bar and every agent ([#107](https://github.com/leciric/agentbox/issues/107)) ([cf143b1](https://github.com/leciric/agentbox/commit/cf143b14c11f5cad59845ffbb58abe9c78163ce4))
* the shared agent budget is on by default, so agents can't freeze your computer ([#109](https://github.com/leciric/agentbox/issues/109)) ([946d4ae](https://github.com/leciric/agentbox/commit/946d4ae647f3552d976f92dbc73fd707ce11865d))
* warn when an agent is short of memory and slowing the computer down, with a one-click raise ([#105](https://github.com/leciric/agentbox/issues/105)) ([730159a](https://github.com/leciric/agentbox/commit/730159a32e6f9970cae4b6cdbd653df2cba57e8f))


### Fixed

* agents in the shared budget give way to your apps on the disk, and you're warned when they run short of memory together ([#108](https://github.com/leciric/agentbox/issues/108)) ([b3ef825](https://github.com/leciric/agentbox/commit/b3ef8258f64225d0bc92fbb56518e17324b81e1f))
* creating an agent no longer fails on Incus's "Failed to retrieve PID" hiccup ([#103](https://github.com/leciric/agentbox/issues/103)) ([6d452e1](https://github.com/leciric/agentbox/commit/6d452e138e3a3601ade473c24f4bc0fbf95193e4))
* new agents start from the latest main, and the project's main stays up to date ([#100](https://github.com/leciric/agentbox/issues/100)) ([f2ab6ad](https://github.com/leciric/agentbox/commit/f2ab6adca0177cd552dec75b818122c78011aa9d))
* turning on GPU for agents no longer fails on the project's lead ([#98](https://github.com/leciric/agentbox/issues/98)) ([1b80f1a](https://github.com/leciric/agentbox/commit/1b80f1abe2766b83d8f55c0887414211f36f0cd3))
* windows in an agent's desktop fit the screen after it's resized ([#101](https://github.com/leciric/agentbox/issues/101)) ([cce99aa](https://github.com/leciric/agentbox/commit/cce99aa139d83c5d233f5d2f9961f9cde44608c2))

## [0.7.0](https://github.com/leciric/agentbox/compare/v0.6.0...v0.7.0) (2026-09-27)


### Added

* agents record every visible change to Media by default ([#91](https://github.com/leciric/agentbox/issues/91)) ([e3b3e25](https://github.com/leciric/agentbox/commit/e3b3e25464aa84a7fb49671683e4b5901cf39d03))
* create a new repository right from Add project ([#90](https://github.com/leciric/agentbox/issues/90)) ([c7057ba](https://github.com/leciric/agentbox/commit/c7057baab39f2a27cb035e63624c4fdfa8f0f997))
* GPU for agents, an installation setting that passes the host's GPU into every agent ([#66](https://github.com/leciric/agentbox/issues/66)) ([cf5d80d](https://github.com/leciric/agentbox/commit/cf5d80d530911c3213c892a079d2b59ce0fb4142))
* let a project's agents push and open their own pull requests, with their media in the PR ([#94](https://github.com/leciric/agentbox/issues/94)) ([f082cb3](https://github.com/leciric/agentbox/commit/f082cb3942889853a6503cd213a165c35a2d6e98))
* search the Media tab by name, type and a note's words ([#92](https://github.com/leciric/agentbox/issues/92)) ([88e165f](https://github.com/leciric/agentbox/commit/88e165f3ea8d4422db232882b344a0c406da0faf))
* show each agent's name in the agents list, and its disk usage in its info card ([#93](https://github.com/leciric/agentbox/issues/93)) ([bf587d5](https://github.com/leciric/agentbox/commit/bf587d5c168e3982101a180a081ac2f7d407f519))
* watch agents' pull requests and tell the agent when one conflicts, fails CI or gets changes requested ([#97](https://github.com/leciric/agentbox/issues/97)) ([f67c18c](https://github.com/leciric/agentbox/commit/f67c18c386c0695fabae31d9d88b9110e8097e6c))


### Fixed

* chats open on their latest messages and load older ones as you scroll up ([#95](https://github.com/leciric/agentbox/issues/95)) ([d188b8c](https://github.com/leciric/agentbox/commit/d188b8c9eb0ae58e4c8c364e5ce7c2dfeb9ff030))
* new agents and the lead run at the model and context window chosen in Settings ([#96](https://github.com/leciric/agentbox/issues/96)) ([7298e60](https://github.com/leciric/agentbox/commit/7298e606720ede42225b8115dfc1e64ec681cdab))
* release-please releases build and publish their files ([#87](https://github.com/leciric/agentbox/issues/87)) ([2db44a9](https://github.com/leciric/agentbox/commit/2db44a9573063c5e1e103aaeaf9cc5ddb67acad4))
* the project chat starts one agent per task instead of stopping at three ([#89](https://github.com/leciric/agentbox/issues/89)) ([af255d1](https://github.com/leciric/agentbox/commit/af255d144811d0667b3a1bc3e91c311b61bb415a))

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
