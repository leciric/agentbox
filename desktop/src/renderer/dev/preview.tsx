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
//   ?vm=create|lima         a Mac's first screen in the middle: the VM to set up,
//                           or Lima to install first
//   ?vm=resize              Settings' panel for the VM's CPUs and memory, whose
//                           resize streams made-up output
//   ?accounts=1             Settings' Claude Code accounts, with a rename the
//                           dev bridge answers (fixtures.ts)
// See scenarios.json for the set scripts/preview.mjs captures.
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '../styles.css';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import type { View } from '../App';
import { AgentRail } from '../components/AgentRail';
import { ClaudeAccounts } from '../components/SettingsView';
import { Sidebar } from '../components/Sidebar';
import { TooltipProvider } from '../components/ui/tooltip';
import { VMSetup } from '../components/VMSetup';
import { VMSize } from '../components/VMSize';
import { buildFixtures, installDevBridge, PROJECT, seedQueryClient } from './fixtures';

installDevBridge();

const params = new URLSearchParams(location.search);
document.documentElement.dataset.appearance = params.get('theme') === 'light' ? 'light' : '';
localStorage.setItem('agentbox.rail.folded', params.get('folded') === '1' ? '1' : '0');
const openAgent = params.get('open'); // e.g. "agent-99"; matches AgentRail's data-rail-thread
const vm = params.get('vm');
const accounts = params.get('accounts') === '1';

const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false, refetchOnWindowFocus: false } } });
seedQueryClient(queryClient, buildFixtures());

const view: View = { kind: 'project', project: PROJECT };

function Preview() {
  // Opening a thread is state AgentRail keeps to itself (there's no prop for
  // it, on purpose — nothing outside a click needs it), so the URL drives it
  // by clicking the same chevron a person would, once the row exists.
  useEffect(() => {
    if (!openAgent) return;
    document.querySelector<HTMLButtonElement>(`[data-rail-thread="${PROJECT}/${openAgent}"]`)?.click();
  }, []);

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
        {vm === 'resize' ? (
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

createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <TooltipProvider delayDuration={250}>
      <Preview />
      <Toaster position="bottom-right" />
    </TooltipProvider>
  </QueryClientProvider>,
);
