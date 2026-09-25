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
//   ?open=agent-99          the named agent's thread open (ref suffix only)
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
//   ?accounts=1             Settings' Claude Code accounts, with a rename the
//                           dev bridge answers (fixtures.ts)
//   ?github=1               a project's GitHub account picker, on a project
//                           that limits its Claude Code accounts, beside
//                           Settings' GitHub accounts with a rename the dev
//                           bridge answers
// See scenarios.json for the set scripts/preview.mjs captures.
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '../styles.css';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import type * as T from '../../shared/api';
import { Toaster } from 'sonner';
import type { View } from '../App';
import { AgentRail } from '../components/AgentRail';
import { GitHubAccountPicker } from '../components/ProjectView';
import { ClaudeAccounts, GitHubAccounts } from '../components/SettingsView';
import { Sidebar } from '../components/Sidebar';
import { TooltipProvider } from '../components/ui/tooltip';
import { VMSetup } from '../components/VMSetup';
import { VMSize } from '../components/VMSize';
import { ChatTab } from '../components/chat/ChatTab';
import { Timeline } from '../components/chat/Timeline';
import { leadAgentFrom } from '../components/ProjectChatPanel';
import { api } from '../lib/api';
import { agent12Chat, buildFixtures, compactionThread, installDevBridge, PROJECT, seedQueryClient } from './fixtures';

installDevBridge();

const params = new URLSearchParams(location.search);
document.documentElement.dataset.appearance = params.get('theme') === 'light' ? 'light' : '';
localStorage.setItem('agentbox.rail.folded', params.get('folded') === '1' ? '1' : '0');
const openAgent = params.get('open'); // e.g. "agent-99"; matches AgentRail's data-rail-thread
const vm = params.get('vm');
const accounts = params.get('accounts') === '1';
const github = params.get('github') === '1';

const chat = params.get('chat');
const fixtures = buildFixtures();
if (github) {
  // Two GitHub accounts, and a project that picked the second one while it
  // limits its Claude Code accounts to one called "work".
  const p = fixtures.projects.find((p) => p.name === PROJECT)!;
  p.claudeAccounts = ['work'];
  p.githubAccount = 'personal-account-with-a-long-name';
}
const chatAgent = chat ? fixtures.agents.find((a) => a.ref === `${PROJECT}/${chat}`) : undefined;

const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false, refetchOnWindowFocus: false } } });
seedQueryClient(queryClient, fixtures);

const view: View = { kind: 'project', project: PROJECT };

function Preview() {
  // Opening a thread is state AgentRail keeps to itself (there's no prop for
  // it, on purpose — nothing outside a click needs it), so the URL drives it
  // by clicking the same chevron a person would, once the row exists.
  useEffect(() => {
    if (!openAgent) return;
    document.querySelector<HTMLButtonElement>(`[data-rail-thread="${PROJECT}/${openAgent}"]`)?.click();
  }, []);

  if (github) return <GitHubPreview />;

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
        {chat === 'compaction' ? (
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

createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <TooltipProvider delayDuration={250}>
      <Preview />
      <Toaster position="bottom-right" />
    </TooltipProvider>
  </QueryClientProvider>,
);
