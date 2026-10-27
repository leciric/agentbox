# Changelog

All notable, user-facing changes to AgentBox are documented here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing older than 0.3.0 is listed.

## [0.16.0](https://github.com/leciric/agentbox/compare/v0.15.0...v0.16.0) (2026-10-26)


### Added

* a tray icon (the menu bar on a Mac) counts the agents working and what waits for you, with a menu to open each and quick actions ([#523](https://github.com/leciric/agentbox/issues/523)) ([c7a06fa](https://github.com/leciric/agentbox/commit/c7a06faeefc9cb312dfc2b59845a21dcc08c21d9))
* a Work page lists everything agents did, live and past, in place of Today and Past agents ([#505](https://github.com/leciric/agentbox/issues/505)) ([bfbee2c](https://github.com/leciric/agentbox/commit/bfbee2c2135a3f1209b8590d99e3e24f3af8286b))
* agents can stack work on another agent's unmerged branch, and AgentBox retargets and rebases it when the parent merges ([#516](https://github.com/leciric/agentbox/issues/516)) ([89450fb](https://github.com/leciric/agentbox/commit/89450fb2d7587ee0a90cbdbbec476a3c0392ea81))
* agents whose machine or processes are frozen show a Frozen label with why, and the VM memory tip shows where used memory goes ([#494](https://github.com/leciric/agentbox/issues/494)) ([f12f0df](https://github.com/leciric/agentbox/commit/f12f0df373aeb3e7956ef4c2df9d9c0708cd2a94))
* an agent's browser on a Mac renders WebGL on the Mac's GPU ([#464](https://github.com/leciric/agentbox/issues/464)) ([c3d8b55](https://github.com/leciric/agentbox/commit/c3d8b55326f7b29dbb359b61547a9803a515f744))
* anonymous error events say why they failed, and usage events carry the UTC offset ([#486](https://github.com/leciric/agentbox/issues/486)) ([ee37f2e](https://github.com/leciric/agentbox/commit/ee37f2ec9d19b8d2568e49bf23492b7569d192f9))
* anonymous usage stats include a daily health event for AgentBox's VM: memory, swap, pressure episodes, freezes and daemon error classes ([#501](https://github.com/leciric/agentbox/issues/501)) ([5d5fb12](https://github.com/leciric/agentbox/commit/5d5fb129c4aec8bf6f3757f178f09b97fcb59df4))
* Claude Code chats show Claude's thinking, live while it thinks and folded after ([#467](https://github.com/leciric/agentbox/issues/467)) ([066cc7c](https://github.com/leciric/agentbox/commit/066cc7c772bf2c1826e767bc240f564f4eb287d2))
* Claude Code is kept up to date by default ([#506](https://github.com/leciric/agentbox/issues/506)) ([9f068a9](https://github.com/leciric/agentbox/commit/9f068a9e60b4f31c5c65fedaa84c2fb441b05bae))
* event triggers for schedules: start an agent or wake the chat when CI goes red, a review is requested, an issue is labelled, or an outside PR opens ([#524](https://github.com/leciric/agentbox/issues/524)) ([7bfb316](https://github.com/leciric/agentbox/commit/7bfb3163d3eeac519c10c158eeb4814b69e9e8df))
* hand a GitHub project's issues to agents, from its Issues view, the lead, or by the agentbox label ([#520](https://github.com/leciric/agentbox/issues/520)) ([c5b89cc](https://github.com/leciric/agentbox/commit/c5b89ccd5b89acf548704ce9b7f4b35d2c542c6a))
* idle agents can give their memory back: frozen and swapped out while idle, stopped once finished (experimental, off by default) ([#481](https://github.com/leciric/agentbox/issues/481)) ([d4ce7ce](https://github.com/leciric/agentbox/commit/d4ce7ceebf8d80d1997875984b9eb92a20b7e8aa))
* move a running agent to another account of its AI tool without losing its conversation ([#491](https://github.com/leciric/agentbox/issues/491)) ([da47004](https://github.com/leciric/agentbox/commit/da4700467e946b071921aa0527cfaa6010d2d6d7))
* on a Mac, Setup installs krunkit with Homebrew for a VM with a GPU, and Settings can remake a vz VM with it ([#457](https://github.com/leciric/agentbox/issues/457)) ([b487da4](https://github.com/leciric/agentbox/commit/b487da423884bd78fb05f34e5ffa3fcbeaa62cef))
* opt-in "Built with AgentBox" footer on agents' pull requests, offered once after the first merge ([#514](https://github.com/leciric/agentbox/issues/514)) ([a85407f](https://github.com/leciric/agentbox/commit/a85407f66a7dacd1d6ecb737c48f2c2b077d7b43))
* optionally stop agents from running the whole test suite, and tell them which tests to run instead ([#479](https://github.com/leciric/agentbox/issues/479)) ([06e4ffa](https://github.com/leciric/agentbox/commit/06e4ffa5439f33af7b44e82a94ddb77b35c2806e))
* pause whole agents when memory runs short, and start new ones on real free memory (experimental) ([#482](https://github.com/leciric/agentbox/issues/482)) ([a03f493](https://github.com/leciric/agentbox/commit/a03f49322c4407ae764bb897a015046387d09632))
* Plan is a toggle beside the permission mode, so you can plan on bypass permissions and the approved plan runs on it ([#478](https://github.com/leciric/agentbox/issues/478)) ([34e4a21](https://github.com/leciric/agentbox/commit/34e4a217d5edd1fdaddaffde918ea90b6f84073f))
* push notifications on the paired phone, with the kinds and quiet hours it chooses ([#525](https://github.com/leciric/agentbox/issues/525)) ([19c750a](https://github.com/leciric/agentbox/commit/19c750a44286cefd31c48c085511fb0e1e160eac))
* report window, GPU and daemon crashes, and keep error reports made while the daemon is away ([#488](https://github.com/leciric/agentbox/issues/488)) ([9ce8027](https://github.com/leciric/agentbox/commit/9ce80278cfdc431051f041d223487d139f66716d))
* share what an agent did as one HTML page anyone can open ([#521](https://github.com/leciric/agentbox/issues/521)) ([5bb70dc](https://github.com/leciric/agentbox/commit/5bb70dc25bcda4e24ae27182ae0127a3ca66312b))
* stopped agents' machines can be removed after N days when nothing would be lost, and stopped agents show with their disk size ([#484](https://github.com/leciric/agentbox/issues/484)) ([1e94e06](https://github.com/leciric/agentbox/commit/1e94e068546816c6ffe026da6fd54c6ecb6446be))
* switch chats from any screen on the phone, with taps and layout that fit a phone ([#469](https://github.com/leciric/agentbox/issues/469)) ([2ece0b4](https://github.com/leciric/agentbox/commit/2ece0b400eb39afeecaefea2986c99704442b18c))
* the lead can fulfil agents' requests for secrets you mark as managed by it ([#507](https://github.com/leciric/agentbox/issues/507)) ([6a03391](https://github.com/leciric/agentbox/commit/6a0339127f673ce5a33ad71acb85e45ba535503f))
* the lead's panel has Agents, Files, Evidence and Schedules, with Today and Settings in its header ([#487](https://github.com/leciric/agentbox/issues/487)) ([52e0c10](https://github.com/leciric/agentbox/commit/52e0c1015396bf7e7a0b475b81de2e5bdb85169b))
* the Mac stays awake while agents are working ([#468](https://github.com/leciric/agentbox/issues/468)) ([f78a10e](https://github.com/leciric/agentbox/commit/f78a10ea0af3ccc42eb20d3be43d22fa4ac00067))
* the right panel's agent list shows each agent's number ([#497](https://github.com/leciric/agentbox/issues/497)) ([f89360e](https://github.com/leciric/agentbox/commit/f89360ec0c6464182dae605b4e2f442906fc4aa5))
* the shared package caches default to 10 GiB, wait while agents build, drop replaced browsers, and show their size in Diagnostics ([#483](https://github.com/leciric/agentbox/issues/483)) ([3292b2a](https://github.com/leciric/agentbox/commit/3292b2a7a6da30dfa0ae228fde68d8a6b60fc996))
* Your week with AgentBox, a weekly recap with share cards to post ([#517](https://github.com/leciric/agentbox/issues/517)) ([ed7f6d4](https://github.com/leciric/agentbox/commit/ed7f6d454b57c6826f5b4ce4e856acf4b0430ce5))


### Fixed

* a machine out of file descriptors no longer brings back reviewed agents or gets agents with work auto-removed ([#463](https://github.com/leciric/agentbox/issues/463)) ([72349d1](https://github.com/leciric/agentbox/commit/72349d15aca6aa389355ae9a1d60cebf81b7bc58))
* a Playwright call that never returns fails back to the agent instead of hanging its turn ([#509](https://github.com/leciric/agentbox/issues/509)) ([1f442ef](https://github.com/leciric/agentbox/commit/1f442ef6386fd4dd9d0e5239da9dc104ea0191e8))
* a project with no icon of its own shows its first letter, not its GitHub owner's avatar ([#513](https://github.com/leciric/agentbox/issues/513)) ([86fc093](https://github.com/leciric/agentbox/commit/86fc093070bed1e43732ea62834f15857490c2e0))
* agentbox wsl init no longer fails on newer WSL that rejects the empty probe archive ([#496](https://github.com/leciric/agentbox/issues/496)) ([38ae407](https://github.com/leciric/agentbox/commit/38ae407a1a7f72de824cd359c77014531e23c198))
* AgentBox's VM no longer holds memory on your computer for agents that are idle, or page cache it doesn't need ([#508](https://github.com/leciric/agentbox/issues/508)) ([0512162](https://github.com/leciric/agentbox/commit/05121622abba80014a2ab955ca50d79c0694ab27))
* AgentBox's VM no longer keeps its disks cached twice in your computer's memory, gives back cold cache while agents work, and shows what it really holds ([#495](https://github.com/leciric/agentbox/issues/495)) ([49e7423](https://github.com/leciric/agentbox/commit/49e742303797ec0bfb0de38d1be1e6a8bc95b2d5))
* agents no longer hit "Too many open files in system" in the Linux VM's shared home ([#462](https://github.com/leciric/agentbox/issues/462)) ([2965cf4](https://github.com/leciric/agentbox/commit/2965cf484b5791363f85158e984b65d68c78af51))
* agents no longer wait on AgentBox re-reading their whole worktree, and the app stops redrawing out of focus ([#480](https://github.com/leciric/agentbox/issues/480)) ([29bde9e](https://github.com/leciric/agentbox/commit/29bde9ebfc4a67ea1567c7fc45b2facd6b7f4284))
* an agent's change counts and pull request no longer include what it took in from main ([#503](https://github.com/leciric/agentbox/issues/503)) ([233a8af](https://github.com/leciric/agentbox/commit/233a8af5681d9d68df44191a8f5b2a0a4e20df7e))
* build with Go 1.27.3, fixing a crypto/x509 vulnerability ([#465](https://github.com/leciric/agentbox/issues/465)) ([16aa1e2](https://github.com/leciric/agentbox/commit/16aa1e29c7c168eecb118279ac2855622549ba42))
* destroying an agent stops what it runs at once and deletes its worktree in one pass ([#466](https://github.com/leciric/agentbox/issues/466)) ([bad85b7](https://github.com/leciric/agentbox/commit/bad85b7be6bb2340d2dc185b5a2fbec777624cc0))
* idle project chats stop their Claude Code after 15 minutes and resume the same conversation on the next message ([#502](https://github.com/leciric/agentbox/issues/502)) ([48b702c](https://github.com/leciric/agentbox/commit/48b702ccc296ac4a112c447683ed17ca59be975e))
* new agents install npm and pnpm packages several times faster, and start already installed when the lockfile is unchanged ([#470](https://github.com/leciric/agentbox/issues/470)) ([26bb4d4](https://github.com/leciric/agentbox/commit/26bb4d48db16456dc1a927d934f64fd131997a8c))
* older Claude models no longer fail with adaptive thinking, and node_modules snapshots keep only untouched installs ([#472](https://github.com/leciric/agentbox/issues/472)) ([1269bd3](https://github.com/leciric/agentbox/commit/1269bd3e19caf3d44e3b2adaf4f3f9a1b7f71eec))
* on a Mac without krunkit, Settings and VM setup say agents get no GPU and how to get one ([#455](https://github.com/leciric/agentbox/issues/455)) ([0d54d83](https://github.com/leciric/agentbox/commit/0d54d83877e1d56ddbe8e7fb7a82f4664ca35ccc))
* on a Mac, agents' GPU driver updates again, and Settings says when updating it fails ([#490](https://github.com/leciric/agentbox/issues/490)) ([bc37382](https://github.com/leciric/agentbox/commit/bc37382f858bc72dd7692777a7db228dff2375f7))
* on a Mac, any Chromium an agent starts renders WebGL on the GPU, and WebGL timer queries no longer crash it ([#477](https://github.com/leciric/agentbox/issues/477)) ([4b0b9e0](https://github.com/leciric/agentbox/commit/4b0b9e0d255b0956637484cf5adda5214b1c1b76))
* on Windows, agents' desktops and browsers no longer reset the GPU driver ([#456](https://github.com/leciric/agentbox/issues/456)) ([09c0a69](https://github.com/leciric/agentbox/commit/09c0a695e345c0509cf03237d2613fc20d5bf83d))
* opening a pull request in the app is faster, and merging one no longer waits for its agent to be freed ([#504](https://github.com/leciric/agentbox/issues/504)) ([8d6d35d](https://github.com/leciric/agentbox/commit/8d6d35dcd789ddd0ea1f6ed359f7db1b3597c0b5))
* pausing whole agents freezes one machine at a time, the one eating memory first, and says when swap is full ([#493](https://github.com/leciric/agentbox/issues/493)) ([d278741](https://github.com/leciric/agentbox/commit/d2787412339bcb24f1d4e3b28d6bc0e1e8a2b1ba))
* private projects' main stays up to date in the VM ([#499](https://github.com/leciric/agentbox/issues/499)) ([e067d85](https://github.com/leciric/agentbox/commit/e067d858621be9934506f6dbe928ea6049950ecb))
* pull requests from forks are no longer shown as the parent of stacks ([#529](https://github.com/leciric/agentbox/issues/529)) ([f42beae](https://github.com/leciric/agentbox/commit/f42beae3e4e8d992a988f08bc5ddfc4a96c39722))
* the agents panel stays beside the chat in windows down to 900px wide ([#459](https://github.com/leciric/agentbox/issues/459)) ([75f4083](https://github.com/leciric/agentbox/commit/75f4083a0ba0168c2969fd97fdf3de760f675dda))
* the chat fills its column, with the scrollbar at its edge ([#460](https://github.com/leciric/agentbox/issues/460)) ([e2263f3](https://github.com/leciric/agentbox/commit/e2263f34d9f28d82e10131cab375f17d6f400f8f))
* the daemon frees idle chats and finished jobs' logs, caps its heap at 256 MiB, and can be profiled ([#500](https://github.com/leciric/agentbox/issues/500)) ([248f081](https://github.com/leciric/agentbox/commit/248f081e5f0d2e98928a060362d74c38be0c16eb))
* the first message to a new project's chat no longer fails with "lead: already exists" ([#527](https://github.com/leciric/agentbox/issues/527)) ([a85bfd1](https://github.com/leciric/agentbox/commit/a85bfd18fc140036599b534d607a2ca412ae802b))
* the lead panel's agent list no longer widens the window at 1440px ([#498](https://github.com/leciric/agentbox/issues/498)) ([9f09421](https://github.com/leciric/agentbox/commit/9f09421a93c86a8f9c8fa94759c6f909612aeebd))
* the Media tab no longer crashes on partial data, and mailto: links open ([#485](https://github.com/leciric/agentbox/issues/485)) ([ad41d52](https://github.com/leciric/agentbox/commit/ad41d52df9be34d57a0198e4c24bb105cae804ae))
* the phone page no longer opens blank after AgentBox updates while installing it ([#492](https://github.com/leciric/agentbox/issues/492)) ([5c8c0d6](https://github.com/leciric/agentbox/commit/5c8c0d69d8ea77241801e4601e48d376ac15b993))
* the right panel's tabs name only the open one ([#474](https://github.com/leciric/agentbox/issues/474)) ([84c5e13](https://github.com/leciric/agentbox/commit/84c5e1347a1789e123a8dc9723522ab9836d4437))
* the share card preview in Your week no longer shows as a broken image ([#528](https://github.com/leciric/agentbox/issues/528)) ([a49f86d](https://github.com/leciric/agentbox/commit/a49f86dbd1b79ad47f102b3e11a85a4a54bdddb0))
* the Week page opens faster, and draws its share card only when it's scrolled into view ([#531](https://github.com/leciric/agentbox/issues/531)) ([51d93c1](https://github.com/leciric/agentbox/commit/51d93c10139a661e0f951c9d1490ad829499caa4))
* the Work page opens and scrolls quickly with a long history of past agents ([#530](https://github.com/leciric/agentbox/issues/530)) ([d10e2a1](https://github.com/leciric/agentbox/commit/d10e2a174a8511e2bd92c33dfb87fa4ad8fa4874))
* turning on GPU for agents on a Mac no longer leaves AgentBox's VM broken ([#476](https://github.com/leciric/agentbox/issues/476)) ([d1cfd80](https://github.com/leciric/agentbox/commit/d1cfd80e02028e4d981b6cf18f7d8e5abd261fce))
* vite and node build scripts run without npm now wait or pause when the VM's memory is short ([#471](https://github.com/leciric/agentbox/issues/471)) ([c248f9b](https://github.com/leciric/agentbox/commit/c248f9bb289c4e4dea4e8ec036efb496770a0e47))
* WebGL and other GPU programs on an agent's desktop draw at 60 fps with nobody watching, not 1 ([#475](https://github.com/leciric/agentbox/issues/475)) ([811ec8d](https://github.com/leciric/agentbox/commit/811ec8dc29eef51116576f26485dad8726630aa0))

## [0.15.0](https://github.com/leciric/agentbox/compare/v0.14.0...v0.15.0) (2026-10-21)


### Added

* a krunkit VM on a Mac readies a GPU for agents (Vulkan on Metal, OpenGL through Zink), and agentbox vm gpu says which renderer they get ([#426](https://github.com/leciric/agentbox/issues/426)) ([872d273](https://github.com/leciric/agentbox/commit/872d27360f1b2212b7f0fb7b3dcccd6873def257))
* a pull request chip in the agent lists, and search finds agents by PR number ([#406](https://github.com/leciric/agentbox/issues/406)) ([c80302e](https://github.com/leciric/agentbox/commit/c80302e3affcf42199e5ef76d25746578ca9f990))
* a simpler review that leads with the proof and the verdicts, fully usable from the keyboard ([#408](https://github.com/leciric/agentbox/issues/408)) ([9914552](https://github.com/leciric/agentbox/commit/991455237d8bb6ca6f962a4d78d39f0714ce2573))
* agent rows show a live terminal in a fixed-size thumbnail, previewed large on hover, with CPU/RAM beside it ([#420](https://github.com/leciric/agentbox/issues/420)) ([7dd833f](https://github.com/leciric/agentbox/commit/7dd833f92e3ae275482d59e71d6899ab1b0859bc))
* agents clear their outdated screenshots and recordings after a rework ([#411](https://github.com/leciric/agentbox/issues/411)) ([b041f68](https://github.com/leciric/agentbox/commit/b041f6824f49c5011d530d27dafce7c6e84ed6f4))
* agents record a before and an after for every visible change ([#390](https://github.com/leciric/agentbox/issues/390)) ([ef617b6](https://github.com/leciric/agentbox/commit/ef617b682ca6596852404365266156c3edb74447))
* agents record a walkthrough by rehearsing it once, then replaying it with no model running ([#424](https://github.com/leciric/agentbox/issues/424)) ([4af9e33](https://github.com/leciric/agentbox/commit/4af9e33db17bfbd960e7f942300a624cdee5f377))
* agents' commits and pull requests no longer credit the AI tool unless turned on in Settings ([#422](https://github.com/leciric/agentbox/issues/422)) ([3688713](https://github.com/leciric/agentbox/commit/36887133d5543c90afa72b51d5ebb3798c086f3c))
* drag the live desktops lane wider to see bigger desktops ([#388](https://github.com/leciric/agentbox/issues/388)) ([a902399](https://github.com/leciric/agentbox/commit/a902399a65b513f49c0f5a7e6057a8f2a451a7c1))
* each agent's own computer on its row, hover card and page, and a right panel that shows one agent in four views ([#384](https://github.com/leciric/agentbox/issues/384)) ([aa321fb](https://github.com/leciric/agentbox/commit/aa321fbd4c0aa0af28ee4a3dbc1dbeb0a48a22b3))
* GPU for agents on Linux: AgentBox's VM gets your GPU (OpenGL and Vulkan), from one patched Cloud Hypervisor ([#429](https://github.com/leciric/agentbox/issues/429)) ([b765b02](https://github.com/leciric/agentbox/commit/b765b025928c7e731b06415e757e62994bfdd803))
* GPU for agents on Windows: agents get WSL's GPU, and their desktop renders OpenGL on it ([#425](https://github.com/leciric/agentbox/issues/425)) ([796bbc5](https://github.com/leciric/agentbox/commit/796bbc5b5e654fdffb44b47a471f98b0f87af930))
* keep Claude Code up to date without waiting for an AgentBox release ([#386](https://github.com/leciric/agentbox/issues/386)) ([9678048](https://github.com/leciric/agentbox/commit/96780484466e3678501f2724e70ee9b5bd445f9a))
* leave notes on diff lines in the review and send them all with Request changes ([#393](https://github.com/leciric/agentbox/issues/393)) ([c834cb4](https://github.com/leciric/agentbox/commit/c834cb450c8219ada76a145f1e5c48a51fbca5a3))
* list every project in a stable order; grouping idle projects and sorting by activity is now a setting ([#413](https://github.com/leciric/agentbox/issues/413)) ([e9edba5](https://github.com/leciric/agentbox/commit/e9edba5d446d8f8d6a2f63afe85a0167d5f87f8a))
* pick an element on an agent's page and send it to the chat ([#394](https://github.com/leciric/agentbox/issues/394)) ([da56204](https://github.com/leciric/agentbox/commit/da56204ca3456ccb6b94b45a341e9ba956ec0779))
* pull requests open as a tab of the right panel ([#453](https://github.com/leciric/agentbox/issues/453)) ([55f477c](https://github.com/leciric/agentbox/commit/55f477cb5bdd8af28ac28749e179ba22a88a7065))
* recordings of the display show the browser's page, and media save takes an id or a name ([#436](https://github.com/leciric/agentbox/issues/436)) ([2c31514](https://github.com/leciric/agentbox/commit/2c3151408a55729114a553e50419c0d24e06bebd))
* recordings skip the time the screen stands still, so they show only the action ([#423](https://github.com/leciric/agentbox/issues/423)) ([afdd191](https://github.com/leciric/agentbox/commit/afdd19114162855b3fd3fa0e3b1c20d932867a17))
* run one task on several agents and keep the best (Compare view) ([#395](https://github.com/leciric/agentbox/issues/395)) ([277a22e](https://github.com/leciric/agentbox/commit/277a22e15edca32887b8e58129ae8b15617820be))
* schedule tasks for a project's lead, once or on a cron, in your timezone ([#392](https://github.com/leciric/agentbox/issues/392)) ([f0de6f1](https://github.com/leciric/agentbox/commit/f0de6f1fd6fbcfa6ad8a9cea3c3de6fb6998ba98))
* see the desktops your agents are using in a lane beside the lead chat ([#379](https://github.com/leciric/agentbox/issues/379)) ([836e0d4](https://github.com/leciric/agentbox/commit/836e0d40b73f46ca241f83102dd89d2eae11312e))
* see what each agent did as a timeline over its recordings, and a project's day as a Today reel ([#380](https://github.com/leciric/agentbox/issues/380)) ([8ef058e](https://github.com/leciric/agentbox/commit/8ef058ec80c26645647cf316f6f421b288322d55))
* Settings → Diagnostics and agentbox trace summary show slow operations, stalls and resource history, kept on your machine ([#397](https://github.com/leciric/agentbox/issues/397)) ([53bbd00](https://github.com/leciric/agentbox/commit/53bbd0000e6fe84bb18e9645af22ce351e758bf5))
* the agent list is wider, its live desktops bigger, and rows animate between tool icon and desktop ([#407](https://github.com/leciric/agentbox/issues/407)) ([a3f4f80](https://github.com/leciric/agentbox/commit/a3f4f8064220094e406270fcce994de06ee8557e))
* the lead's panel switches between Agents and Files, and an agent's chat has a way back to the lead ([#412](https://github.com/leciric/agentbox/issues/412)) ([eb20fd3](https://github.com/leciric/agentbox/commit/eb20fd37dc89f2abe15246f5ed7842551e7e6764))
* the review opens on the agent's proof, and the lead chat shows what each agent finished with ([#377](https://github.com/leciric/agentbox/issues/377)) ([35f351c](https://github.com/leciric/agentbox/commit/35f351ca369383e087f2a3ab9e61afe293a9e372))
* the usage pill shows the account's name when it isn't the default ([#452](https://github.com/leciric/agentbox/issues/452)) ([a72e993](https://github.com/leciric/agentbox/commit/a72e993fed14d2e51ad31e577deb673b6e3b402b))
* turning on Codex or OpenCode no longer rebuilds the base image; Setup downloads it and adds them ([#439](https://github.com/leciric/agentbox/issues/439)) ([a8aa096](https://github.com/leciric/agentbox/commit/a8aa0963f89812d64353bf606ab73fdbeb9c46ef))
* UI v2 shows a project's agents beside its lead chat, with Pull requests and Settings one click away ([#401](https://github.com/leciric/agentbox/issues/401)) ([ce39b05](https://github.com/leciric/agentbox/commit/ce39b0534b3fd065a6169bd17925e9369eaf866f))
* UI v2's sidebar shows your project sections, to make, rename, fold and drag projects into ([#450](https://github.com/leciric/agentbox/issues/450)) ([6ed91d0](https://github.com/leciric/agentbox/commit/6ed91d0116b52da297a04242a248f5865af7612f))


### Fixed

* a finished, sleeping agent shows one bar over the chat composer instead of two stacked ([#441](https://github.com/leciric/agentbox/issues/441)) ([2d56454](https://github.com/leciric/agentbox/commit/2d564548f5f14bb084051381962b818c70e2436e))
* a lead's chat shows an agent's evidence card once, however often it finishes ([#414](https://github.com/leciric/agentbox/issues/414)) ([2a4dfbd](https://github.com/leciric/agentbox/commit/2a4dfbd1791bedefe5575dc09da20514c0785d0e))
* a Mac's VM changes its kernel and builds the GPU driver only when GPU for agents is turned on, and falls back to its old kernel if the new one doesn't boot ([#428](https://github.com/leciric/agentbox/issues/428)) ([2675285](https://github.com/leciric/agentbox/commit/26752850b8154e29f247f50c00bfceadb138caef))
* agents can delete their own media again, and a recording that fails to finish keeps what was captured ([#438](https://github.com/leciric/agentbox/issues/438)) ([fb7231e](https://github.com/leciric/agentbox/commit/fb7231eee7f663415653695df0ec1f810d9e2275))
* agents get DNS and full downloads on Windows with WSL's mirrored networking ([#417](https://github.com/leciric/agentbox/issues/417)) ([a644270](https://github.com/leciric/agentbox/commit/a644270b6d708dd6824d156c7540bf1f134c1576))
* agents whose pull requests were merged long ago no longer reappear in Ready to review ([#447](https://github.com/leciric/agentbox/issues/447)) ([c82ccb4](https://github.com/leciric/agentbox/commit/c82ccb4ce01e2a6930a2fe32ac949861a017d4dd))
* an agent row without a live desktop shows its tool's icon at full size ([#405](https://github.com/leciric/agentbox/issues/405)) ([0502513](https://github.com/leciric/agentbox/commit/0502513c2898da86a7c2e7c3736eb86b7a497cbc))
* an agent with a follow-up pull request stays in review until its last pull request ends ([#409](https://github.com/leciric/agentbox/issues/409)) ([eaa3795](https://github.com/leciric/agentbox/commit/eaa3795d413f55e91c9bfa57346cd1136be0cc56))
* chat previews and diagrams are as wide as the message text ([#440](https://github.com/leciric/agentbox/issues/440)) ([0574d4a](https://github.com/leciric/agentbox/commit/0574d4a845073a529dd522f6fa30e00e1497bec9))
* GPU draws in agents' machines no longer hang, and Vulkan offers only the chosen GPU ([#443](https://github.com/leciric/agentbox/issues/443)) ([d7f850f](https://github.com/leciric/agentbox/commit/d7f850f52373324aa246ae73e804c166b3021c3e))
* GPU for agents can be turned on in a VM an older AgentBox started, and Settings offers Restart now where the VM must restart ([#434](https://github.com/leciric/agentbox/issues/434)) ([6293764](https://github.com/leciric/agentbox/commit/6293764ff220adab7780c8106f50f8e511aed948))
* idle projects no longer listed as active for questions nobody can answer ([#391](https://github.com/leciric/agentbox/issues/391)) ([a6495b0](https://github.com/leciric/agentbox/commit/a6495b007cf85568387b8533c12d038653d8f0bf))
* new agents start again when the AI tool isn't credited in commits ([#433](https://github.com/leciric/agentbox/issues/433)) ([da76056](https://github.com/leciric/agentbox/commit/da7605665cf0006b2f9a4ed9f013b788d73e0518))
* no more card under lead messages that mention a working agent ([#389](https://github.com/leciric/agentbox/issues/389)) ([8396589](https://github.com/leciric/agentbox/commit/8396589657d5ab74848ae847797213cadbd26dce))
* on Windows, opening Settings no longer launches a second AgentBox over and over ([#410](https://github.com/leciric/agentbox/issues/410)) ([6bcff5a](https://github.com/leciric/agentbox/commit/6bcff5a350dde8835da83690ffe2d405a2663d56))
* starting an agent no longer fails when another change to its machine's settings lands at the same moment ([#437](https://github.com/leciric/agentbox/issues/437)) ([27488fa](https://github.com/leciric/agentbox/commit/27488fa0e0b141d75226b3d3823883b6e1699631))
* the app no longer crashes on start when its daemon is older than it ([#381](https://github.com/leciric/agentbox/issues/381)) ([752d95b](https://github.com/leciric/agentbox/commit/752d95bd2ca81a7bdca72244102ae04845bdd33e))
* the image build stops with the failing network hop instead of hanging without a connection ([#416](https://github.com/leciric/agentbox/issues/416)) ([d8e8985](https://github.com/leciric/agentbox/commit/d8e89852b912487bf531a097c3979a241b20cc03))
* the phone app no longer crashes on every start once a project has agents ([#442](https://github.com/leciric/agentbox/issues/442)) ([2a06098](https://github.com/leciric/agentbox/commit/2a0609827df046287e3824eb0585c5012a34a4be))
* the PR number on the review card opens the pull request on GitHub ([#402](https://github.com/leciric/agentbox/issues/402)) ([c0c354f](https://github.com/leciric/agentbox/commit/c0c354f6dcf4e0ab13410ff73343a08fe7edddb3))
* the review modal never scrolls sideways with many media ([#421](https://github.com/leciric/agentbox/issues/421)) ([259af71](https://github.com/leciric/agentbox/commit/259af71a2be3e285ae988a5e34b2677c69f2c4fc))
* the review modal's media strip scrolls on its own and the checks column stays inside it ([#418](https://github.com/leciric/agentbox/issues/418)) ([501b1b8](https://github.com/leciric/agentbox/commit/501b1b86d81116cfc148d18d80f938e6aebef296))
* the shut-down button in the top bar is just its icon ([#451](https://github.com/leciric/agentbox/issues/451)) ([0b5dec7](https://github.com/leciric/agentbox/commit/0b5dec7df083781b7e050695a4a5e374d41117f7))
* the sidebar header shows icons instead of labels ([#449](https://github.com/leciric/agentbox/issues/449)) ([e426da2](https://github.com/leciric/agentbox/commit/e426da2f6dbe15aeb78805013201a3494483784f))
* the sidebar no longer shows an "unread" count on project rows ([#419](https://github.com/leciric/agentbox/issues/419)) ([3a8db0f](https://github.com/leciric/agentbox/commit/3a8db0f983c4de14bad9bf5d4182133b8bfdb97f))
* the Today page scrolls when its chapters are taller than the window ([#387](https://github.com/leciric/agentbox/issues/387)) ([6642b52](https://github.com/leciric/agentbox/commit/6642b52954eebb5188608805806b5267355a64f5))
* Windows setup explains a missing Virtual Machine Platform before downloading Ubuntu ([#415](https://github.com/leciric/agentbox/issues/415)) ([1202ff5](https://github.com/leciric/agentbox/commit/1202ff57e03f57879c84ac5bcb38ad5b6febebce))

## [0.14.0](https://github.com/leciric/agentbox/compare/v0.13.0...v0.14.0) (2026-10-16)


### Added

* Media and Jobs as icons at the sidebar's foot in UI v2, and Overview becomes Home's Status tab ([#363](https://github.com/leciric/agentbox/issues/363)) ([2085b7a](https://github.com/leciric/agentbox/commit/2085b7a0f521946fc073bb0ba6419146525b0edd))
* Open desktop from an agent's right-click menu ([#357](https://github.com/leciric/agentbox/issues/357)) ([732fa52](https://github.com/leciric/agentbox/commit/732fa52e96352102e3ef2fb1ac9b82c110031954))


### Fixed

* adding a connector says why Add is greyed out, and a token connector names its secret itself ([#358](https://github.com/leciric/agentbox/issues/358)) ([6a3345c](https://github.com/leciric/agentbox/commit/6a3345c3608df4247e0ce5eb1f6de331d93d2db1))
* eight bugs from an end-to-end pass: Talk to another project, the tour, first-message notices, the model menu and the review ([#353](https://github.com/leciric/agentbox/issues/353)) ([c5871c2](https://github.com/leciric/agentbox/commit/c5871c23663eb905af67cebe8f57826bef2f2d4c))
* hovering an agent no longer crashes the app without usage data, and Hide on a pinned project moves it to Hidden ([#355](https://github.com/leciric/agentbox/issues/355)) ([920482b](https://github.com/leciric/agentbox/commit/920482bc0937778901421c227c9e92cbf640a4dc))
* merged or closed PRs no longer show as ready to review, and freed agents go straight to Past agents instead of "missing" ([#367](https://github.com/leciric/agentbox/issues/367)) ([2f8d745](https://github.com/leciric/agentbox/commit/2f8d7455f6cbc2a5775a2a1ceee877b412b54c16))
* on Windows, a slow first start after an update shows its progress instead of "Command failed" ([#366](https://github.com/leciric/agentbox/issues/366)) ([09e159d](https://github.com/leciric/agentbox/commit/09e159d29b79e5ff958d043a2ee82d8f2111aac5))
* Send says why when Claude Code isn't signed in, and the chat options ⋯ sits on the header row ([#359](https://github.com/leciric/agentbox/issues/359)) ([a723785](https://github.com/leciric/agentbox/commit/a72378572ff280f5bfb72c7e46ecbcacbb227ecf))
* tables and markdown read as plain text in Ctrl+K and the sidebar, and a streaming table waits for its separator row ([#356](https://github.com/leciric/agentbox/issues/356)) ([34a6dcd](https://github.com/leciric/agentbox/commit/34a6dcd1f23309921c3a3e6ba887b9aec43d8570))
* the review stays open after Merge, Reviewed and Request changes, and goes on to the next agent ([#362](https://github.com/leciric/agentbox/issues/362)) ([6429b6e](https://github.com/leciric/agentbox/commit/6429b6e5589c9bd355311c49761624a496c0960c))
* the right panel's tabs show their names, + follows the last tab, and closing a tab shows its neighbour ([#361](https://github.com/leciric/agentbox/issues/361)) ([3a2c71e](https://github.com/leciric/agentbox/commit/3a2c71e17989e579669c98175d76842d642289d9))
* UI v2 sidebar rows are two lines again, and Idle sits at the bottom above the foot ([#365](https://github.com/leciric/agentbox/issues/365)) ([c0314f2](https://github.com/leciric/agentbox/commit/c0314f2969b1ddf319ed93fc04052108047af2ae))
* UI v2's review card docks on the composer, and the sidebar, Settings and the panel no longer overflow ([#364](https://github.com/leciric/agentbox/issues/364)) ([7b8ebb3](https://github.com/leciric/agentbox/commit/7b8ebb35ba3be8e23f078fb7fc140eb7b1a01aa4))
* v2 polish from the UX test: wording, Y/N on agent permissions, Settings back button, review keys in the shortcuts sheet and more ([#360](https://github.com/leciric/agentbox/issues/360)) ([30b9b1a](https://github.com/leciric/agentbox/commit/30b9b1a40e800dda9689961de76bdf43d9a03845))

## [0.13.0](https://github.com/leciric/agentbox/compare/v0.12.1...v0.13.0) (2026-10-16)


### Added

* a calmer chat header, agent view and top bar, with one inbox for what needs you ([#311](https://github.com/leciric/agentbox/issues/311)) ([51d0365](https://github.com/leciric/agentbox/commit/51d0365a24aeb81b6cb688afb091049f87d11792))
* a chime and a "new" mark when an agent is ready to review ([#299](https://github.com/leciric/agentbox/issues/299)) ([0fc4134](https://github.com/leciric/agentbox/commit/0fc4134b44b22029ef1bf772a31ac112af0d4387))
* a file tree and file viewer beside every chat, live as agents edit ([#306](https://github.com/leciric/agentbox/issues/306)) ([27a2682](https://github.com/leciric/agentbox/commit/27a2682848b9b57032c0a19723d288b82698508f))
* a native phone app (Expo / React Native) that pairs with AgentBox, follows projects, leads and agents live, and answers and merges from the phone ([#332](https://github.com/leciric/agentbox/issues/332)) ([7f87789](https://github.com/leciric/agentbox/commit/7f87789309c6a69327eab5077e23278f6b5490b5))
* agentbox memory tidy removes secrets from what project memory stored before ([#259](https://github.com/leciric/agentbox/issues/259)) ([8a2829e](https://github.com/leciric/agentbox/commit/8a2829e0fbf22e72e3b4214272a05da5429d8818))
* AgentBox-wide connectors, signed in once in Settings, with a per-project off / inherit / on ([#236](https://github.com/leciric/agentbox/issues/236)) ([192457e](https://github.com/leciric/agentbox/commit/192457ef6d88b832c109de2a1ca688c3fb3ff3ea))
* agents and the lead can mark a memory wrong, stale or helpful, and memories written down again rank higher ([#255](https://github.com/leciric/agentbox/issues/255)) ([0fbde64](https://github.com/leciric/agentbox/commit/0fbde642a78ea87f1c18cbe1bed033362b0a84b3))
* agents start at once, and tests wait or pause while the VM's memory is short ([#252](https://github.com/leciric/agentbox/issues/252)) ([ad25737](https://github.com/leciric/agentbox/commit/ad25737ed7873102dcd7784e2c87f068906cf371))
* answer an agent's permission requests from the project's chat, where the lead knows about them ([#323](https://github.com/leciric/agentbox/issues/323)) ([d4d8ead](https://github.com/leciric/agentbox/commit/d4d8ead8584a1d5abd6a42ae654da7ff002320fb))
* attach any file in the chat (PDFs, CSVs, zips…), not only images ([#309](https://github.com/leciric/agentbox/issues/309)) ([d94ca24](https://github.com/leciric/agentbox/commit/d94ca240f1eb9073a5622fa019827bb298bb7129))
* chats draw diagrams, charts, pictures and HTML pages inline, and publish them to Hatch ([#293](https://github.com/leciric/agentbox/issues/293)) ([697e627](https://github.com/leciric/agentbox/commit/697e627f5c4ab3d8f459bb83122185ce5bb13d5f))
* choose when the pages stack shows by the composer: always, only for new pages, or never ([#268](https://github.com/leciric/agentbox/issues/268)) ([bb8988e](https://github.com/leciric/agentbox/commit/bb8988e5fe62101381344c4e3f21fb76a8fb6289))
* clicking a pull request opens it in the app, with its description, pictures, checks and diffs ([#263](https://github.com/leciric/agentbox/issues/263)) ([4ee8a6e](https://github.com/leciric/agentbox/commit/4ee8a6e44f32e29a31c27632c8cc59c0f287d62c))
* Codex, OpenCode and Cursor accounts, picked per project like Claude Code's ([#305](https://github.com/leciric/agentbox/issues/305)) ([d52ef35](https://github.com/leciric/agentbox/commit/d52ef355b5abb8ed12d7df07f0def80aa66d2388))
* Cursor as a fourth AI tool, driven through Cursor's TypeScript SDK ([#226](https://github.com/leciric/agentbox/issues/226)) ([e2c6749](https://github.com/leciric/agentbox/commit/e2c67499427d6d1b29b77ec41d44254179321335))
* destroyed agents are kept as past agents, with their chat, until you delete them ([#296](https://github.com/leciric/agentbox/issues/296)) ([85deaaa](https://github.com/leciric/agentbox/commit/85deaaaf5edba39ae95cae21678ad590cdeec55c))
* each agent gets a share of the VM's cores, so test runners and builds start fewer workers and use less memory ([#249](https://github.com/leciric/agentbox/issues/249)) ([e127933](https://github.com/leciric/agentbox/commit/e1279333cd39b278e6f8fd585900f0abbdc64a22))
* error reports are on by default, with a one-time notice instead of a question ([#272](https://github.com/leciric/agentbox/issues/272)) ([77bae22](https://github.com/leciric/agentbox/commit/77bae222be09411ad3295df59595e8a056b7225b))
* find in a chat with a list of every match and a From filter, and search everything from a modal with filters and a preview ([#270](https://github.com/leciric/agentbox/issues/270)) ([e3b75dd](https://github.com/leciric/agentbox/commit/e3b75dd736ed885d117b63f75d5a0a542de29917))
* find in a chat with Ctrl+F, including messages not loaded yet ([#241](https://github.com/leciric/agentbox/issues/241)) ([2d06581](https://github.com/leciric/agentbox/commit/2d06581ad3be798bd0de8e28980f7cb9766087ef))
* global Media's Pages tab filters by project, and its search finds a project's name ([#261](https://github.com/leciric/agentbox/issues/261)) ([b168eb6](https://github.com/leciric/agentbox/commit/b168eb6095ee9e056798fd48dd975449e9f2681a))
* Hatch is a built-in connector, and the connector grid fits six tiles ([#243](https://github.com/leciric/agentbox/issues/243)) ([2bb5a63](https://github.com/leciric/agentbox/commit/2bb5a6343610d72f7e6f9d12e256b39cc7288afd))
* hide projects from v2's sidebar, with Idle always last and a Review all button ([#343](https://github.com/leciric/agentbox/issues/343)) ([45f0da6](https://github.com/leciric/agentbox/commit/45f0da60a66e36d6805aea8326b7509024bd5e17))
* import the MCP servers your AI tools already have as connectors ([#277](https://github.com/leciric/agentbox/issues/277)) ([74a3588](https://github.com/leciric/agentbox/commit/74a3588a64f4b07910580af488508f843dc873dc))
* Media views load a page at a time as you scroll, opening instantly on thousands of items ([#269](https://github.com/leciric/agentbox/issues/269)) ([1e05c3b](https://github.com/leciric/agentbox/commit/1e05c3b3f7f7f13c87f1d86e3423170c8d4cf558))
* memory for every project, kept by any chat and listed in Settings → Memory ([#258](https://github.com/leciric/agentbox/issues/258)) ([a1f1bb7](https://github.com/leciric/agentbox/commit/a1f1bb7590e3ec92abd6281ec1e43b56832ac6dd))
* mention another project with @ in the chat, and let the lead copy its files into an agent ([#291](https://github.com/leciric/agentbox/issues/291)) ([ec1fbc2](https://github.com/leciric/agentbox/commit/ec1fbc2ce04bd4465bdbfd02e9bae28f6dcd90a7))
* new installs start on Opus with medium effort and a 1M context ([#339](https://github.com/leciric/agentbox/issues/339)) ([9b359c3](https://github.com/leciric/agentbox/commit/9b359c3b87b3de7ff558053378cb707ed44a3e39))
* nightly builds show the starry header and Nightly badge in UI v2 too ([#349](https://github.com/leciric/agentbox/issues/349)) ([dc10651](https://github.com/leciric/agentbox/commit/dc10651de0859c009f5ed48af1cc9ab98ea35655))
* paired phones get their own permissions, and the app shows what each phone did ([#294](https://github.com/leciric/agentbox/issues/294)) ([8bc8d0e](https://github.com/leciric/agentbox/commit/8bc8d0ef6be9fc7262867b68bffd37daf2a5b788))
* phones get a stable address from the hub, so a paired phone keeps working after a restart ([#334](https://github.com/leciric/agentbox/issues/334)) ([d52bd41](https://github.com/leciric/agentbox/commit/d52bd4117f7265d15df5991b18140cecd89a20ac))
* project memory removes secrets before storing anything, and briefs fence recalled memory as data ([#253](https://github.com/leciric/agentbox/issues/253)) ([aa80768](https://github.com/leciric/agentbox/commit/aa80768eda990ea4bb85399a085b880048ed5f5e))
* projects' leads can message each other and align on shared changes ([#310](https://github.com/leciric/agentbox/issues/310)) ([264bd33](https://github.com/leciric/agentbox/commit/264bd33bd9e2b2c96a6d393f22d736bc29dd0c68))
* pull request label chips have an X that removes the label ([#266](https://github.com/leciric/agentbox/issues/266)) ([fb7740a](https://github.com/leciric/agentbox/commit/fb7740af2015c09a83dbaa636101f9e3b2ed4564))
* pull requests show and edit their labels, filter to your own, and stay current ([#264](https://github.com/leciric/agentbox/issues/264)) ([d449ac0](https://github.com/leciric/agentbox/commit/d449ac0e3829b6f7fde525e3ded353cfe0c79c28))
* Ready to review: see each finished agent's screenshots, recordings, pages and PR in one carousel ([#295](https://github.com/leciric/agentbox/issues/295)) ([d6ba5b8](https://github.com/leciric/agentbox/commit/d6ba5b817af1c3d0bc5a945c21b2d063b945d466))
* reload a chat's tools after a connector change without losing the conversation ([#235](https://github.com/leciric/agentbox/issues/235)) ([4676e4a](https://github.com/leciric/agentbox/commit/4676e4a78a9f4e4294c31b73c272df4e66d980c0))
* review all finished agents in one walk: merge from the review, next agent, keys, and a Reviewed section ([#301](https://github.com/leciric/agentbox/issues/301)) ([1dfc623](https://github.com/leciric/agentbox/commit/1dfc62326154fb8190827b7853753fcd76adb4d2))
* reviewing an agent, or merging its pull request, gives its machine back unless it holds unpushed work ([#316](https://github.com/leciric/agentbox/issues/316)) ([eac8a6a](https://github.com/leciric/agentbox/commit/eac8a6ab674e9adce10d0240bb7711900051d088))
* rich renders use the chat's full width ([#307](https://github.com/leciric/agentbox/issues/307)) ([7de0dc6](https://github.com/leciric/agentbox/commit/7de0dc65ce9a28f86a4ccb87830a34694eb17781))
* search everything in AgentBox from one palette (Ctrl/Cmd+K) ([#242](https://github.com/leciric/agentbox/issues/242)) ([81acf45](https://github.com/leciric/agentbox/commit/81acf45e18c21f08bc0eb33a701e1943a8e7c1f8))
* see the pages your agents publish on Hatch: a stack by the message box, a Pages tab, and in Media ([#248](https://github.com/leciric/agentbox/issues/248)) ([4a1dc83](https://github.com/leciric/agentbox/commit/4a1dc836853d5e451bccec444f64723d9aec739a))
* Settings regrouped into plain pages with Notifications on its own, and v2's sidebar foot trimmed to Settings and Past agents ([#342](https://github.com/leciric/agentbox/issues/342)) ([8f90ad6](https://github.com/leciric/agentbox/commit/8f90ad668ee305ecc31442d0062bf1b12b50e897))
* Setup downloads a prebuilt agent base image instead of building it from Debian ([#275](https://github.com/leciric/agentbox/issues/275)) ([0969090](https://github.com/leciric/agentbox/commit/0969090d6d60a451083c6dfe3f34addadd713be4))
* Setup downloads the prebuilt base image from GitHub releases instead of R2 ([#278](https://github.com/leciric/agentbox/issues/278)) ([ccc9da7](https://github.com/leciric/agentbox/commit/ccc9da754ad68e1fbc41e2b8174d14135d84b395))
* silence notifications, with per-kind switches and Do not disturb ([#302](https://github.com/leciric/agentbox/issues/302)) ([9ff65c2](https://github.com/leciric/agentbox/commit/9ff65c2fbdae55a75933434bcb95b589839a176f))
* skills for every agent and chat, AgentBox-wide or per project, imported from your AI tools, a folder or git ([#230](https://github.com/leciric/agentbox/issues/230)) ([ee35be9](https://github.com/leciric/agentbox/commit/ee35be9453f19755c73fb4c831057edb0a2c8131))
* star media to keep it: favorites are never removed automatically, and the Media view filters to them ([#229](https://github.com/leciric/agentbox/issues/229)) ([5e78f75](https://github.com/leciric/agentbox/commit/5e78f7563da382bbd84857ff4fb36fcc79989c11))
* switch the lead's AI tool, account and model from the chat's composer ([#297](https://github.com/leciric/agentbox/issues/297)) ([b54a83a](https://github.com/leciric/agentbox/commit/b54a83abb989ca6fcef97549c3f080d422e247c2))
* the base image builds in the background, and new agents wait for it instead of failing ([#290](https://github.com/leciric/agentbox/issues/290)) ([9e5a00b](https://github.com/leciric/agentbox/commit/9e5a00bacc862e008cf0b6f0226ca2c2bfe169f0))
* the chat composer shows only the AI tool's icon, with quota shown in the picker's menu ([#318](https://github.com/leciric/agentbox/issues/318)) ([2196aae](https://github.com/leciric/agentbox/commit/2196aae9564f9ae011cc64c13c14b893144a2fd2))
* the Home chat has a Memory tab listing what is remembered for every project ([#267](https://github.com/leciric/agentbox/issues/267)) ([2025a55](https://github.com/leciric/agentbox/commit/2025a5578856a00ca3ea92242a0cbbe3eef21b8d))
* the inbox keeps only what needs you, opening an agent in review marks it reviewed, and lists load on scroll ([#313](https://github.com/leciric/agentbox/issues/313)) ([986c661](https://github.com/leciric/agentbox/commit/986c661030bb68fa3d06eb25d60f383f2b70bc20))
* the lead is offered memories its agents keep being handed as project notes, to accept or dismiss ([#257](https://github.com/leciric/agentbox/issues/257)) ([5d13a87](https://github.com/leciric/agentbox/commit/5d13a874ab29717fdec6484e4c17b2efef490285))
* the new layout (v2) is the default, with a first-run tour and replayable What's new tours ([#347](https://github.com/leciric/agentbox/issues/347)) ([659e34a](https://github.com/leciric/agentbox/commit/659e34a9df9ad45f67c85832ba26bd7530e3651d))
* the phone shows what each chat is doing live, and reconnects cleanly after sleep or a dropped tunnel ([#303](https://github.com/leciric/agentbox/issues/303)) ([caf83f3](https://github.com/leciric/agentbox/commit/caf83f370fbacaf26cdd8ac58dc2858041951c05))
* the usage meter shows Codex's 5-hour and weekly limits, and says so for tools that report none ([#336](https://github.com/leciric/agentbox/issues/336)) ([16c215a](https://github.com/leciric/agentbox/commit/16c215a513c530038f855cc3e64135472f89cb2b))
* try a new lead-first layout (UI v2) from Settings → Experimental, with Request changes in the review ([#315](https://github.com/leciric/agentbox/issues/315)) ([a7be077](https://github.com/leciric/agentbox/commit/a7be077070fba38c90f796281c48b56509c02477))
* UI v2 drops the inbox, puts ⋯ (with New chat) and an icon-only Panel at the top bar's right, fixes Talk to another project, and adds a Reviewed tab to review ([#344](https://github.com/leciric/agentbox/issues/344)) ([d506dad](https://github.com/leciric/agentbox/commit/d506dad6d140331d3c896abd29a99093132468ef))
* UI v2's New task (Ctrl+N) picks a project from the keyboard and opens its chat ready to write in ([#319](https://github.com/leciric/agentbox/issues/319)) ([c8d8c60](https://github.com/leciric/agentbox/commit/c8d8c60602e4818fec69ecacb4dd8abe43f02809))
* Update available installs the new version in place and restarts ([#240](https://github.com/leciric/agentbox/issues/240)) ([0736975](https://github.com/leciric/agentbox/commit/0736975ed3da27aa3824f4bb3ef046d4087289fe))
* v2 sidebar agent rows show the agent number and are roomier ([#348](https://github.com/leciric/agentbox/issues/348)) ([f7efbee](https://github.com/leciric/agentbox/commit/f7efbeef06cd21d488bdadbff35b5af6c818dd52))
* v2's project header is one breadcrumb with a ⋯ menu ([#331](https://github.com/leciric/agentbox/issues/331)) ([46ccb4a](https://github.com/leciric/agentbox/commit/46ccb4a7f47cdb3252c5d812a6a9b97b3c3db85e))
* v2's right-hand panel works like T3Code's: tabs per chat, a launcher of cards, and one width ([#326](https://github.com/leciric/agentbox/issues/326)) ([61b03f1](https://github.com/leciric/agentbox/commit/61b03f1781aa82efd73c222492af1058412c5c99))
* v2's right-hand panel: files, desktop, terminal, browser, pages, media, pull requests and settings beside the chat, for any agent ([#324](https://github.com/leciric/agentbox/issues/324)) ([f86068e](https://github.com/leciric/agentbox/commit/f86068ecc4952135515dec7caa597c628d508826))
* v2's sidebar pins projects, sorts them, shelves settled ones and shows project icons, with one review walk per project ([#321](https://github.com/leciric/agentbox/issues/321)) ([42fcdf0](https://github.com/leciric/agentbox/commit/42fcdf0f888aac6e70d1c7a095eefbd003e7b6bb))
* v2's sidebar shows only projects with something live by default, and Show all lists every project ([#325](https://github.com/leciric/agentbox/issues/325)) ([e5d2c54](https://github.com/leciric/agentbox/commit/e5d2c5421f133ec3d1c227e03543267ff6b88e2d))


### Fixed

* a chat moved to another Claude Code account restarts on it instead of staying on the old token ([#285](https://github.com/leciric/agentbox/issues/285)) ([596f8b2](https://github.com/leciric/agentbox/commit/596f8b2c0b99e0a48b8afaedf401a6622a571a41))
* a new agent no longer flashes "Needs attention" while it is created ([#328](https://github.com/leciric/agentbox/issues/328)) ([30926a0](https://github.com/leciric/agentbox/commit/30926a0836c4b469305e0150d7356b3589a17ba1))
* a new agent shows as starting while it is made, with no error, attention or notification ([#317](https://github.com/leciric/agentbox/issues/317)) ([188f176](https://github.com/leciric/agentbox/commit/188f176c2229b5d21218dbf9907e7c786cb89c78))
* a page published on Hatch always shows as new, not only when the daemon stored it in time ([#260](https://github.com/leciric/agentbox/issues/260)) ([d33437b](https://github.com/leciric/agentbox/commit/d33437b0deb0622c054c210309324903558e247a))
* a search result opens a long chat around it instead of loading everything since ([#247](https://github.com/leciric/agentbox/issues/247)) ([55810a1](https://github.com/leciric/agentbox/commit/55810a13db40d91c88d6e66a257944c0d7034707))
* a secret like ANTHROPIC_API_KEY no longer replaces the AI tool's account ([#284](https://github.com/leciric/agentbox/issues/284)) ([238b494](https://github.com/leciric/agentbox/commit/238b4942731c8737de3d2e31667b98dd240680ff))
* a stop-retired agent stays stopped when an answer or a memory notice reaches it ([#308](https://github.com/leciric/agentbox/issues/308)) ([80d2d27](https://github.com/leciric/agentbox/commit/80d2d27b03c51f2cd54a0746099979de8c173035))
* AgentBox stops reporting Incus as not answering once it answers again, and no longer restarts a healthy incus.service ([#271](https://github.com/leciric/agentbox/issues/271)) ([7bbb481](https://github.com/leciric/agentbox/commit/7bbb481564640a22dbf9ea42f4c8152f413b79c6))
* AgentBox's VM on the vz driver no longer spikes the CPU, and its memory panel counts what it really uses ([#300](https://github.com/leciric/agentbox/issues/300)) ([79415ec](https://github.com/leciric/agentbox/commit/79415ec172f0b83716ed2206830425d361887e0f))
* agents cap how many monorepo packages a task runner runs at once ([#282](https://github.com/leciric/agentbox/issues/282)) ([518a3ad](https://github.com/leciric/agentbox/commit/518a3ad4ba8cbae6f59b8952b19c383cef0dcace))
* agents that stop on a question, a promise or an error no longer look finished ([#292](https://github.com/leciric/agentbox/issues/292)) ([8c750e3](https://github.com/leciric/agentbox/commit/8c750e3a54918c4c5d13a7346c6e372aeae6dae8))
* an agent that hits its usage limit is paused and resumes itself when the limit resets, instead of showing as finished ([#320](https://github.com/leciric/agentbox/issues/320)) ([ed987d0](https://github.com/leciric/agentbox/commit/ed987d074a09d9431cadfee12250eba42870f364))
* an empty job log no longer crashes the app with "reading 'replace'" ([#238](https://github.com/leciric/agentbox/issues/238)) ([e1624d7](https://github.com/leciric/agentbox/commit/e1624d7da136c2672d855cab42cfcfb7b9c78670))
* build with Go 1.27.2 and golang.org/x/net v0.60.0, fixing net/http vulnerabilities ([#254](https://github.com/leciric/agentbox/issues/254)) ([868ba9f](https://github.com/leciric/agentbox/commit/868ba9fda2e3dbe1f3e2865a0c5339121d4988de))
* chats no longer restart their AI tool when nothing they use changed ([#265](https://github.com/leciric/agentbox/issues/265)) ([50e101a](https://github.com/leciric/agentbox/commit/50e101a0973e7c206a34ee71e0d020d42c6e3212))
* check OpenCode's help on stderr so the image verify passes ([#287](https://github.com/leciric/agentbox/issues/287)) ([421594c](https://github.com/leciric/agentbox/commit/421594ca9933170c3c387787ec6348bff6f5ebe0))
* Claude Code chats get Haiku 5.5, with the Claude ACP adapter 0.88.0 ([#232](https://github.com/leciric/agentbox/issues/232)) ([86b5de5](https://github.com/leciric/agentbox/commit/86b5de527a3a520dfa9b29b6fde48a64bdaff3f0))
* Docker's firewall no longer blocks agents' network in WSL or with a remote dockerd ([#280](https://github.com/leciric/agentbox/issues/280)) ([8627b5b](https://github.com/leciric/agentbox/commit/8627b5b998a87e5fb970069112e09ef0c9c12cda))
* dragging projects and sections in the sidebar lands where they're dropped and stays there ([#228](https://github.com/leciric/agentbox/issues/228)) ([8176fd5](https://github.com/leciric/agentbox/commit/8176fd558118a36f7eb6dd86840feb91f90c948a))
* first run says how to log in, Setup is one checklist with a next step, and Add project, Phone and the v2 switch are clearer ([#340](https://github.com/leciric/agentbox/issues/340)) ([d5accbf](https://github.com/leciric/agentbox/commit/d5accbf69e4a8a94283c92861fd48a86f40a86e8))
* heavy commands pause when the VM is about to run out of memory, before it stalls ([#273](https://github.com/leciric/agentbox/issues/273)) ([57e25e1](https://github.com/leciric/agentbox/commit/57e25e1b1cadb22a50fbf319e56339b3fa200e26))
* install OpenCode from mise's native backend so image builds work ([#286](https://github.com/leciric/agentbox/issues/286)) ([9c6411b](https://github.com/leciric/agentbox/commit/9c6411b9676e8a7c4e6f31345877c9ceb3232683))
* memory distillation skips repeats, empty chat turns and status lines, sending the cheap model about half the input ([#256](https://github.com/leciric/agentbox/issues/256)) ([c5ff596](https://github.com/leciric/agentbox/commit/c5ff5966f9452cdddd199727834f23645c5efaec))
* merging from review no longer errors on a freed agent, and the error reports notice shows once per install ([#327](https://github.com/leciric/agentbox/issues/327)) ([7c7bb5c](https://github.com/leciric/agentbox/commit/7c7bb5c5f9b804708704288fe933ed5c28c12dbd))
* on Windows, importing skills finds the ones your Windows AI tools have and takes Windows paths ([#237](https://github.com/leciric/agentbox/issues/237)) ([279eb97](https://github.com/leciric/agentbox/commit/279eb978f305b15b797656852cdd6868dfeabcdd))
* phones can chat with AgentBox on a Mac running its VM on the vz driver ([#312](https://github.com/leciric/agentbox/issues/312)) ([b9af888](https://github.com/leciric/agentbox/commit/b9af88858d420808c18d8f342597178cd56fa0bd))
* removing a project that ever had an agent no longer fails with a foreign key error ([#262](https://github.com/leciric/agentbox/issues/262)) ([fe65d6e](https://github.com/leciric/agentbox/commit/fe65d6e4ad047050daf219d8af92611ade2c1738))
* rich renders show a generating card while they stream, and tall previews fit the chat ([#330](https://github.com/leciric/agentbox/issues/330)) ([d016a19](https://github.com/leciric/agentbox/commit/d016a1968c9da2d23ea19fd20f36a8d02ba9bd70))
* rich renders sit in the chat's text column as cards instead of breaking out to full width ([#337](https://github.com/leciric/agentbox/issues/337)) ([2b6dff8](https://github.com/leciric/agentbox/commit/2b6dff8a439e1378d9210ae6129c19f429dfa3af))
* screenshots of an app on the display no longer come out blank ([#227](https://github.com/leciric/agentbox/issues/227)) ([d16c25a](https://github.com/leciric/agentbox/commit/d16c25ae0343b048fd9c315f57775a6b1ff8b49f))
* signing in to OpenCode works in the VM, which has no OpenCode of its own ([#289](https://github.com/leciric/agentbox/issues/289)) ([ea3a514](https://github.com/leciric/agentbox/commit/ea3a5143fd48de4e9651c24e4f8f6007fb3b4398))
* skills installed as symlinks (like npx skills add) can be imported ([#234](https://github.com/leciric/agentbox/issues/234)) ([996d3a8](https://github.com/leciric/agentbox/commit/996d3a8c7884eeb10a09eb3db463365d42716205))
* stale issues in project memory close themselves, age out of the lead's recap, and can be tidied ([#251](https://github.com/leciric/agentbox/issues/251)) ([e0e6c63](https://github.com/leciric/agentbox/commit/e0e6c63ffd9192fd7066a92c9392259b6d5c255a))
* tests no longer pause or wait for memory while the VM has gigabytes free ([#283](https://github.com/leciric/agentbox/issues/283)) ([936e9dd](https://github.com/leciric/agentbox/commit/936e9dd3e597a569c1a0d53218c340eeb8191813))
* the agent's desktop view keeps its aspect ratio and scales to fit instead of stretching ([#346](https://github.com/leciric/agentbox/issues/346)) ([c3e65e8](https://github.com/leciric/agentbox/commit/c3e65e8743f356a6d1ca7c43841cae3d4d50dce4))
* the error reports notice is one short line ([#274](https://github.com/leciric/agentbox/issues/274)) ([459b259](https://github.com/leciric/agentbox/commit/459b2599e8f78e3f634151337600b8c58c97d96d))
* the phone settings point iPhones that only open HTTPS to Reach from anywhere, and the phone page stops saying plain HTTP over https ([#314](https://github.com/leciric/agentbox/issues/314)) ([5861fe1](https://github.com/leciric/agentbox/commit/5861fe122f059b00929de133dad35a988ee023ed))
* the PRs tab lists every open pull request, not just the 30 most recently updated ([#276](https://github.com/leciric/agentbox/issues/276)) ([5db09dd](https://github.com/leciric/agentbox/commit/5db09ddd99b18959d3396732c4d5d8e09706e816))
* the Published image workflow reads the renamed files and publishes only when every build succeeded ([#279](https://github.com/leciric/agentbox/issues/279)) ([cff34a0](https://github.com/leciric/agentbox/commit/cff34a06a9ac080994831470bf7753eae38b46a5))
* the review footer keeps Merge and Request changes in fixed slots, the inbox opens the waiting card, and Approve is the primary permission button ([#338](https://github.com/leciric/agentbox/issues/338)) ([b05ef5e](https://github.com/leciric/agentbox/commit/b05ef5e2faf710f8994476f678e8c8d3043f714f))
* the review modal has one set of actions: Done reviewing (asks about merging), Request changes to the chat, and Back ([#322](https://github.com/leciric/agentbox/issues/322)) ([f20d8a6](https://github.com/leciric/agentbox/commit/f20d8a6b20f7ddd0b5f35bd65fd318cbc586a8f2))
* the review's Merge is one split button that remembers your method, and the end of a walk offers what's still waiting ([#329](https://github.com/leciric/agentbox/issues/329)) ([89b4e28](https://github.com/leciric/agentbox/commit/89b4e281315a98b534124569aeb508873dada4f3))
* the review's PR title and status sit at the top with the agent's, and Request changes opens its chat again ([#352](https://github.com/leciric/agentbox/issues/352)) ([97d3c2d](https://github.com/leciric/agentbox/commit/97d3c2d0a581f5f4d217a5f4ef8db5e9325451d6))
* the window fits portrait, small and changing monitors, and the app stays usable down to 480px wide ([#304](https://github.com/leciric/agentbox/issues/304)) ([680c4c1](https://github.com/leciric/agentbox/commit/680c4c18704e37ad00ff2ad79854cb1a730eb679))
* UI v2's agent page drops the top tabs; Desktop, Terminal, Media and Files open in the right panel ([#350](https://github.com/leciric/agentbox/issues/350)) ([5d812a9](https://github.com/leciric/agentbox/commit/5d812a9d896af68b4d4b8e1e0a97bd03819c6702))
* updating the app on Windows no longer crashes when the updates folder is locked ([#288](https://github.com/leciric/agentbox/issues/288)) ([31cb94d](https://github.com/leciric/agentbox/commit/31cb94dd6f68b4898b7c1333fa7bd74978fc03f8))
* v2 tour's review step, release notes wait for the tour, and the grouped-rows switch is gone ([#351](https://github.com/leciric/agentbox/issues/351)) ([e2d0230](https://github.com/leciric/agentbox/commit/e2d02303ceffa4de86da38b6e55766a4f114f3d5))
* v2's sidebar folds idle projects into an Idle list, stops working projects flickering, and puts project options on right-click ([#335](https://github.com/leciric/agentbox/issues/335)) ([c6464f3](https://github.com/leciric/agentbox/commit/c6464f385b18c5d45080c8ff45611761b7c238bb))
* v2's sidebar keeps your project, panel tabs stay readable, menus are shorter, and the keyboard reaches everything ([#341](https://github.com/leciric/agentbox/issues/341)) ([130c956](https://github.com/leciric/agentbox/commit/130c956b975db7b8fe89c707d1ce3b5595151aa6))


### Changed

* remove reading aloud and voice dictation ([#298](https://github.com/leciric/agentbox/issues/298)) ([b0ff724](https://github.com/leciric/agentbox/commit/b0ff724babf8db8fa298fae5f3932e35082f59f9))
* remove the Tasks tab and the user's task list ([#250](https://github.com/leciric/agentbox/issues/250)) ([07916f3](https://github.com/leciric/agentbox/commit/07916f3f6cacdb0ea0bc35faa4ad990ba3069096))

## [0.12.1](https://github.com/leciric/agentbox/compare/v0.12.0...v0.12.1) (2026-10-07)


### Fixed

* the top bar's memory shows what the VM uses of what it's been granted ([#223](https://github.com/leciric/agentbox/issues/223)) ([44a9648](https://github.com/leciric/agentbox/commit/44a9648fdccfa46e8116df79ee850d3b9ef27cee))
* updates are looked for hourly and usage stats are sent soon after they are due ([#224](https://github.com/leciric/agentbox/issues/224)) ([56d2192](https://github.com/leciric/agentbox/commit/56d219239132f8bc5caa472dee072a15af741d16))

## [0.12.0](https://github.com/leciric/agentbox/compare/v0.11.0...v0.12.0) (2026-10-07)


### Added

* a clearer top bar: Claude's 5-hour and weekly limits, and one status bubble for the VM ([#214](https://github.com/leciric/agentbox/issues/214)) ([ca3181b](https://github.com/leciric/agentbox/commit/ca3181bdd700602da596a8997baf3e35e8182cc1))
* a project's model picker warns when Settings enforces a model ([#202](https://github.com/leciric/agentbox/issues/202)) ([8a302db](https://github.com/leciric/agentbox/commit/8a302dbb965d8c0f1f242a6eb0bfad024b9cfe60))
* a speaker under the agent's replies reads one aloud again ([#189](https://github.com/leciric/agentbox/issues/189)) ([b72399b](https://github.com/leciric/agentbox/commit/b72399ba0706758e488eee8197f4a1ac21d6750d))
* a stopped or paused agent's chat stays readable, with Start on the composer ([#216](https://github.com/leciric/agentbox/issues/216)) ([8127022](https://github.com/leciric/agentbox/commit/8127022b063d9b573a0d52ad7bea128bfe337754))
* agentbox machines mcp gives Claude Code and Codex on your computer a desktop machine to test in ([#181](https://github.com/leciric/agentbox/issues/181)) ([4f2d2d0](https://github.com/leciric/agentbox/commit/4f2d2d0ed5b3a582e29f96b849fbdc66f8321d87))
* agentbox machines serve opens a local web page to browse every screenshot and recording ([#180](https://github.com/leciric/agentbox/issues/180)) ([a70d9d3](https://github.com/leciric/agentbox/commit/a70d9d34109f142f8970695bd4f626b8d826c32d))
* agentbox machines serve shows each machine's desktop live, with start, stop and take control ([#182](https://github.com/leciric/agentbox/issues/182)) ([501027b](https://github.com/leciric/agentbox/commit/501027b157d3fc8c730a631b7f23e31bab3b4b24))
* agents and chats carry on with a turn that an AgentBox restart cut short ([#198](https://github.com/leciric/agentbox/issues/198)) ([a6a3647](https://github.com/leciric/agentbox/commit/a6a36472bcb226a808e278d6344bac0ef8f0998d))
* agents checkpoint every turn, and can be rolled back or forked from any of them ([#185](https://github.com/leciric/agentbox/issues/185)) ([3a52003](https://github.com/leciric/agentbox/commit/3a52003135a2155a92fe9b35c121c16865bd279b))
* agents from every project share the VM's memory: a create that doesn't fit waits, and tests and builds take a share of it when they run ([#171](https://github.com/leciric/agentbox/issues/171)) ([f5290c6](https://github.com/leciric/agentbox/commit/f5290c63768967600e90d78dfe08763ed9a4345a))
* agents share one package cache, so dependencies and browsers are downloaded once ([#169](https://github.com/leciric/agentbox/issues/169)) ([d6b8821](https://github.com/leciric/agentbox/commit/d6b8821702db7f577f7a1f789bab18493f0ea0a3))
* desktop machines operate the app with a real pointer and keyboard, with the dock back on their desktop ([#183](https://github.com/leciric/agentbox/issues/183)) ([33a0750](https://github.com/leciric/agentbox/commit/33a0750ec3486d7473e9893619260171b939482d))
* downloads and updates come from downloads.agentbox.linting.dev, since GitHub's releases are private ([#206](https://github.com/leciric/agentbox/issues/206)) ([4a5a89a](https://github.com/leciric/agentbox/commit/4a5a89aca6215b6de15342abdccdc9c008d2f5f0))
* import a project's browser cookies straight from an installed browser ([#201](https://github.com/leciric/agentbox/issues/201)) ([1570e14](https://github.com/leciric/agentbox/commit/1570e1436a3f42e93748dc98f910227a1d6a7ea8))
* Media's project and agent filters are selects, so many agents no longer fill the view with chips ([#200](https://github.com/leciric/agentbox/issues/200)) ([39c3770](https://github.com/leciric/agentbox/commit/39c37707d6f5cc3d6d65e8b89f115fa17a2260d7))
* notifications open their agent and media, with a history bell and an all-projects Media view ([#195](https://github.com/leciric/agentbox/issues/195)) ([8ce0349](https://github.com/leciric/agentbox/commit/8ce0349efca35ba85c518324dc5ebf6b64325c30))
* on Windows, the top bar shows AgentBox's WSL distro and its memory, with Free resources ([#213](https://github.com/leciric/agentbox/issues/213)) ([baf3f1f](https://github.com/leciric/agentbox/commit/baf3f1fae4148fd9bdbb72a0f417d5a16fb4ad8d))
* projects can be named anything, with spaces, capitals or any character ([#172](https://github.com/leciric/agentbox/issues/172)) ([81d2611](https://github.com/leciric/agentbox/commit/81d2611479072b0255dd1914aacc65489ad2dead))
* SnapShots send a window to a chat as a bug report, and projects import browser cookies from an export ([#186](https://github.com/leciric/agentbox/issues/186)) ([cb34d13](https://github.com/leciric/agentbox/commit/cb34d1309f8a6d40333a81de2e4f76d568707f56))
* the app can be used in English or Brazilian Portuguese, chosen in Settings ([#197](https://github.com/leciric/agentbox/issues/197)) ([9d725be](https://github.com/leciric/agentbox/commit/9d725be409fccce68a4d009981435c8eb1c2b2b7))
* the AppImage downloads and installs updates itself, then restarts into them ([#199](https://github.com/leciric/agentbox/issues/199)) ([077aff4](https://github.com/leciric/agentbox/commit/077aff48f78b4b4d8e813b50adda926457ef256a))
* the top bar's Claude chip shows the time left in the 5-hour window ([#219](https://github.com/leciric/agentbox/issues/219)) ([359d991](https://github.com/leciric/agentbox/commit/359d99110ec7907ab559a3c84c7a08c4bd2cae8b))
* usage stats learn the models, effort and context windows in use, with anonymous turn, heartbeat, agent, error and setup events ([#220](https://github.com/leciric/agentbox/issues/220)) ([e985880](https://github.com/leciric/agentbox/commit/e985880719b731e57f4cd5426b3bb1beac1c4374))
* zoom and pan images in the Media viewer ([#208](https://github.com/leciric/agentbox/issues/208)) ([a1353f7](https://github.com/leciric/agentbox/commit/a1353f77d31d4ac0767c76314ef49644b53f3153))


### Fixed

* a fixed number of agents at once really caps them ([#207](https://github.com/leciric/agentbox/issues/207)) ([cb0aea2](https://github.com/leciric/agentbox/commit/cb0aea266f28648aa8185943e8b4ae12010383db))
* a VM made again, or a new WSL distro, stays the same install in the update check ([#218](https://github.com/leciric/agentbox/issues/218)) ([df8eca0](https://github.com/leciric/agentbox/commit/df8eca0e9f69cb8d92ad6217ae3fc59529ce2d4d))
* agent recordings are made in one pass and saved the moment they stop ([#188](https://github.com/leciric/agentbox/issues/188)) ([11d899c](https://github.com/leciric/agentbox/commit/11d899c3204ece30ec7f779ba148f7c9b1cc4296))
* AgentBox's caches and tools move to the VM's agents' disk, and caches are sized to fit it ([#196](https://github.com/leciric/agentbox/issues/196)) ([c6542a6](https://github.com/leciric/agentbox/commit/c6542a628da5639c29c96f7b40c2e36033dff5c5))
* agents and the project's chat follow up when the background work they watch ends ([#215](https://github.com/leciric/agentbox/issues/215)) ([6f58183](https://github.com/leciric/agentbox/commit/6f581838160a5b1dfe58dc3c1d2a5aee4bfdf8b6))
* agents no longer wait for memory the VM has free, and a queued agent can be started now ([#175](https://github.com/leciric/agentbox/issues/175)) ([82fdad2](https://github.com/leciric/agentbox/commit/82fdad20e9d8483eb744092e24ef835f4218b8dc))
* agents' turns end when you message them while their background subagents run ([#176](https://github.com/leciric/agentbox/issues/176)) ([06875d0](https://github.com/leciric/agentbox/commit/06875d007e426d276534c00d39165829a0908979))
* an agent's /t holds at most 2 GB, so temporary files can't fill the VM's memory ([#221](https://github.com/leciric/agentbox/issues/221)) ([eff0867](https://github.com/leciric/agentbox/commit/eff086766bbaf94ea02817daa32b7e9d3c9cc412))
* build the Arch package faster, with zstd ([#211](https://github.com/leciric/agentbox/issues/211)) ([64010c8](https://github.com/leciric/agentbox/commit/64010c8a6a824189dc036bc0246dc614496d63b8))
* Chromium no longer asks to restore pages after a machine stops ([#184](https://github.com/leciric/agentbox/issues/184)) ([dcb9d89](https://github.com/leciric/agentbox/commit/dcb9d897f2b90b6a92174a2d5b92008d36c4b805))
* media save says a screenshot must be in Media first ([#203](https://github.com/leciric/agentbox/issues/203)) ([922aadd](https://github.com/leciric/agentbox/commit/922aaddfa6a49e618fcb6c132017a0c8c18bc27b))
* new autonomous agents start in Bypass permissions again ([#191](https://github.com/leciric/agentbox/issues/191)) ([84e82b0](https://github.com/leciric/agentbox/commit/84e82b0b30fb4f76ebf0a611608eb38788899367))
* package-cache tests no longer leak the agent's environment ([#187](https://github.com/leciric/agentbox/issues/187)) ([3c90763](https://github.com/leciric/agentbox/commit/3c90763c20ba544a54160ab3cbf998f54cb53fad))
* queued agents start on the model chosen for them ([#193](https://github.com/leciric/agentbox/issues/193)) ([4ea1ae3](https://github.com/leciric/agentbox/commit/4ea1ae3162f9885ed2a7ca11f3f60a78250f83f4))
* recordings in Media play again when there are many of them ([#210](https://github.com/leciric/agentbox/issues/210)) ([9b664f5](https://github.com/leciric/agentbox/commit/9b664f56cd573238aab7dd446c13df6406d2db6b))
* Settings explain each option in plain words, and match what it does ([#209](https://github.com/leciric/agentbox/issues/209)) ([325e05c](https://github.com/leciric/agentbox/commit/325e05c82019ee0d05e11bbf3f292ce5bdcf2ba4))
* telling a stopped or paused agent starts its machine first, and waits for memory when the VM has none ([#173](https://github.com/leciric/agentbox/issues/173)) ([02ddf90](https://github.com/leciric/agentbox/commit/02ddf90653a82e759b5fd674b88e5d56dd89b8bb))
* the project chat has AgentBox's tools in a Mac VM made before 0.10 ([#192](https://github.com/leciric/agentbox/issues/192)) ([bfa1de4](https://github.com/leciric/agentbox/commit/bfa1de4fd2228bba593907ad7b31b8c1233cccd4))
* the project chat recovers when its repository was cloned again ([#190](https://github.com/leciric/agentbox/issues/190)) ([5565caa](https://github.com/leciric/agentbox/commit/5565caa51c849220c4cc11bc3b41d5cd47c34bb0))
* the WSL storage pool is sized from free disk and grows on re-run ([#194](https://github.com/leciric/agentbox/issues/194)) ([28abf18](https://github.com/leciric/agentbox/commit/28abf18d8748fac9d076207998592eb566589550))

## [0.11.0](https://github.com/leciric/agentbox/compare/v0.10.0...v0.11.0) (2026-10-02)


### Added

* a Main chat across every project, above the projects in the sidebar ([#153](https://github.com/leciric/agentbox/issues/153)) ([abd0759](https://github.com/leciric/agentbox/commit/abd07597488e3a3b1e00fda3895f780a768f4a3b))
* a task in the Tasks tab can go to the project's lead, which can split it across several agents ([#156](https://github.com/leciric/agentbox/issues/156)) ([3a4afcc](https://github.com/leciric/agentbox/commit/3a4afcc537462c14cb4e843bb9f4c8c243504212))
* AgentBox always runs in its VM on Linux, and installations from before are prompted to move ([#146](https://github.com/leciric/agentbox/issues/146)) ([370c47d](https://github.com/leciric/agentbox/commit/370c47d987ad592b91d2dde0affc58070be61587))
* AgentBox never fills your disk: it keeps a floor of free space, refuses new agents and pauses the busiest writers at it ([#148](https://github.com/leciric/agentbox/issues/148)) ([41865b4](https://github.com/leciric/agentbox/commit/41865b424b2abddc42cf14b4c495e3140f1ed2f2))
* agents share one Docker image cache, so a Docker Hub image is downloaded once ([#166](https://github.com/leciric/agentbox/issues/166)) ([7a88106](https://github.com/leciric/agentbox/commit/7a88106508a0a7bf538dca1a83e5b79efd754339))
* an agent queue with a running limit per project, and a lead that rechecks its agents ([#138](https://github.com/leciric/agentbox/issues/138)) ([fee4611](https://github.com/leciric/agentbox/commit/fee4611485d7149a6c95550cdcec69f1f2a60218))
* chat from your phone from anywhere, through a Cloudflare Tunnel ([#137](https://github.com/leciric/agentbox/issues/137)) ([3e8d8ab](https://github.com/leciric/agentbox/commit/3e8d8abb534b417a53090713cc694de8bd76c95c))
* connectors give agents remote MCP servers like Notion, signed in once on the host ([#133](https://github.com/leciric/agentbox/issues/133)) ([519584b](https://github.com/leciric/agentbox/commit/519584b92ea47e0e435cac5f7e767ca9b805cec3))
* make AgentBox's VM disk bigger from Settings or agentbox vm resize --disk, without a restart on Linux ([#161](https://github.com/leciric/agentbox/issues/161)) ([b58db1e](https://github.com/leciric/agentbox/commit/b58db1e4fafed88add9a34f718c5daa1f50a9788))
* nightly builds, and a Stable/Nightly update channel in Settings ([#136](https://github.com/leciric/agentbox/issues/136)) ([23a6c50](https://github.com/leciric/agentbox/commit/23a6c50a90306ff2577ac40320bbfac96e8a5458))
* report a problem from the app or agentbox report, and opt-in error reports ([#149](https://github.com/leciric/agentbox/issues/149)) ([c0465df](https://github.com/leciric/agentbox/commit/c0465df510dbab04d0c1915d60e1c21a052171a5))
* stopping an agent first frees its Docker space, its build cache and the images no container uses ([#165](https://github.com/leciric/agentbox/issues/165)) ([7a25c6c](https://github.com/leciric/agentbox/commit/7a25c6c8bd11cf92c5007bce5599da3316a40d68))
* swap for AgentBox's VM, a swapfile on its disk you turn on in Settings or with agentbox vm swap ([#162](https://github.com/leciric/agentbox/issues/162)) ([edcf220](https://github.com/leciric/agentbox/commit/edcf220b479459ae37d7a26059308d8ad0531606))
* Tasks tab splits into Open and Done lists, and a task is marked done when its agent's pull request merges ([#155](https://github.com/leciric/agentbox/issues/155)) ([96d80c8](https://github.com/leciric/agentbox/commit/96d80c815f7a4083ae7d764f0f5017431b9e007c))
* the top bar shows what AgentBox's VM really takes of your disk, of the most it can hold, with a breakdown ([#160](https://github.com/leciric/agentbox/issues/160)) ([3e3a1bf](https://github.com/leciric/agentbox/commit/3e3a1bf37b31d693bdc1e9f31775a0570732eadc))


### Fixed

* "Open with your default app" and "Show in folder" work on media again now that AgentBox runs in a VM ([#158](https://github.com/leciric/agentbox/issues/158)) ([a835726](https://github.com/leciric/agentbox/commit/a8357262b5bd7d9c1dc82232db95aa784f9dbd3d))
* a project's Tokens tab shows the limits of the Claude account the project uses, not the default account's ([#159](https://github.com/leciric/agentbox/issues/159)) ([27e1264](https://github.com/leciric/agentbox/commit/27e1264da145f3260f739b3d3404b16c6824cc69))
* AgentBox no longer hangs when Incus dies as the Linux VM starts ([#139](https://github.com/leciric/agentbox/issues/139)) ([e062346](https://github.com/leciric/agentbox/commit/e06234699a9c3dc4470e6aeebe1884eaf289b635))
* agents test and record with the real mouse instead of Playwright, and carry a third less instructions on every step ([#157](https://github.com/leciric/agentbox/issues/157)) ([aeaffaf](https://github.com/leciric/agentbox/commit/aeaffafa131a69f7368582d8e94f912dff223fb5))
* an agent started from stopped keeps its in-agent API socket ([#167](https://github.com/leciric/agentbox/issues/167)) ([1c36c28](https://github.com/leciric/agentbox/commit/1c36c28f8620848f2af466642dd9d36c107ebe75))
* logging in to Claude Code works when AgentBox runs in its Linux VM, and a pasted code is submitted ([#142](https://github.com/leciric/agentbox/issues/142)) ([a50d027](https://github.com/leciric/agentbox/commit/a50d02722c8aeb72bcf7e097ca4d4497a70a3db4))
* messages in a chat no longer disappear when a turn ends or the chat is read again ([#144](https://github.com/leciric/agentbox/issues/144)) ([7034fa1](https://github.com/leciric/agentbox/commit/7034fa1f43f90ef9afe362037b72939076b6559d))
* recordings in Media play again on Linux, with GPU voice transcription (Vulkan) now an opt-in setting ([#150](https://github.com/leciric/agentbox/issues/150)) ([d42a8e2](https://github.com/leciric/agentbox/commit/d42a8e278d8a9353eec090205f780b258402a2bf))
* recordings, and other large media, are no longer saved corrupted and play as black ([#134](https://github.com/leciric/agentbox/issues/134)) ([53c6ddd](https://github.com/leciric/agentbox/commit/53c6ddd6e5a5ed6c53c8195acdc181331baa8cec))
* restarting AgentBox no longer leaves several daemons running and the app without its socket ([#141](https://github.com/leciric/agentbox/issues/141)) ([b73f49a](https://github.com/leciric/agentbox/commit/b73f49aa4674b059baca33dbaf9120f9538ee07f))
* Settings shows the VM's disk size field again after an upgrade ([#168](https://github.com/leciric/agentbox/issues/168)) ([7512544](https://github.com/leciric/agentbox/commit/7512544457a50762d248937f5f9fd7b2a19064a2))
* the desktop app no longer shows "write EPIPE" error dialogs when the VM stops or starts ([#132](https://github.com/leciric/agentbox/issues/132)) ([0b5aee8](https://github.com/leciric/agentbox/commit/0b5aee805fd3047b680f45b06a2c2089f7499f32))
* the Tasks tab is a list only you manage, with a Backlog and a Queue ([#145](https://github.com/leciric/agentbox/issues/145)) ([b8080a2](https://github.com/leciric/agentbox/commit/b8080a2b79d9e90f059aa334b20a5a3047b53fd3))
* the top bar's disk meter shows what AgentBox's VM takes again after an upgrade, and reads like the CPU meter ([#163](https://github.com/leciric/agentbox/issues/163)) ([d9aba1d](https://github.com/leciric/agentbox/commit/d9aba1d137d9c3c2fe37dc97a041d27603c5ba54))
* the top bar's resource controls show only the VM's memory ([#147](https://github.com/leciric/agentbox/issues/147)) ([ac2e2a2](https://github.com/leciric/agentbox/commit/ac2e2a2963b68c98532207447858cabef5d012ff))


### Changed

* a project's page has five tabs, with Memory, Tokens, Secrets and Connectors in its Settings ([#164](https://github.com/leciric/agentbox/issues/164)) ([636c5de](https://github.com/leciric/agentbox/commit/636c5deab74cdd0a0e21e230345c4a8720e7a8cc))
* remove per-agent resource limits, the shared agent budget, "Never freeze my CPU" and "GPU for agents", now that agents always run in AgentBox's VM ([#154](https://github.com/leciric/agentbox/issues/154)) ([8778ad3](https://github.com/leciric/agentbox/commit/8778ad3f5614c1a2eec0c1e9d93a151efc462679))

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
