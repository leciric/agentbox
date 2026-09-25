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
//   ?folded=1               the rail folded to 56px (default: open)
//   ?open=agent-99          the named agent open, its row active (ref suffix only)
//   ?finished=1             the rail's Finished section open (default: closed,
//                           unless ?open names an agent in it)
//   ?chat=agent-12          agent-12's conversation in the middle, blocked on a
//                           credential request
//   ?chat=lead              the project's chat, with the credential requests
//                           its agents are waiting on at its end
//   ?vm=create|lima         a Mac's first screen in the middle: the VM to set up,
//                           or Lima to install first
//   ?vm=resize              Settings' panel for the VM's CPUs and memory, whose
//                           resize streams made-up output
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
//   ?resources=1            the resource limits' copy: Home's host stats,
//                           Settings' defaults for new agents, and an agent's
//                           limits editor, open
//   ?usage=1                the top bar's usage meter against two Claude
//                           accounts: on Home (the default account), on a
//                           project that uses the other one, on a Claude
//                           agent, and on a Codex agent (no meter)
//   ?pulls=1                a project's pull requests list, with a long
//                           GitHub login on one row
// See scenarios.json for the set scripts/preview.mjs captures.
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '../styles.css';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import type { ConnectionState, HostSetupStatus } from '../../preload';
import type * as T from '../../shared/api';
import { Toaster } from 'sonner';
import type { View } from '../App';
import { AgentRail } from '../components/AgentRail';
import { HomeView } from '../components/HomeView';
import { DefaultContextWindow, DefaultModel, NewAgentResources } from '../components/NewAgentDefaults';
import { LimitsEditor } from '../components/OverviewTab';
import { GitHubAccountPicker } from '../components/ProjectView';
import { PullRequestsPanel } from '../components/PullRequestsPanel';
import { ClaudeAccounts, GitHubAccounts, SettingsView } from '../components/SettingsView';
import { AgentAvatar, aiLabel } from '../components/state';
import { Panel } from '../components/ui/card';
import type { Mood } from '../lib/agentStatus';
import { Sidebar } from '../components/Sidebar';
import { TopBar } from '../components/TopBar';
import { TooltipProvider } from '../components/ui/tooltip';
import { VMSetup } from '../components/VMSetup';
import { VMSize } from '../components/VMSize';
import { connectEvents } from '../lib/events';
import { ChatTab } from '../components/chat/ChatTab';
import { Timeline } from '../components/chat/Timeline';
import { leadAgentFrom } from '../components/ProjectChatPanel';
import { api } from '../lib/api';
import { agent12Chat, buildFixtures, compactionThread, installDevBridge, PROJECT, pullRequests, seedDefaults, seedImageUpdate, seedQueryClient } from './fixtures';

installDevBridge();

const params = new URLSearchParams(location.search);
document.documentElement.dataset.appearance = params.get('theme') === 'light' ? 'light' : '';
localStorage.setItem('agentbox.rail.folded', params.get('folded') === '1' ? '1' : '0');
localStorage.setItem('agentbox.rail.finished', params.get('finished') === '1' ? '1' : '0');
const openAgent = params.get('open'); // e.g. "agent-99"
const vm = params.get('vm');
const wsl = params.get('wsl');
const accounts = params.get('accounts') === '1';
const avatars = params.get('avatars');
const moods: Mood[] = ['working', 'asking', 'idle', 'sleeping', 'error'];
const moodState: Record<Mood, string> = { working: 'running', asking: 'running', idle: 'running', sleeping: 'stopped', error: 'incomplete' };
const defaults = params.get('defaults') === '1';
const github = params.get('github') === '1';
const resources = params.get('resources') === '1';
const usage = params.get('usage') === '1';
const pulls = params.get('pulls') === '1';
const imageUpdate = params.get('setup') === 'updating';

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
if (imageUpdate) seedImageUpdate(queryClient);
if (resources) {
  const GiB = 1024 ** 3;
  queryClient.setQueryData(['usage'], { host: { cpu: 62, cores: 8, memUsed: 19 * GiB, memTotal: 31 * GiB, poolUsed: 120 * GiB, poolTotal: 400 * GiB }, agents: [] });
  queryClient.setQueryData(['settings'], { ...(queryClient.getQueryData(['settings']) ?? {}), hostCores: 8, hostMemory: 31 * GiB, seedMemory: '8GiB', defaultCPU: '4', defaultCPUAllowance: '', defaultMemory: '8GiB' });
}

if (usage) {
  const at = new Date(Date.now() - 12 * 60_000).toISOString();
  const resets = (hours: number) => new Date(Date.now() + hours * 3_600_000).toISOString();
  queryClient.setQueryData(['claudeLimits'], [
    { account: 'personal', default: true, at, status: 'allowed', windows: [
      { name: 'five_hour', label: '5-hour', utilization: 0.23, resetsAt: resets(3) },
      { name: 'seven_day', label: 'Weekly', utilization: 0.41, resetsAt: resets(80) },
    ] },
    { account: 'work', default: false, at, status: 'allowed_warning', windows: [
      { name: 'five_hour', label: '5-hour', utilization: 0.88, resetsAt: resets(1) },
      { name: 'seven_day', label: 'Weekly', utilization: 0.52, resetsAt: resets(40) },
    ] },
  ] satisfies T.ClaudeLimit[]);
}

const view: View = openAgent ? { kind: 'agent', ref: `${PROJECT}/${openAgent}` } : { kind: 'project', project: PROJECT };

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

function Preview() {
  // The limits editor opens on a click.
  useEffect(() => {
    if (!resources) return;
    document.querySelector<HTMLButtonElement>('[data-preview-limits] button')?.click();
    // Its input takes focus and scrolls itself into view; put Home's stats back on screen.
    requestAnimationFrame(() => document.querySelector('[data-preview-resources]')?.scrollTo(0, 0));
  }, []);

  if (github) return <GitHubPreview />;
  if (usage) return <UsagePreview />;

  if (pulls) {
    return (
      <div style={{ maxWidth: 420, padding: 24, font: '13px var(--font-sans)' }}>
        <PullRequestsPanel project={PROJECT} onSelect={() => {}} onOpenAccount={() => {}} />
      </div>
    );
  }

  if (resources) {
    return (
      <div data-preview-resources style={{ height: '100vh', overflowY: 'auto', background: 'var(--color-ink)' }}>
        <div style={{ height: 300 }}>
          <HomeView onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
        </div>
        <div className="mx-auto grid max-w-3xl gap-4 px-4 pb-10">
          <NewAgentResources />
          <Panel className="p-5" data-preview-limits>
            <LimitsEditor agent={{ ...fixtures.agents[0], limits: { cpu: '4', allowance: '', memory: '8GiB' } }} />
          </Panel>
        </div>
      </div>
    );
  }

  if (imageUpdate) {
    return (
      <div style={{ height: '100vh' }}>
        <SettingsView />
      </div>
    );
  }

  if (defaults) {
    return (
      <div style={{ maxWidth: 720, padding: 24, font: '13px var(--font-sans)' }} className="grid gap-3">
        <Panel className="p-5">
          <h2 className="text-[12px] font-semibold uppercase tracking-[0.08em] text-subtle">Lead</h2>
          <div className="mt-3">
            <DefaultModel role="lead" />
            <DefaultContextWindow role="lead" />
          </div>
        </Panel>
        <Panel className="p-5">
          <h2 className="text-[12px] font-semibold uppercase tracking-[0.08em] text-subtle">New agents</h2>
          <div className="mt-3">
            <DefaultModel role="agents" />
            <DefaultContextWindow role="agents" />
          </div>
        </Panel>
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
                : { lima: '/opt/homebrew/bin/limactl', name: 'agentbox', exists: false, problem: "AgentBox's Linux VM isn't set up: run agentbox vm init" }
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

if (wsl) {
  windowsBeforeSetupBridge();
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
      <Preview />
      <Toaster position="bottom-right" />
    </TooltipProvider>
  </QueryClientProvider>,
);
