// The lead's picture-in-picture desktops (components/pip) in the dev preview,
// with &pip=1 on ?app=project&v2=1: four agents at work, whose desktops are
// drawn (pipDesktops.tsx) where the app would stream them, three waiting for
// review, today's screenshots and recordings, and a lead that reports on
// them. "Finish agent-26" has one finish while you watch.
import type { QueryClient } from '@tanstack/react-query';
import { useQueryClient } from '@tanstack/react-query';
import { renderToStaticMarkup } from 'react-dom/server';
import type * as T from '../../shared/api';
import { chatKey } from '../lib/chat';
import { setDesktopStandIn } from '../components/pip/standIn';
import { devRequests, leadChat, PROJECT, setDevMedia, setMediaFile } from './fixtures';
import { Desktop, H, SceneAt, W, type Scene } from './pipDesktops';
import { landReview } from './review';

const min = 60_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

type Working = { name: string; title: string; ai: string; scene: Scene; since: number };
const working: Working[] = [
  { name: 'agent-24', title: 'Export the reminders table as CSV', ai: 'claude', scene: 'csv', since: 38 },
  { name: 'agent-25', title: 'Dark mode for Settings', ai: 'codex', scene: 'dark', since: 36 },
  { name: 'agent-26', title: 'Fix the flaky reconnect test', ai: 'claude', scene: 'test', since: 35 },
  { name: 'agent-27', title: 'Write the phone app setup guide', ai: 'opencode', scene: 'docs', since: 33 },
  // At work without its desktop, until "agent-28 opens its desktop".
  { name: 'agent-28', title: 'Make the release workflow retry flaky uploads', ai: 'claude', scene: 'ci', since: 20 },
];
// using is who is driving its desktop: the daemon's /desktops answers them as
// just now, and anyone who stopped as a while ago.
const using = new Set(['agent-24', 'agent-25', 'agent-26', 'agent-27']);
const desktops = (): T.DesktopUse[] => working.map((w) => ({ ref: `${PROJECT}/${w.name}`, usedAt: ago(using.has(w.name) ? 2_000 : 10 * min) }));
const sceneOf: Record<string, Scene> = { ...Object.fromEntries(working.map((w) => [w.name, w.scene])), 'agent-12': 'newproject', 'agent-41': 'ci', 'agent-98': 'startup' };

const agent = (name: string, title: string, ai: string, chat: string, createdMin: number): T.Agent =>
  ({
    project: PROJECT,
    name,
    ref: `${PROJECT}/${name}`,
    title,
    instance: '',
    ai,
    autonomous: true,
    branch: `agentbox/${name}`,
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
  }) as T.Agent;

let finished = false;
const agents = (): T.Agent[] => [
  ...working.map((w) => agent(w.name, w.title, w.ai, finished && w.name === 'agent-26' ? 'ready' : 'running', w.since)),
  // The review fixtures' (dev/review.tsx): agent-12 finished 3 minutes ago.
  agent('agent-12', 'Add a "New project" button to the Sidebar', 'claude', 'ready', 52),
  agent('agent-41', 'Release CI: find what failed', 'claude', 'ready', 95),
  agent('agent-98', 'Speed up the daemon’s startup', 'codex', 'ready', 240),
];

// shot is a scene at a moment as a picture: its markup in an SVG.
function shot(scene: Scene, t: number): string {
  const html = renderToStaticMarkup(
    <div style={{ position: 'relative', width: W, height: H, overflow: 'hidden' }}>
      <SceneAt scene={scene} t={t} />
    </div>,
  );
  const body = html.replace('<div', '<div xmlns="http://www.w3.org/1999/xhtml"');
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}"><foreignObject width="100%" height="100%">${body}</foreignObject></svg>`)}`;
}

type Capture = { id: string; agent: string; kind: 'screenshot' | 'recording'; name: string; t: number; minutesAgo: number; scene?: Scene };
const captures: Capture[] = [
  // The review fixtures' recording of the New project button (rv-rec).
  { id: 'rv-rec', agent: 'agent-12', kind: 'recording', name: 'new-project-button', t: 0, minutesAgo: 3 },
  { id: 'pip-2', agent: 'agent-12', kind: 'screenshot', name: 'add-project-dialog', t: 4.5, minutesAgo: 4 },
  { id: 'pip-3', agent: 'agent-12', kind: 'screenshot', name: 'sidebar-with-new-project', t: 7, minutesAgo: 5 },
  { id: 'pip-4', agent: 'agent-24', kind: 'screenshot', name: 'csv-downloaded', t: 4, minutesAgo: 9 },
  { id: 'pip-5', agent: 'agent-24', kind: 'screenshot', name: 'export-button', t: 0.2, minutesAgo: 21 },
  { id: 'pip-6', agent: 'agent-25', kind: 'screenshot', name: 'appearance-dark', t: 1.5, minutesAgo: 11 },
  { id: 'pip-7', agent: 'agent-25', kind: 'screenshot', name: 'appearance-light', t: 5.5, minutesAgo: 12 },
  { id: 'pip-9', agent: 'agent-26', kind: 'screenshot', name: 'reconnect-31-of-50', t: 0, minutesAgo: 16 },
  { id: 'pip-10', agent: 'agent-27', kind: 'screenshot', name: 'guide-draft', t: 9, minutesAgo: 18 },
  { id: 'pip-11', agent: 'agent-41', kind: 'screenshot', name: 'release-workflow-green', t: 0, minutesAgo: 42 },
  { id: 'pip-12', agent: 'agent-98', kind: 'screenshot', name: 'startup-hyperfine', t: 0, minutesAgo: 140 },
];
const testDone: Capture = { id: 'pip-14', agent: 'agent-26', kind: 'screenshot', name: 'reconnect-50-of-50', t: 0, minutesAgo: 0, scene: 'testdone' };

const mediaItem = (c: Capture): T.MediaItem => ({
  id: c.id,
  agent: `${PROJECT}/${c.agent}`,
  agentName: c.agent,
  agentTitle: agents().find((a) => a.name === c.agent)?.title,
  kind: c.kind,
  name: c.name,
  file: c.kind === 'recording' ? `${c.name}.webm` : `${c.name}.png`,
  mime: c.kind === 'recording' ? 'video/webm' : 'image/png',
  size: c.kind === 'recording' ? 840_000 : 180_000,
  source: 'agent',
  meta: { width: W, height: H, ...(c.kind === 'recording' ? { durationMs: 4_000 } : {}) },
  createdAt: ago(c.minutesAgo * min),
});

let media: T.MediaItem[] = [];
function addCapture(c: Capture) {
  if (c.kind === 'screenshot') setMediaFile(c.id, shot(c.scene ?? sceneOf[c.agent], c.t));
  media = [mediaItem(c), ...media];
  setDevMedia(media);
}

// finished is an agent whose finish the lead was told of just before, which
// the chat shows as its evidence card (chat/EvidenceCard.tsx).
type Said = { who: 'user' | 'lead'; text: string; minutesAgo: number; finished?: string };
const said: Said[] = [
  { who: 'user', minutesAgo: 40, text: 'Get going on the reminders CSV export, dark mode in Settings, the flaky reconnect test and a setup guide for the phone app. And keep agent-12 on the New project button.' },
  {
    who: 'lead',
    minutesAgo: 39,
    text: 'Started four agents: agent-24 on the CSV export, agent-25 on dark mode for Settings, agent-26 on the flaky `TestReconnect`, and agent-27 on the phone setup guide. agent-12 is still on the New project button; agent-41 and agent-98 are waiting for your review.',
  },
  { who: 'user', minutesAgo: 10, text: 'How is the CSV export going?' },
  { who: 'lead', minutesAgo: 8.5, text: 'agent-24 has the export working: **Export CSV** downloads `reminders-2026-10-17.csv` with the current filter applied. It is adding a test for titles with commas and quotes next.' },
  { who: 'user', minutesAgo: 4, text: 'And the New project button?' },
  { who: 'lead', minutesAgo: 2.5, finished: 'agent-12', text: 'agent-12 finished the **New project** button: PR #318 is green, with screenshots and a recording in Media.' },
];
const done: Said = { who: 'lead', minutesAgo: 0, finished: 'agent-26', text: 'agent-26 fixed the flaky reconnect test: it reconnected before the old socket had closed. 50 of 50 runs pass under `-race` now.' };

let thread: T.ChatThread;
function chatItems(lines: Said[]): T.ChatItem[] {
  let turn = '';
  return lines.flatMap((l, i) => {
    if (l.who === 'user') turn = `pip-m${i}`;
    const at = ago(l.minutesAgo * min);
    const item = {
      id: `pip-m${i}`,
      turn,
      kind: l.who === 'user' ? 'user' : 'assistant',
      text: l.text,
      ...(l.who === 'user' ? { result: { state: 'completed', stopReason: 'end_turn', endedAt: at } } : {}),
      createdAt: at,
      updatedAt: at,
    } as T.ChatItem;
    if (!l.finished) return [item];
    const noticed = ago(l.minutesAgo * min + 5_000);
    const notice = { id: `pip-n${i}`, turn, kind: 'notice', hidden: true, finished: [`${PROJECT}/${l.finished}`], text: `${l.finished} finished.`, createdAt: noticed, updatedAt: noticed } as T.ChatItem;
    return [notice, item];
  });
}
function setThread(lines: Said[]) {
  thread = { ...leadChat(), seq: lines.length, items: chatItems(lines) };
}

// seedPip puts the scenario in place of the v2 fixtures' agents, media and
// lead's chat; after seedV2, seedReview and seedMedia.
export function seedPip(queryClient: QueryClient): void {
  setDesktopStandIn((a) => <Desktop scene={finished && a.name === 'agent-26' ? 'testdone' : (sceneOf[a.name] ?? 'csv')} t={a.chat === 'ready' ? 7 : undefined} />);
  for (const c of [...captures].reverse()) addCapture(c);
  setThread(said);
  const json = (v: unknown) => ({ status: 200, body: JSON.stringify(v), contentType: 'application/json' });
  devRequests.unshift((method, path) => {
    if (method !== 'GET') return undefined;
    if (path === '/v1/agents') return json(agents());
    if (path === `/v1/projects/${PROJECT}/desktops`) return json(desktops());
    if (/^\/v1\/projects\/[^/]+\/chat(\?|$)/.test(path) && path.startsWith(`/v1/projects/${PROJECT}/`)) return json(thread);
    if (path.startsWith('/v1/archived-agents')) return json([]);
    // The fixtures' agents asking for accounts and secrets aren't this scenario's.
    if (path.startsWith(`/v1/projects/${PROJECT}/questions`)) return json([]);
    return undefined;
  });
  queryClient.setQueryData(['agents'], agents());
  queryClient.setQueryData(chatKey(`${PROJECT}/lead`), thread);
  queryClient.setQueryData(['media', `${PROJECT}/agent-26`], []);
  queryClient.setQueryData(['questions', PROJECT], []);
}

// PipFinish lands agent-26 as the preview runs: it stops using its desktop,
// so its tile goes, it saves a screenshot, and the lead says so. Its other
// button has agent-28, at work without its desktop, start using it.
export function PipFinish() {
  const queryClient = useQueryClient();
  const finish = () => {
    if (finished) return;
    finished = true;
    using.delete('agent-26');
    void queryClient.invalidateQueries({ queryKey: ['desktops'] });
    queryClient.setQueryData(['agents'], agents());
    addCapture({ ...testDone, minutesAgo: 0 });
    queryClient.setQueryData(['media', `${PROJECT}/agent-26`], [mediaItem(testDone)]);
    landReview(queryClient, {
      project: PROJECT,
      agent: 'agent-26',
      ref: `${PROJECT}/agent-26`,
      title: 'Fix the flaky reconnect test',
      branch: 'agentbox/flaky-reconnect',
      ai: 'claude',
      summary: 'TestReconnect reconnected before the old socket had closed, so one run in ten saw two sessions. It waits for the close now; 50 of 50 runs pass under `-race`.',
      finishedAt: new Date().toISOString(),
      fingerprint: '{"f":"agent-26"}',
    });
    void queryClient.invalidateQueries({ queryKey: ['mediaPages'] });
    void queryClient.invalidateQueries({ queryKey: ['pipCaptures'] });
    window.setTimeout(() => {
      setThread([...said, done]);
      queryClient.setQueryData(chatKey(`${PROJECT}/lead`), thread);
    }, 900);
  };
  const open = () => {
    using.add('agent-28');
    void queryClient.invalidateQueries({ queryKey: ['desktops'] });
  };
  const button = 'rounded-lg border border-line-strong bg-overlay px-3 py-1.5 text-[12px] text-secondary shadow-md hover:text-title';
  return (
    <div className="fixed bottom-3 left-[300px] z-40 flex gap-2">
      <button type="button" onClick={open} data-pip-use className={button}>
        agent-28 opens its desktop
      </button>
      <button type="button" onClick={finish} data-pip-finish className={button}>
        Finish agent-26
      </button>
    </div>
  );
}
