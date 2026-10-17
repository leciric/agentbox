// The PiP concept's project: who is working on what, what they captured
// today, and what the lead said about it.
import type * as T from '../../../shared/api';
import { PROJECT } from '../fixtures';
import type { Scene } from './pipDesktops';

const min = 60_000;
export const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

export type Live = { name: string; title: string; ai: T.Agent['ai']; scene: Scene; since: number; doing: string };

// The agents at work, one dock tile each.
export const live: Live[] = [
  { name: 'agent-14', title: 'Export the reminders table as CSV', ai: 'claude', scene: 'csv', since: 38, doing: 'Checking the download with the Open filter on' },
  { name: 'agent-15', title: 'Dark mode for Settings', ai: 'codex', scene: 'dark', since: 36, doing: 'Comparing Appearance in light and dark' },
  { name: 'agent-16', title: 'Fix the flaky reconnect test', ai: 'claude', scene: 'test', since: 35, doing: 'Running TestReconnect 50 times under -race' },
  { name: 'agent-17', title: 'Write the phone app setup guide', ai: 'opencode', scene: 'docs', since: 33, doing: 'Drafting docs/phone-setup.md' },
];

const base = (name: string, title: string, ai: T.Agent['ai'], chat: T.Agent['chat'], createdMin: number): T.Agent => ({
  project: PROJECT,
  name,
  ref: `${PROJECT}/${name}`,
  title,
  instance: '',
  ai,
  autonomous: true,
  branch: `agentbox/${title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 40)}`,
  baseRef: 'main',
  baseCommit: 'abc123',
  worktree: `/home/me/.local/share/agentbox/worktrees/${PROJECT}/${name}`,
  source: 'main',
  claudeAccount: '',
  githubAccount: '',
  codexAccount: '',
  openCodeAccounts: {},
  cursorAccount: '',
  interface: ai,
  state: 'running',
  ip: '',
  chat,
  createdAt: ago(createdMin * min),
});

// agents is the sidebar's list: the four at work and the three the review
// fixtures (dev/review.tsx) have waiting.
export function agents(finished: string[]): T.Agent[] {
  return [
    ...live.map((a) => base(a.name, a.title, a.ai, finished.includes(a.name) ? 'ready' : 'running', a.since)),
    base('agent-12', 'Add a "New project" button to the Sidebar', 'claude', 'ready', 52),
    base('agent-41', 'Release CI: find what failed', 'claude', 'ready', 95),
    base('agent-98', 'Speed up the daemon’s startup', 'codex', 'ready', 240),
  ];
}

export type Capture = {
  id: string;
  agent: string;
  kind: 'screenshot' | 'recording';
  name: string;
  scene: Scene;
  // at is the moment of the scene a screenshot shows (or a recording's poster).
  at: number;
  duration?: number;
  minutesAgo: number;
};

export const captures: Capture[] = [
  { id: 'c1', agent: 'agent-12', kind: 'recording', name: 'new-project-button', scene: 'newproject', at: 6.4, duration: 12, minutesAgo: 3 },
  { id: 'c2', agent: 'agent-12', kind: 'screenshot', name: 'add-project-dialog', scene: 'newproject', at: 4.5, minutesAgo: 4 },
  { id: 'c3', agent: 'agent-12', kind: 'screenshot', name: 'sidebar-with-new-project', scene: 'newproject', at: 7, minutesAgo: 5 },
  { id: 'c4', agent: 'agent-14', kind: 'screenshot', name: 'csv-downloaded', scene: 'csv', at: 4, minutesAgo: 9 },
  { id: 'c5', agent: 'agent-14', kind: 'screenshot', name: 'export-button', scene: 'csv', at: 0.2, minutesAgo: 21 },
  { id: 'c6', agent: 'agent-15', kind: 'screenshot', name: 'appearance-dark', scene: 'dark', at: 1.5, minutesAgo: 11 },
  { id: 'c7', agent: 'agent-15', kind: 'screenshot', name: 'appearance-light', scene: 'dark', at: 5.5, minutesAgo: 12 },
  { id: 'c8', agent: 'agent-15', kind: 'recording', name: 'theme-switch', scene: 'dark', at: 4.2, duration: 8, minutesAgo: 14 },
  { id: 'c9', agent: 'agent-16', kind: 'screenshot', name: 'reconnect-31-of-50', scene: 'test', at: 0, minutesAgo: 16 },
  { id: 'c10', agent: 'agent-17', kind: 'screenshot', name: 'guide-draft', scene: 'docs', at: 9, minutesAgo: 18 },
  { id: 'c11', agent: 'agent-41', kind: 'screenshot', name: 'release-workflow-green', scene: 'ci', at: 0, minutesAgo: 42 },
  { id: 'c12', agent: 'agent-98', kind: 'screenshot', name: 'startup-hyperfine', scene: 'startup', at: 0, minutesAgo: 140 },
  { id: 'c13', agent: 'agent-98', kind: 'recording', name: 'cold-start', scene: 'startup', at: 0, duration: 6, minutesAgo: 150 },
];

// What agent-16 captures when it finishes, during the demo.
export const testDone: Capture = { id: 'c14', agent: 'agent-16', kind: 'screenshot', name: 'reconnect-50-of-50', scene: 'testdone', at: 0, minutesAgo: 0 };

export type Message = {
  id: string;
  who: 'user' | 'lead';
  text: string;
  minutesAgo: number;
  // expand is the mention the lead's message opens on: that agent's latest
  // capture at the time, which it is reporting.
  expand?: string;
  capture?: string;
};

export const messages: Message[] = [
  {
    id: 'm1',
    who: 'user',
    minutesAgo: 40,
    text: 'Get going on the reminders CSV export, dark mode in Settings, the flaky reconnect test and a setup guide for the phone app. And keep agent-12 on the New project button.',
  },
  {
    id: 'm2',
    who: 'lead',
    minutesAgo: 39,
    text: 'Started four agents: agent-14 on the CSV export, agent-15 on dark mode for Settings, agent-16 on the flaky `TestReconnect`, and agent-17 on the phone setup guide. agent-12 is still on the New project button; agent-41 and agent-98 are waiting for your review.',
  },
  { id: 'm3', who: 'user', minutesAgo: 10, text: 'How is the CSV export going?' },
  {
    id: 'm4',
    who: 'lead',
    minutesAgo: 9,
    expand: 'agent-14',
    capture: 'c4',
    text: 'agent-14 has the export working: **Export CSV** downloads `reminders-2026-10-17.csv` with the current filter applied. It is adding a test for titles with commas and quotes next.',
  },
  {
    id: 'm5',
    who: 'lead',
    minutesAgo: 3,
    expand: 'agent-12',
    capture: 'c1',
    text: 'agent-12 finished the **New project** button: PR #318 is green, with screenshots and a recording in Media.',
  },
];

export const doneMessage: Message = {
  id: 'm6',
  who: 'lead',
  minutesAgo: 0,
  expand: 'agent-16',
  capture: 'c14',
  text: 'agent-16 fixed the flaky reconnect test: it reconnected before the old socket had closed. 50 of 50 runs pass under `-race` now.',
};
