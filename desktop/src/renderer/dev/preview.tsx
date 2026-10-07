// A standing preview of the app's narrow fixed-width columns — the agent
// rail, the sidebar — under the content that breaks them: long paths, URLs,
// branch names, stack traces, code blocks, unbroken error strings, compressed
// JSON. Not part of the app; nothing here ships (vite.config.mts's build
// only bundles index.html and web.html, so `npm run build` never touches
// this file). Run it with `npm run preview` from desktop/, or drive it
// headlessly with scripts/preview.mjs.
//
// State comes from the URL, not clicks, so a scenario is a link both a
// person and scripts/preview.mjs can go straight to:
//   ?theme=light            the light appearance (default: dark)
//   ?lang=pt-BR             the app in another language (default: en-US), for
//                           strings longer than English's
//   ?folded=1               the rail folded to 56px (default: open)
//   ?open=agent-99          the named agent open, its row active (ref suffix only)
//   ?finished=1             the rail's Finished section open (default: closed,
//                           unless ?open names an agent in it)
//   ?chat=agent-12          agent-12's conversation in the middle, blocked on a
//                           credential request
//   ?chat=lead              the project's chat, with the credential requests
//                           its agents are waiting on at its end
//   ?vm=create|lima|vz      a Mac's first screen in the middle: the VM to set up,
//                           or Lima to install first, or the experimental vz
//                           driver's VM, made and not finished (no Lima needed)
//   ?vm=krunkit|nokrunkit   the VM to set up on Apple Silicon, with krunkit
//                           installed (it gives memory back), or without it
//   ?vm=resize              Settings' panel for the VM's CPUs and memory, whose
//                           resize streams made-up output
//   ?readAloud=1            read aloud on: replies' hover rows get their speaker
//   ?chat=compaction        a project chat's timeline with compaction cards,
//                           done, failed and running with a held message
//   ?wsl=create|nowsl       Windows' first screen before setup: the distro to
//                           make, or WSL to install first. The whole App, with
//                           no daemon to reach, as a first launch shows it
//   ?accounts=1             Settings' Claude Code accounts, with a rename the
//                           dev bridge answers (fixtures.ts)
//   ?avatars=working        every AI tool's avatar in one mood (working, asking,
//                           idle, sleeping, error), at the rail's 40px and
//                           blown up; ?avatars=all for every mood at once. The
//                           rail beside it has an agent in each mood too.
//   ?avatars=transition     the avatars walking working → asking → idle →
//                           sleeping → error, easing from one pose to the next;
//                           window.avatarMood('idle') stops the walk and sets one
//   ?defaults=1             Settings' Lead and Agents defaults, the model and
//                           the context window of each, which the dev bridge
//                           saves (fixtures.ts)
//   ?github=1               a project's GitHub account picker, on a project
//                           that limits its Claude Code accounts, beside
//                           Settings' GitHub accounts with a rename the dev
//                           bridge answers
//   ?setup=updating         Settings while the daemon updates the base image's
//                           agent tools in place: the image check updating,
//                           with its job's log (fixtures.ts)
//   ?settings=models        the whole Settings page on a set-up machine, open
//                           at a section (general, models, agents, resources,
//                           accounts, machine) or at a project's settings
//                           (project:<name>), against fixtures the dev bridge
//                           saves (fixtures.ts)
//   ?enforce=1              with ?settings=project:<name>: "Enforce this model" on, so the
//                           project's model picker warns that Settings' model wins
//   ?topbar=a|b|c           mock-ups of a redesigned top bar (topbarDesigns.tsx),
//                           every state with its popovers drawn open
//   ?usage=1                the top bar's usage meter against two Claude
//                           accounts: on Home (the default account), on a
//                           project that uses the other one, on a Claude
//                           agent, and on a Codex agent (no meter)
//   ?pulls=1                a project's pull requests list, with a long
//                           GitHub login on one row
//   ?tokens=1               a project's Settings → Tokens: headline (with average
//                           TPS), by agent and by model, and spend over time
//   ?tokens=1&account=work
//                           the same, its project and agents on the "work"
//                           Claude account: its limits card shows only that one
//   ?tokens=agent           agent-99's own "What it spent" card, on its
//                           Settings → AI tool
//   ?meters=cpu|disk|pool   the top bar's "Host CPU", or its disk meter in VM mode or host mode
//                           popover, against a CPU-capped agent, a paused
//                           one and a plain one —
//                           scenarios.json clicks the meter open before its
//                           shot, since state here comes from the URL alone
//   ?meters=disk-unmeasured the disk meter in VM mode before its images are
//                           measured: — / 120 GiB
//   ?meters=disklow         the "Disk low" pill, for the VM's agents' disk near
//                           its floor; click it for the popover and its advice
//   ?nightly=1              a nightly build, on the nightly channel with a newer
//                           nightly out: the sidebar's starry header and badge,
//                           and with ?settings=general the Update channel row
//                           and the About line (fixtures.ts)
//   ?loading=hold           the first launch before the daemon has answered:
//                           the sidebar, Home and the rail with no lists yet.
//                           ?loading=3000 answers them from the fixtures after
//                           3 s; ?loading=refetch loads them, then holds every
//                           refetch, the way a busy daemon does
//   ?io=1|stalling          Home and the rail with disk IO next to CPU and
//                           memory: a quiet host, or one stalling on
//                           disk and memory the way the user's desktop froze
//                           (io full 35%, memory full 19%); ?io=agent is
//                           agent-12's Overview, its disk IO beside its CPU
//   ?media=project|agent|all  a project's Media, 360 items across fourteen agents; all, the all-projects view
//                           with long names and unbroken notes, or agent-99's
//                           own Media tab, at the width of a narrow window
//   ?notify=bell|media|viewer|toast
//                           a mock of clickable notifications, the top bar's
//                           bell and its history, the all-projects Media view
//                           and the viewer a notification opens (notifications.tsx)
//   ?power=host|host-start  the top bar's resource controls in host mode:
//                           agents running (Free resources), or every one
//                           stopped by it, with Start to bring them back
//   ?power=running|paused|off|starting|stopping
//                           in VM mode, the VM's pill in that state beside
//                           them; scenarios.json clicks it open for its popover
//                           or clicks Free resources for its confirmation.
//                           Free resources runs start to finish against the
//                           dev bridge (fixtures.ts)
//   ?newagent=1             the New agent dialog, open on the project, for its
//                           Size picker and the rest of its form
//   &free=progress|done|partial|error
//                           Free resources' dialog part-way through stopping
//                           the agents, with what it freed, with one agent
//                           that wouldn't stop, or failed
//   ?linux=setup|nokvm      a Linux machine's first screen, before AgentBox's
//                           VM is made: the VM to set up, at the size picked,
//                           or what's missing without /dev/kvm
//   ?linux=move             a Linux machine set up to run agents itself before
//                           AgentBox ran in a VM on Linux: the prompt to move
//                           into the VM, whose button opens Settings' Setup at
//                           the move
//   ?chv=live|old|off       Settings' Resources on a Linux machine in VM mode:
//                           the Cloud Hypervisor VM's size, running with room
//                           to resize it live, started by an older AgentBox
//                           (a resize restarts it), or off
//   ?queue=busy             The agent queue (D.. the per-project slots): the
//                           rail and sidebar with agentbox at 4 of 4 slots,
//                           three queued below them, and organic — a second
//                           project sharing the same budget, learned at ~6
//                           GiB an agent against agentbox's own ~2 GiB —
//                           content with the one slot Auto leaves it
//   ?queue=alone            Only organic has work: Auto gives it two slots
//                           instead of one, with nobody to share the budget
//   ?queue=tasks            agentbox's Tasks tab: the slots strip, the
//                           composer and the plan, three tasks queued, and
//                           a Done list with each way a task ends; Done and
//                           Reopen work against the fixture
//   ?queue=demo             Starts like busy, then plays the queue draining
//                           on a timer (~4s a step) — a slot frees, the next
//                           queued agent starts, the rest move up — until
//                           it's empty, for a recording; the per-agent usage
//                           table follows the same stops and starts
//   ?queue=settings         agentbox's Settings → General, at the slots
//                           row: Auto's slot size and the running agents its
//                           peak is learned from, memory and CPU now and at
//                           their peak
//   ?queue=settings-fixed   The same, with agentbox pinned to 2 agents at once
//   ?queue=organic-alone    organic's Settings at the slots row, alone: two
//   ?queue=organic-busy     organic's Settings beside a busy agentbox: one
//   ?queue=off              Like busy, but with the installation's Agent
//                           queue switch off: Settings and New agent's Queue
//                           show disabled with a pointer, the Tasks tab's
//                           toggle is "Start" instead of "Queue" and its
//                           slots strip is gone, and the sidebar's three
//                           already-queued agents still show Queued #N
//   ?page=settings          the project's page itself, beside the sidebar,
//                           open at a tab or a section of its Settings tab
//                           (?page=tokens, ?page=general…); with ?open=agent-99,
//                           that agent's page (?page=secrets, ?page=machine…)
// See scenarios.json for the set scripts/preview.mjs captures.
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '../styles.css';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { lazy, Suspense, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import type { ConnectionState, HostSetupStatus } from '../../preload';
import type * as T from '../../shared/api';
import { Toaster } from 'sonner';
import type { View } from '../App';
import { AgentRail } from '../components/AgentRail';
import { HomeView } from '../components/HomeView';
import { DefaultContextWindow, DefaultModel } from '../components/NewAgentDefaults';
import { OverviewTab } from '../components/OverviewTab';
import { GitHubAccountPicker } from '../components/ProjectAccounts';
import { PullRequestsPanel } from '../components/PullRequestsPanel';
import { MediaTab } from '../components/MediaTab';
import { ProjectMediaPanel } from '../components/ProjectMediaPanel';
import { AllMediaView } from '../components/AllMediaView';
import { ProjectSettings } from '../components/ProjectSettings';
import { ProjectTasksPanel } from '../components/ProjectTasksPanel';
import { AgentTokensCard, TokensPanel } from '../components/TokensPanel';
import { ClaudeAccounts, GitHubAccounts, SettingsView } from '../components/SettingsView';
import { SettingsGroup } from '../components/ui/settings';
import { AgentAvatar, aiLabel } from '../components/state';
import type { Mood } from '../lib/agentStatus';
import { Sidebar } from '../components/Sidebar';
import { FreeResourcesDialog, type FreeRun } from '../components/ResourceControls';
import { NewAgentDialog } from '../components/NewAgentDialog';
import { TopBar } from '../components/TopBar';
import { TopbarDesigns } from './topbarDesigns';
import { TooltipProvider } from '../components/ui/tooltip';
import { VMSetup } from '../components/VMSetup';
import { MovePrompt } from '../components/RunInVM';
import { laterKey } from '../lib/vmMove';
import { VMSize } from '../components/VMSize';
import { connectEvents } from '../lib/events';
import { applyLanguage, useLanguage } from '../lib/i18n';
import { ChatTab } from '../components/chat/ChatTab';
import { Timeline } from '../components/chat/Timeline';
import { leadAgentFrom } from '../components/ProjectChatPanel';
import { api } from '../lib/api';
import type { AgentPlaceName, ProjectPlaceName } from '../lib/tabs';
import { agent12Chat, buildFixtures, compactionThread, freeRun, installDevBridge, PROJECT, pullRequests,  seedDefaults, seedAllMedia, seedMedia, seedImageUpdate, seedSettings, seedNightly, seedMeterUsage, seedVMDisk, seedPower, seedQueryClient, seedQueue, seedLinuxHost, seedLinuxVM } from './fixtures';

import { mockMedia, NotificationsPreview, seedNotifications } from './notifications';

installDevBridge();

const params = new URLSearchParams(location.search);
document.documentElement.dataset.appearance = params.get('theme') === 'light' ? 'light' : '';
applyLanguage(params.get('lang'));
localStorage.setItem('agentbox.rail.folded', params.get('folded') === '1' ? '1' : '0');
localStorage.setItem('agentbox.rail.finished', params.get('finished') === '1' ? '1' : '0');
localStorage.setItem('agentbox.voice.readAloud', JSON.stringify({ on: params.get('readAloud') === '1' }));
const openAgent = params.get('open'); // e.g. "agent-99"
const page = params.get('page'); // a tab or Settings section of the project's page, or of ?open's agent
const vm = params.get('vm');
const wsl = params.get('wsl');
const accounts = params.get('accounts') === '1';
const avatars = params.get('avatars');
const moods: Mood[] = ['working', 'asking', 'idle', 'sleeping', 'error'];
const moodState: Record<Mood, string> = { working: 'running', asking: 'running', idle: 'running', sleeping: 'stopped', error: 'incomplete' };
const defaults = params.get('defaults') === '1';
const github = params.get('github') === '1';
const usage = params.get('usage') === '1';
const topbar = params.get('topbar');
const pulls = params.get('pulls') === '1';
const notify = params.get('notify'); // 'bell' | 'media' | 'viewer' | 'toast' | null
const media = params.get('media'); // 'project' the project's Media, 'agent' agent-99's Media tab
const tokens = params.get('tokens'); // '1' the project's Tokens tab, 'agent' agent-99's own tokens card
const meters = params.get('meters'); // "cpu" | "disk" | "disk-unmeasured" | "pool" | null
const power = params.get('power');
const free = params.get('free');
const newAgent = params.get('newagent') === '1';
const loading = params.get('loading'); // 'hold' | 'refetch' | milliseconds | null
const io = params.get('io'); // "1" | "stalling" | "agent" | null
const imageUpdate = params.get('setup') === 'updating';
const settingsPage = params.get('settings'); // a section of Settings, or a project's name
const linuxHost = params.get('linux'); // 'setup' | 'nokvm' | 'move' | null
const chvSize = params.get('chv'); // 'live' | 'old' | 'off' | null
const queue = params.get('queue'); // 'busy' | 'alone' | 'tasks' | 'demo' | 'settings' | 'settings-fixed' | 'organic-alone' | 'organic-busy' | 'off' | null
// Whose Settings ?queue=settings and the organic ones show.
const queueSettingsProject = queue?.startsWith('organic-') ? 'organic' : PROJECT;
if (chvSize) localStorage.setItem('agentbox.settings.section', 'resources');
if (settingsPage) localStorage.setItem('agentbox.settings.section', settingsPage);

const windowsBeforeSetup: HostSetupStatus = {
  pkexec: null,
  user: 'leandro',
  running: false,
  resizing: false,
  vm: null,
  wsl:
    wsl === 'nowsl'
      ? { wsl: '', name: 'AgentBox', user: '', exists: false, problem: "WSL 2 isn't installed: run wsl --install --no-distribution" }
      : { wsl: '2.4.13.0', name: 'AgentBox', user: '', exists: false, problem: "AgentBox's WSL distro isn't set up: run agentbox wsl init" },
};

// windowsBeforeSetup makes the dev bridge a Windows machine on first launch:
// no distro, so no daemon, and every attempt at it failing the way the main
// process reports it. The whole App renders against it, not only the card, so
// the scenario shows the screen as it really is.
function windowsBeforeSetupBridge(): void {
  const bridge = (window as unknown as { agentbox: Record<string, unknown> }).agentbox;
  const error = "the AgentBox daemon isn't running";
  bridge.info = async () => ({ socket: '', version: 'preview', electron: '', packaged: false, platform: 'win32' });
  bridge.request = async () => {
    throw new Error(error);
  };
  bridge.connection = async () => ({ state: 'disconnected', error }) satisfies ConnectionState;
  bridge.onConnection = (fn: (state: ConnectionState) => void) => {
    const timer = setInterval(() => fn({ state: 'disconnected', error }), 1_000);
    return () => clearInterval(timer);
  };
  bridge.hostSetup = { status: async () => windowsBeforeSetup, run: async () => ({ restarted: false }), onOutput: () => () => {} };
}

// linuxBeforeSetup makes the dev bridge a Linux machine on first launch, the
// way windowsBeforeSetupBridge makes it Windows: AgentBox's VM not made yet,
// so no daemon.
function linuxBeforeSetupBridge(kvm: boolean): void {
  const bridge = (window as unknown as { agentbox: Record<string, unknown> }).agentbox;
  const error = "AgentBox's Linux VM isn't set up: run agentbox vm init";
  const status: HostSetupStatus = {
    pkexec: '/usr/bin/pkexec',
    user: 'leandro',
    running: false,
    resizing: false,
    vm: null,
    wsl: null,
    linux: { mode: 'vm', kvm, cores: 16, memory: 32 * 1024 ** 3, defaultCpus: 8, defaultMemoryCap: 24 * 1024 ** 3 },
    chv: { mode: 'vm', driver: 'cloud-hypervisor', name: 'agentbox', state: 'missing', since: '0001-01-01T00:00:00Z', problem: error } as T.VMStatus,
  };
  bridge.info = async () => ({ socket: '', version: 'preview', electron: '', packaged: false, platform: 'linux' });
  bridge.request = async () => {
    throw new Error(error);
  };
  bridge.connection = async () => ({ state: 'disconnected', error }) satisfies ConnectionState;
  bridge.onConnection = (fn: (state: ConnectionState) => void) => {
    const timer = setInterval(() => fn({ state: 'disconnected', error }), 1_000);
    return () => clearInterval(timer);
  };
  bridge.hostSetup = { status: async () => status, run: async () => ({ restarted: false }), onOutput: () => () => {} };
}

const chat = params.get('chat');
const fixtures = buildFixtures();
if (github) {
  // Two GitHub accounts, and a project that picked the second one while it
  // limits its Claude Code accounts to one called "work".
  const p = fixtures.projects.find((p) => p.name === PROJECT)!;
  p.claudeAccounts = ['work'];
  p.githubAccount = 'personal-account-with-a-long-name';
}
if (usage) {
  // Two accounts: the machine's default, and "work", which the project uses.
  fixtures.projects.find((p) => p.name === PROJECT)!.claudeAccount = 'work';
  fixtures.agents.find((a) => a.ref === `${PROJECT}/agent-99`)!.claudeAccount = 'work';
}
const chatAgent = chat ? fixtures.agents.find((a) => a.ref === `${PROJECT}/${chat}`) : undefined;

const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false, refetchOnWindowFocus: false } } });
seedQueryClient(queryClient, fixtures);
if (defaults) seedDefaults(queryClient);
if (pulls) queryClient.setQueryData(['pulls', PROJECT], pullRequests());
if (media === 'all') seedAllMedia(queryClient);
else if (media) seedMedia(queryClient);
if (notify) seedNotifications(queryClient, mockMedia());
if (imageUpdate) seedImageUpdate(queryClient);
if (settingsPage) seedSettings(queryClient);
if (params.get('enforce') === '1') { queryClient.setQueryData(['queue', PROJECT], { projects: [] }); seedDefaults(queryClient); queryClient.setQueryData<T.Settings>(['settings'], (s) => s && { ...s, enforceAgentDefaults: true }); }
if (params.get('nightly') === '1') seedNightly(queryClient);
if (chvSize) seedLinuxVM(queryClient, chvSize);
// ?page= shows whole pages, and their sections ask for what no other
// scenario seeds: no limits read yet, an empty queue, and no secrets or
// connectors.
if (page) {
  queryClient.setQueryData(['claudeLimits'], []);
  queryClient.setQueryData(['queue', PROJECT], { enabled: true, budget: 0, reserve: 0, projects: [], queued: [], reserved: 0 } satisfies T.QueueStatus);
  for (const target of [PROJECT, `${PROJECT}/${openAgent ?? 'agent-99'}`]) {
    queryClient.setQueryData(['secrets', target], []);
    queryClient.setQueryData(['connectors', target], []);
  }
}
if (linuxHost === 'move') {
  seedLinuxHost(queryClient, true);
  localStorage.removeItem(laterKey);
}
if (meters) {
  const GiB = 1024 ** 3;
  queryClient.setQueryData(['usage'], { host: { cpu: 62, cores: 8, memUsed: 15 * GiB, memTotal: 32 * GiB, poolUsed: 15.5 * GiB, poolTotal: 100 * GiB, diskRead: 0, diskWrite: 0 }, agents: [] });
  queryClient.setQueryData(['claudeLimits'], []);
  seedMeterUsage(queryClient);
  if (meters === 'disk' || meters === 'disk-unmeasured') seedVMDisk(queryClient, meters === 'disk-unmeasured');
  if (meters === 'disklow') {
    queryClient.setQueryData(['disk'], {
      level: 'low',
      paused: [],
      since: '',
      message: '',
      disks: [
        {
          label: "The agents' disk, the shared caches",
          free: 14.2 * GiB,
          total: 100 * GiB,
          floor: 10 * GiB,
          level: 'low',
          advice:
            "Make it bigger in Settings, AgentBox's Linux VM, VM size, or with `agentbox vm resize --disk <size>`. Destroy agents you're done with. `agentbox package-cache --clear` and `agentbox docker-cache --clear` empty the shared caches, which fill again as agents need them.",
        },
        { label: "The VM's system disk", path: '/home/lint.linux/.local/share/agentbox', free: 15.1 * GiB, total: 19.6 * GiB, floor: 1 * GiB, level: 'ok', advice: "It holds the VM's system and AgentBox's state, and can't be made bigger." },
        { label: 'Worktrees, media', path: '/home/lint/.local/share/agentbox/worktrees', free: 420 * GiB, total: 931 * GiB, floor: 10 * GiB, level: 'ok' },
      ],
    } satisfies T.DiskGuard);
  }
}

const queueSeed: Record<string, 'busy' | 'alone' | 'demo' | 'off'> = { tasks: 'busy', settings: 'busy', 'settings-fixed': 'busy', 'organic-alone': 'alone', 'organic-busy': 'busy' };
if (queue) seedQueue(queryClient, queueSeed[queue] ?? (queue as 'busy' | 'alone' | 'demo' | 'off'));
if (queue === 'settings-fixed') {
  queryClient.setQueryData<T.Project[]>(['projects'], (ps) => ps?.map((p) => (p.name === PROJECT ? { ...p, slots: 2 } : p)));
  queryClient.setQueryData<T.QueueStatus>(['queue', PROJECT], (q) => q && { ...q, projects: q.projects.map((p) => (p.project === PROJECT ? { ...p, slots: 2, pinned: 2 } : p)) });
}
if (queue) startNowBridge();
if (queue === 'tasks') tasksBridge();
if (settingsPage && !queue) settingsBridge();
if (page) pageBridge();
if (power) seedPower(queryClient, power);
const seededRun = power && free ? freeRun(queryClient, free) : undefined;

if (io) {
  const GiB = 1024 ** 3;
  const MiB = 1024 ** 2;
  const stalling = io !== '1';
  // What each running agent is doing: agent-12 re-reading what it had to
  // drop, the rest reading and writing a little.
  const disk: Record<string, [number, number]> = {
    'agent-12': stalling ? [118 * MiB, 4.2 * MiB] : [1.2 * MiB, 0.4 * MiB],
    'agent-96': [0.6 * MiB, 3.1 * MiB],
    'agent-99': [0, 96 * 1024],
  };
  queryClient.setQueryData(['usage'], {
    host: {
      cpu: stalling ? 48 : 21,
      cores: 16,
      memUsed: stalling ? 29.4 * GiB : 14 * GiB,
      memTotal: 31 * GiB,
      poolUsed: 180 * GiB,
      poolTotal: 400 * GiB,
      diskRead: stalling ? 142 * MiB : 2.1 * MiB,
      diskWrite: stalling ? 38 * MiB : 0.8 * MiB,
      pressure: stalling
        ? { ioSome: 61.2, ioFull: 35.4, memorySome: 27.8, memoryFull: 19.1, stalling: true }
        : { ioSome: 1.4, ioFull: 0.3, memorySome: 0, memoryFull: 0, stalling: false },
    },
    agents: fixtures.agents
      .filter((a) => a.state === 'running')
      .map((a, i) => {
        const [diskRead, diskWrite] = disk[a.name] ?? [0, 0];
        return { ref: a.ref, state: 'running', cpu: [142, 38, 9, 71][i % 4], memory: [6.1, 2.4, 0.9, 3.3][i % 4] * GiB, processes: 40 + i, diskRead, diskWrite };
      }),
  } satisfies T.Usage);
  queryClient.setQueryData(['claudeLimits'], []);
  queryClient.setQueryData(['diff', `${PROJECT}/agent-12`], '');
  queryClient.setQueryData(['agentEvents', PROJECT], []);
}

// ?account=work puts the project, and its Claude Code agents, on that account,
// as creating them under a project with its own account does, with both
// accounts' readings below.
const account = params.get('account');
if (usage || account) {
  const at = new Date(Date.now() - 12 * 60_000).toISOString();
  const resets = (hours: number) => new Date(Date.now() + hours * 3_600_000).toISOString();
  queryClient.setQueryData(['claudeLimits'], [
    { account: 'personal', default: true, at, status: 'allowed', windows: [
      { name: 'five_hour', label: '5-hour', utilization: 0.23, resetsAt: resets(3) },
      { name: 'seven_day', label: 'Weekly', utilization: 0.41, resetsAt: resets(80) },
    ] },
    { account: 'work', default: false, at, status: 'allowed_warning', windows: [
      { name: 'five_hour', label: '5-hour', utilization: 0.88, resetsAt: resets(0.8) },
      { name: 'seven_day', label: 'Weekly', utilization: 0.52, resetsAt: resets(40) },
    ] },
  ] satisfies T.ClaudeLimit[]);
}

if (account) {
  queryClient.setQueryData<T.Project[]>(['projects'], (ps) => ps?.map((p) => (p.name === PROJECT ? { ...p, claudeAccount: account } : p)));
  queryClient.setQueryData<T.Agent[]>(['agents'], (as) => as?.map((a) => (a.project === PROJECT && a.ai === 'claude' ? { ...a, claudeAccount: account } : a)));
}

// ?queue=alone has nothing left in agentbox: organic is the project worth
// looking at, so the rail follows it instead of the default project.
const view: View = openAgent ? { kind: 'agent', ref: `${PROJECT}/${openAgent}` } : { kind: 'project', project: queue === 'alone' ? 'organic' : PROJECT };

// UsagePreview stands the top bar up once per kind of view, so each one's
// meter can be compared, and hovered for its tooltip.
function UsagePreview() {
  const views: [string, View][] = [
    ['Home', { kind: 'home' }],
    [`Project ${PROJECT}, on "work"`, { kind: 'project', project: PROJECT }],
    ['Claude agent-99, on "work"', { kind: 'agent', ref: `${PROJECT}/agent-99` }],
    ['Codex agent-96: nothing known, no meter', { kind: 'agent', ref: `${PROJECT}/agent-96` }],
  ];
  return (
    <div style={{ minHeight: '100vh', background: 'var(--color-ink)', font: '13px var(--font-sans)' }}>
      {views.map(([label, v]) => (
        <section key={label} data-usage-view={v.kind === 'agent' ? v.ref.split('/')[1] : v.kind}>
          <h3 style={{ padding: '14px 20px 4px', color: 'var(--ab-subtle)', textTransform: 'uppercase', letterSpacing: '0.08em', fontSize: 11 }}>{label}</h3>
          <TopBar view={v} onSelect={() => {}} onOpenNav={() => {}} onNewAgent={() => {}} />
        </section>
      ))}
    </div>
  );
}

// SeededFreeRun is Free resources' dialog held in one phase (?free=), which
// the top bar only reaches by running one: its agents as the phase left them.
function SeededFreeRun({ run }: { run: FreeRun }) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  return <FreeResourcesDialog open onOpenChange={() => {}} run={run} agents={agents.data ?? []} onConfirm={() => {}} onStartAgain={() => {}} starting={false} />;
}

// AvatarRow is one mood's row of avatars: each AI tool at the rail's 40px
// and blown up.
function AvatarRow({ mood, big = true }: { mood: Mood; big?: boolean }) {
  return (
    <div style={{ display: 'flex', alignItems: 'end', gap: 28 }}>
      {['claude', 'codex', 'opencode', 'none'].map((ai) => (
        <figure key={ai} style={{ display: 'grid', justifyItems: 'center', gap: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
            <AgentAvatar ai={ai} mood={mood} state={moodState[mood]} seed={`${ai}-${mood}`} />
            {big && <AgentAvatar ai={ai} mood={mood} state={moodState[mood]} seed={`${ai}-${mood}`} className="size-28 rounded-3xl" />}
          </div>
          <figcaption style={{ fontSize: 11.5 }}>{aiLabel(ai)}</figcaption>
        </figure>
      ))}
    </div>
  );
}

// AvatarTransition walks the moods on a timer, to watch the poses ease from
// one to the next. window.avatarMood(mood) takes over from the timer, so a
// script can set a mood and step the transition it starts.
function AvatarTransition() {
  const [mood, setMood] = useState<Mood>('working');
  const [walking, setWalking] = useState(true);
  useEffect(() => {
    (window as unknown as { avatarMood: (m: Mood) => void }).avatarMood = (m) => {
      setWalking(false);
      setMood(m);
    };
  }, []);
  useEffect(() => {
    if (!walking) return;
    const timer = setTimeout(() => setMood(moods[(moods.indexOf(mood) + 1) % moods.length]), 2500);
    return () => clearTimeout(timer);
  }, [mood, walking]);
  return (
    <section data-avatars="transition">
      <h3 style={{ marginBottom: 12, color: 'var(--ab-subtle)', textTransform: 'uppercase', letterSpacing: '0.08em', fontSize: 11 }}>{mood}</h3>
      <AvatarRow mood={mood} />
    </section>
  );
}

// PagePreview is a project's page, or an agent's, as the app shows it beside
// the sidebar (?page=), open where the URL says and clickable from there. Both
// load once the dev bridge is in: the agent's desktop and terminal reach for
// window.agentbox as their modules load.
const ProjectView = lazy(() => import('../components/ProjectView').then((m) => ({ default: m.ProjectView })));
const AgentView = lazy(() => import('../components/AgentView').then((m) => ({ default: m.AgentView })));

function PagePreview({ at }: { at: string }) {
  const [agentAt, setAgentAt] = useState(at as AgentPlaceName);
  return (
    <div style={{ display: 'flex', height: '100vh', width: '100vw' }}>
      <Sidebar view={view} onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      <div style={{ flex: 1, minWidth: 0, background: 'var(--color-ink)' }} data-preview-page={at}>
        <Suspense>
          {openAgent ? (
            <AgentView agentRef={`${PROJECT}/${openAgent}`} tab={agentAt} onTab={setAgentAt} onSelect={() => {}} />
          ) : (
            <ProjectView name={PROJECT} tab={at as ProjectPlaceName} onSelect={() => {}} onNewAgent={() => {}} />
          )}
        </Suspense>
      </div>
    </div>
  );
}

function Preview() {
  if (topbar) return <TopbarDesigns option={topbar} />;
  if (github) return <GitHubPreview />;
  if (page) return <PagePreview at={page} />;
  if (usage) return <UsagePreview />;
  if (notify) return <NotificationsPreview scenario={notify} />;
  if (newAgent) return <NewAgentDialog project={PROJECT} onClose={() => {}} onCreated={() => {}} />;

  if (power) {
    return (
      <div style={{ minHeight: '100vh', background: 'var(--color-ink)', font: '13px var(--font-sans)' }} data-preview-power={power}>
        <TopBar view={{ kind: 'home' }} onSelect={() => {}} onOpenNav={() => {}} onNewAgent={() => {}} />
        {seededRun && <SeededFreeRun run={seededRun} />}
      </div>
    );
  }

  if (meters) {
    return (
      <div style={{ minHeight: '100vh', background: 'var(--color-ink)', font: '13px var(--font-sans)' }}>
        <TopBar view={{ kind: 'home' }} onSelect={() => {}} onOpenNav={() => {}} onNewAgent={() => {}} />
      </div>
    );
  }

  if (pulls) {
    return (
      <div style={{ maxWidth: 420, padding: 24, font: '13px var(--font-sans)' }}>
        <PullRequestsPanel project={PROJECT} onSelect={() => {}} onOpenAccount={() => {}} />
      </div>
    );
  }

  if (queue === 'tasks') {
    return (
      <div style={{ padding: 24, font: '13px var(--font-sans)' }}>
        <ProjectTasksPanel project={PROJECT} onSelect={() => {}} onOpenChat={() => {}} />
      </div>
    );
  }

  if (queue === 'settings' || queue === 'settings-fixed' || queue === 'organic-alone' || queue === 'organic-busy') {
    return (
      <div style={{ maxWidth: 720, padding: 24, font: '13px var(--font-sans)' }}>
        <QueueSettingsPreview />
      </div>
    );
  }

  if (media === 'project') {
    return (
      <div style={{ padding: 24, font: '13px var(--font-sans)' }}>
        <ProjectMediaPanel project={PROJECT} />
      </div>
    );
  }

  if (media === 'all') {
    return (
      <div style={{ height: '100vh', font: '13px var(--font-sans)' }}>
        <AllMediaView onSelect={() => {}} />
      </div>
    );
  }

  if (media === 'agent') {
    return (
      <div style={{ height: '100vh', font: '13px var(--font-sans)' }}>
        <MediaTab agent={fixtures.agents.find((a) => a.ref === `${PROJECT}/agent-99`)!} />
      </div>
    );
  }

  if (tokens === '1') {
    return (
      <div style={{ maxWidth: 960, padding: 24, font: '13px var(--font-sans)' }}>
        <TokensPanel project={PROJECT} onOpenAgent={() => {}} />
      </div>
    );
  }

  if (tokens === 'agent') {
    return (
      <div style={{ maxWidth: 640, padding: 24, font: '13px var(--font-sans)' }}>
        <AgentTokensCard agent={fixtures.agents.find((a) => a.ref === `${PROJECT}/agent-99`)!} />
      </div>
    );
  }

  if (io) {
    const agent = fixtures.agents.find((a) => a.ref === `${PROJECT}/agent-12`)!;
    return (
      <div data-preview-io style={{ display: 'flex', flexDirection: 'column', height: '100vh', width: '100vw', background: 'var(--color-ink)' }}>
        <TopBar view={io === 'agent' ? { kind: 'agent', ref: agent.ref } : { kind: 'home' }} onSelect={() => {}} onOpenNav={() => {}} onNewAgent={() => {}} />
        <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            {io === 'agent' ? <OverviewTab agent={agent} /> : <HomeView onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />}
          </div>
          <AgentRail view={io === 'agent' ? { kind: 'agent', ref: agent.ref } : { kind: 'home' }} onSelect={() => {}} onNewAgent={() => {}} />
        </div>
      </div>
    );
  }

  if (linuxHost === 'move') return <LinuxHomePreview />;

  if (imageUpdate || settingsPage || chvSize) {
    return (
      <div style={{ height: '100vh' }}>
        <SettingsView />
      </div>
    );
  }

  if (defaults) {
    return (
      <div style={{ maxWidth: 720, padding: 24, font: '13px var(--font-sans)' }} className="grid gap-3">
        <SettingsGroup title="Lead">
          <DefaultModel role="lead" />
          <DefaultContextWindow role="lead" />
        </SettingsGroup>
        <SettingsGroup title="New agents">
          <DefaultModel role="agents" />
          <DefaultContextWindow role="agents" />
        </SettingsGroup>
      </div>
    );
  }

  if (accounts) {
    // Alone: a rename refetches projects and agents, which the dev bridge
    // answers with nothing, so the sidebar and rail would have none to show.
    return (
      <div style={{ maxWidth: 720, padding: 24, font: '13px var(--font-sans)' }}>
        <ClaudeAccounts
          accounts={[
            { name: 'default', default: true, savedAt: '2026-08-01T10:00:00Z', valid: 'valid' },
            { name: 'work', default: false, savedAt: '2026-09-12T10:00:00Z' },
          ]}
        />
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', height: '100vh', width: '100vw' }}>
      <Sidebar view={view} onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      <div style={{ flex: 1, minWidth: 0, background: 'var(--color-ink)', color: 'var(--color-zinc-600)', padding: 24, font: '13px var(--font-sans)' }}>
        {avatars === 'transition' ? (
          <AvatarTransition />
        ) : avatars ? (
          <div style={{ display: 'grid', gap: 28 }}>
            {moods
              .filter((m) => avatars === 'all' || avatars === m)
              .map((m) => (
                <section key={m} data-avatars={m}>
                  <h3 style={{ marginBottom: 12, color: 'var(--ab-subtle)', textTransform: 'uppercase', letterSpacing: '0.08em', fontSize: 11 }}>{m}</h3>
                  <AvatarRow mood={m} big={avatars !== 'all'} />
                </section>
              ))}
          </div>
        ) : chat === 'compaction' ? (
          <div style={{ maxWidth: 720 }}>
            <Timeline agent={{ ...fixtures.agents[0], ref: `${PROJECT}/lead`, name: 'lead' }} thread={compactionThread()} />
          </div>
        ) : chat === 'lead' ? (
          <div style={{ height: '100%', margin: -24 }}>
            <ChatTab
              agent={leadAgentFrom(fixtures.projects.find((p) => p.name === PROJECT)!, { ref: `${PROJECT}/lead`, started: true } as T.ProjectChat)}
              starting={false}
              autoStart={false}
              onStart={() => {}}
            />
          </div>
        ) : chatAgent ? (
          <div className="mx-auto max-w-3xl px-2 pt-2">
            <Timeline agent={chatAgent} thread={agent12Chat()} />
          </div>
        ) : vm === 'resize' ? (
          <div style={{ maxWidth: 720 }}>
            <VMSize
              busy={false}
              vm={{
                lima: '/opt/homebrew/bin/limactl',
                name: 'agentbox',
                exists: true,
                status: 'Running',
                cpus: 4,
                memory: 8 * 1024 ** 3,
                disk: 100 * 1024 ** 3,
                limits: { minCpus: 2, maxCpus: 10, minMemory: 4 * 1024 ** 3, maxMemory: 14 * 1024 ** 3 },
              }}
            />
          </div>
        ) : vm ? (
          <VMSetup
            vm={
              vm === 'lima'
                ? { lima: '', name: 'agentbox', exists: false, problem: "AgentBox runs in a Linux VM made with Lima, which isn't installed: brew install lima" }
                : vm === 'vz'
                  ? { driver: 'vz', lima: '', name: 'agentbox', exists: true, status: 'Stopped', cpus: 4, memory: 8 * 1024 ** 3 }
                  : {
                      lima: '/opt/homebrew/bin/limactl',
                      name: 'agentbox',
                      exists: false,
                      problem: "AgentBox's Linux VM isn't set up: run agentbox vm init",
                      krunkit: vm === 'krunkit' ? { available: true } : vm === 'nokrunkit' ? { available: false, missing: 'krunkit' } : undefined,
                    }
            }
          />
        ) : (
          'A standing preview of the rail and sidebar under wide content — see the comment at the top of preview.tsx for the URL params that drive it.'
        )}
      </div>
      <AgentRail view={view} onSelect={() => {}} onNewAgent={() => {}} />
    </div>
  );
}

// QueueSettingsPreview is agentbox's Settings → General (?queue=settings):
// the same ProjectSettings the real Settings tab renders, against whatever
// seedQueue put in the projects query.
function QueueSettingsPreview() {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const project = projects.data?.find((p) => p.name === queueSettingsProject);
  return project ? <ProjectSettings project={project} /> : null;
}

// GitHubPreview reads the project and the accounts through their queries, so
// a pick or a rename shows what the dev bridge answered once they refetch.
function GitHubPreview() {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const project = projects.data?.find((p) => p.name === PROJECT);
  return (
    <div style={{ maxWidth: 720, padding: 24, font: '13px var(--font-sans)', display: 'grid', gap: 24 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
        <span className="text-[13px] text-muted">GitHub account</span>
        {project && <GitHubAccountPicker project={project} />}
      </div>
      <GitHubAccounts accounts={auth.data?.githubAccounts ?? []} />
    </div>
  );
}

// startNowBridge answers a queued agent's "Start now": it leaves the queue
// and starts initializing, the rest moving up a place. The agents and the
// queue are read back from the cache, so the refresh after it shows that.
function startNowBridge(): void {
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const answer = (value: unknown) => ({ status: 200, body: JSON.stringify(value), contentType: 'application/json' });
  bridge.request = async (method, path, body) => {
    if (method === 'GET' && path.startsWith('/v1/queue')) return answer(queryClient.getQueryData(['queue', PROJECT]));
    if (method === 'GET' && path === '/v1/agents') return answer(queryClient.getQueryData(['agents']));
    const start = method === 'POST' ? /^\/v1\/queue\/([^/]+)\/([^/]+)\/start$/.exec(path) : null;
    if (start) {
      const ref = `${decodeURIComponent(start[1])}/${decodeURIComponent(start[2])}`;
      const status = queryClient.getQueryData<T.QueueStatus>(['queue', PROJECT]);
      if (status) {
        const queued = status.queued.filter((q) => q.ref !== ref).map((q, i) => ({ ...q, position: i + 1 }));
        queryClient.setQueryData(['queue', PROJECT], { ...status, queued });
      }
      const agents = queryClient.getQueryData<T.Agent[]>(['agents']) ?? [];
      let position = 0;
      const next = agents.map((a) => {
        if (a.ref === ref) return { ...a, state: 'initializing', queuePosition: undefined, waiting: undefined };
        if (a.state === 'queued' && a.project === PROJECT) return { ...a, queuePosition: ++position };
        return a;
      });
      queryClient.setQueryData(['agents'], next);
      return { status: 204, body: '', contentType: 'application/json' };
    }
    return inner(method, path, body);
  };
}

// settingsBridge answers what Settings polls and the dev bridge would answer
// with a bare {}: a project's queue (its "Agents at once" row reads the
// projects in it) and the phone's LAN status, off and unpaired.
function settingsBridge(): void {
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const answer = (value: unknown) => ({ status: 200, body: JSON.stringify(value), contentType: 'application/json' });
  const queue: T.QueueStatus = { enabled: false, budget: 0, reserve: 0, projects: [], queued: [], reserved: 0 };
  const lan: T.LANStatus = { enabled: false, port: 7780, listening: false, urls: [], tunnel: { enabled: false, named: false, state: 'off', origin: 'http://localhost:7780' }, phones: [] };
  bridge.request = async (method, path, body) => {
    if (method === 'GET' && path.startsWith('/v1/queue')) return answer(queryClient.getQueryData(['queue', PROJECT]) ?? queue);
    if (method === 'GET' && path === '/v1/lan') return answer(lan);
    return inner(method, path, body);
  };
}

// tasksBridge answers the Tasks tab from what seedQueue put in the cache, so
// its 5s queue refetch and the refresh after each action read the fixtures
// back rather than the dev bridge's generic {}; marking a task done and
// reopening it change the fixture, so both can be clicked through.
function tasksBridge(): void {
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const answer = (value: unknown) => ({ status: 200, body: JSON.stringify(value), contentType: 'application/json' });
  const tasksPath = `/v1/projects/${PROJECT}/memory/tasks`;
  bridge.request = async (method, path, body) => {
    const tasks = queryClient.getQueryData<T.Task[]>(['memoryTasks', PROJECT]) ?? [];
    if (method === 'GET' && path.startsWith('/v1/queue')) return answer(queryClient.getQueryData(['queue', PROJECT]));
    if (method === 'GET' && path === '/v1/agents') return answer(queryClient.getQueryData(['agents']));
    if (method === 'GET' && path === '/v1/settings') return answer(queryClient.getQueryData(['settings']));
    if (method === 'GET' && path === tasksPath) return answer(tasks);
    if (method === 'PATCH' && path.startsWith(`${tasksPath}/`)) {
      const id = decodeURIComponent(path.slice(tasksPath.length + 1));
      const req = (typeof body === 'string' ? JSON.parse(body) : body) as T.UpdateTaskRequest;
      const now = new Date().toISOString();
      const next = tasks.map((t) => {
        if (t.id !== id) return t;
        const status = req.status ?? t.status;
        const closed = status === 'done' || status === 'abandoned';
        return { ...t, status, agent: req.agent ?? t.agent, updatedAt: now, closedAt: closed ? now : undefined, pullUrl: closed ? t.pullUrl : undefined, pullNumber: closed ? t.pullNumber : undefined };
      });
      queryClient.setQueryData(['memoryTasks', PROJECT], next);
      return answer(next.find((t) => t.id === id));
    }
    return inner(method, path, body);
  };
}

// pageBridge answers what a page's sections read and no fixture has: a
// project's memory lists, with none, and an agent's diff, rather than the dev
// bridge's generic {}.
function pageBridge(): void {
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const lists = /^\/v1\/projects\/[^/]+\/memory\/(memories|events|artifacts|reports|duplicates|tasks)(\?|$)/;
  bridge.request = async (method, path, body) => {
    if (method === 'GET' && lists.test(path)) return { status: 200, body: '[]', contentType: 'application/json' };
    if (method === 'GET' && path.endsWith('/diff?stat=true')) return { status: 200, body: JSON.stringify(' desktop/src/renderer/lib/tabs.ts | 46 ++++++\n 1 file changed, 46 insertions(+)'), contentType: 'application/json' };
    return inner(method, path, body);
  };
}

// slowListsBridge answers the lists the sidebar, Home and the rail read from
// the fixtures, but only once `hold` has passed: never, for null. With
// `refetches`, the first answer is at once and it's every later one that
// waits, the way a daemon busy destroying an agent answers.
function slowListsBridge(hold: number | null, refetches = false): void {
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown> };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const lists: Record<string, unknown> = {
    '/v1/agents': fixtures.agents,
    '/v1/projects': fixtures.projects,
    '/v1/sections': fixtures.sections,
    '/v1/jobs': [],
    '/v1/setup': { ready: true },
    '/v1/update': {},
    '/v1/usage?interval=500ms': { host: { cpu: 12, cores: 16, memUsed: 9 * 1024 ** 3, memTotal: 31 * 1024 ** 3, poolUsed: 0, poolTotal: 0, diskRead: 0, diskWrite: 0 }, agents: [] },
    [`/v1/projects/${PROJECT}/fleet`]: fixtures.fleet,
    [`/v1/projects/${PROJECT}/agent-events`]: fixtures.events,
    [`/v1/projects/${PROJECT}/questions?all=1`]: fixtures.questions,
    [`/v1/projects/${PROJECT}/lead`]: { project: PROJECT, ref: `${PROJECT}/lead`, started: true, chat: 'idle' },
  };
  const answered = new Set<string>();
  bridge.request = async (method, path, body) => {
    if (method !== 'GET' || !(path in lists)) return inner(method, path, body);
    if (!refetches || answered.has(path)) await new Promise((resolve) => hold !== null && setTimeout(resolve, hold));
    answered.add(path);
    return { status: 200, body: JSON.stringify(lists[path]), contentType: 'application/json' };
  };
  // What the app does when an agent is removed, as the daemon announces it.
  if (refetches) (window as unknown as { refetchLists: () => void }).refetchLists = () => void loadingClient.invalidateQueries();
}

// Home on a Linux machine that runs agents itself, and the Settings its
// suggestion opens.
function LinuxHomePreview() {
  const [view, setView] = useState<View>({ kind: 'home' });
  return (
    <div style={{ display: 'flex', height: '100vh', width: '100vw', background: 'var(--color-ink)' }} data-preview-linux={view.kind}>
      <Sidebar view={view} onSelect={setView} onAddProject={() => {}} onNewAgent={() => {}} />
      <div className="min-w-0 flex-1">
        {view.kind === 'settings' ? <SettingsView /> : <HomeView onSelect={setView} onAddProject={() => {}} onNewAgent={() => {}} />}
      </div>
      <MovePrompt version="preview" onOpen={() => setView({ kind: 'settings' })} />
    </div>
  );
}

// The sidebar, Home and the rail side by side, against a client nothing is
// seeded into, so each reads what it shows through its own query.
const loadingClient = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
function LoadingPreview() {
  return (
    <div style={{ display: 'flex', height: '100vh', width: '100vw' }}>
      <Sidebar view={{ kind: 'home' }} onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      <div className="min-w-0 flex-1 overflow-y-auto">
        <HomeView onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      </div>
      <AgentRail view={{ kind: 'project', project: PROJECT }} onSelect={() => {}} onNewAgent={() => {}} />
    </div>
  );
}

// Languaged draws the preview again when Settings changes the language, as the
// app's Root does.
function Languaged() {
  useLanguage();
  const language = useQuery({ queryKey: ['settings'], queryFn: api.settings }).data?.language;
  useEffect(() => {
    if (language) applyLanguage(language);
  }, [language]);
  return <Preview />;
}

if (loading) {
  slowListsBridge(loading === 'hold' ? null : loading === 'refetch' ? null : Number(loading), loading === 'refetch');
  createRoot(document.getElementById('root')!).render(
    <QueryClientProvider client={loadingClient}>
      <TooltipProvider delayDuration={250}>
        <LoadingPreview />
      </TooltipProvider>
    </QueryClientProvider>,
  );
} else if (wsl || linuxHost === 'setup' || linuxHost === 'nokvm') {
  if (wsl) windowsBeforeSetupBridge();
  else linuxBeforeSetupBridge(linuxHost === 'setup');
  // After the bridge: some of what App imports reaches for it as it loads.
  const { App } = await import('../App');
  const appClient = new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5_000 } } });
  connectEvents(appClient);
  createRoot(document.getElementById('root')!).render(
    <QueryClientProvider client={appClient}>
      <TooltipProvider delayDuration={250}>
        <App />
      </TooltipProvider>
    </QueryClientProvider>,
  );
} else createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <TooltipProvider delayDuration={250}>
      <Languaged />
      <Toaster position="bottom-right" />
    </TooltipProvider>
  </QueryClientProvider>,
);
