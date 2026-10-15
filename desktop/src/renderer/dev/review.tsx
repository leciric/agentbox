// ?review=1: "Ready to review" (components/ReadyToReview.tsx) with three
// agents waiting, in the top bar's inbox (components/Inbox.tsx). agent-12's
// carousel has two screenshots, a recording (drawn here, on a canvas) and its
// Hatch pages (dev/pages.ts), then its pull request in full, which merges;
// agent-41's pull request fails its checks, so it can't. Opening an agent
// moves it to Reviewed, which the dev bridge remembers (reviewRequest), and
// two agents are there already. ?review=many has forty more waiting, for the
// list to load on scroll.
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { Suspense } from 'react';
import type * as T from '../../shared/api';
import { HomeProject as T_HOME } from '../../shared/api';
import type { View } from '../App';
import { AgentRail } from '../components/AgentRail';
import { ProjectView } from '../components/ProjectView';
import { Sidebar } from '../components/Sidebar';
import { TopBar } from '../components/TopBar';
import { devRequests, pageOf, patchDevAgent, PROJECT, setMediaFile } from './fixtures';
import { useReadyAlerts } from '../lib/useReadyAlerts';
import { screen } from './notifications';
import { seedPages } from './pages';

const min = 60_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

const pr: T.PullRequest = {
  number: 318,
  title: 'feat: a "New project" button at the top of the sidebar',
  state: 'open',
  checks: 'passing',
  url: 'https://github.com/leciric/agentbox/pull/318',
  additions: 142,
  deletions: 17,
  baseBranch: 'main',
  headBranch: 'agentbox/new-project-button',
  headSha: 'f00d318',
  author: 'leciric',
};

let waiting: T.ReviewItem[] = [];
let decided: T.ReviewItem[] = [];
let client: QueryClient | undefined;

function items(): T.ReviewItem[] {
  const item = (agent: string, title: string, branch: string, finished: number, more: Partial<T.ReviewItem> = {}): T.ReviewItem => ({
    project: PROJECT,
    agent,
    ref: `${PROJECT}/${agent}`,
    title,
    branch,
    ai: 'claude',
    finishedAt: ago(finished),
    fingerprint: `{"f":"${agent}"}`,
    ...more,
  });
  return [
    item('agent-12', 'Add a "New project" button to the Sidebar', 'agentbox/new-project-button', 3 * min, {
      pr,
      summary:
        'Added a **New project** button at the top of the sidebar, with a tooltip and the `Ctrl+Shift+N` shortcut.\n\n- The empty sidebar points at it too\n- Strings are in en-US and pt-BR\n\nScreenshots and a recording are in Media; PR #318 is green.',
      changes: { files: 4, insertions: 142, deletions: 17, dirty: false },
    }),
    item('agent-41', 'Release CI: find what failed', 'agentbox/release-ci-failures', 40 * min, {
      ai: 'codex',
      pr: { ...pr, number: 312, title: 'fix: the release build waits for the signed Mac zip', checks: 'failing', additions: 12, deletions: 3, headBranch: 'agentbox/release-ci-failures' },
      summary: 'The release build started before the signed Mac zip was uploaded. It now waits for it. The Windows job still fails on a flaky test, so checks are red.',
      changes: { files: 2, insertions: 12, deletions: 3, dirty: false },
    }),
    item('agent-98', 'Speed up the daemon’s startup', 'agentbox/agent-98-startup-is-slow-on-a-cold-cache-with-many-projects', 3 * 60 * min),
  ];
}

// many is forty more agents waiting, each an hour older than the last.
function many(): T.ReviewItem[] {
  return Array.from({ length: 40 }, (_, i) => {
    const agent = `agent-${200 + i}`;
    return { project: PROJECT, agent, ref: `${PROJECT}/${agent}`, title: `Task ${i + 1}: tidy the settings page`, branch: `agentbox/task-${i + 1}`, ai: 'claude', finishedAt: ago((4 + i) * 60 * min), fingerprint: `{"f":"${agent}"}` };
  });
}

// done is what was reviewed before the preview opened.
function done(): T.ReviewItem[] {
  const base = { project: PROJECT, ai: 'claude', fingerprint: '{}' };
  return [
    { ...base, agent: 'agent-07', ref: `${PROJECT}/agent-07`, title: 'Paginate the reminders list', branch: 'agentbox/reminders-pages', finishedAt: ago(5 * 60 * min), verdict: 'reviewed', reviewedAt: ago(2 * 60 * min), summary: 'Paginated the list, 50 a page.' },
    { ...base, agent: 'agent-03', ref: `${PROJECT}/agent-03`, title: 'Try a denser agent rail', branch: 'agentbox/dense-rail', finishedAt: ago(26 * 60 * min), verdict: 'dismissed', reviewedAt: ago(24 * 60 * min) },
  ];
}

function media(): T.MediaItem[] {
  const dark = document.documentElement.dataset.appearance !== 'light';
  const shot = (id: string, name: string, hue: number, at: number): T.MediaItem => {
    setMediaFile(id, screen(name, hue, dark));
    return { id, agent: `${PROJECT}/agent-12`, agentName: 'agent-12', kind: 'screenshot', name, file: `${name}.png`, mime: 'image/png', size: 210_000, source: 'agent', meta: { width: 1280, height: 800 }, createdAt: ago(at) };
  };
  return [
    shot('rv-before', 'sidebar-before', 262, 40 * min),
    shot('rv-after', 'sidebar-new-project-button', 150, 12 * min),
    {
      id: 'rv-rec',
      agent: `${PROJECT}/agent-12`,
      agentName: 'agent-12',
      kind: 'recording',
      name: 'new-project-walkthrough',
      file: 'new-project-walkthrough.webm',
      mime: 'video/webm',
      size: 1_400_000,
      source: 'agent',
      meta: { duration: 4 },
      createdAt: ago(5 * min),
    },
    { id: 'rv-note', agent: `${PROJECT}/agent-12`, agentName: 'agent-12', kind: 'note', name: 'note', size: 20, source: 'agent', text: 'Not in the carousel', meta: {}, createdAt: ago(4 * min) },
  ];
}

// recording draws a few seconds of a cursor crossing a window, so the
// carousel has a video to play inline.
async function recording(): Promise<string> {
  const canvas = Object.assign(document.createElement('canvas'), { width: 640, height: 400 });
  const ctx = canvas.getContext('2d')!;
  const recorder = new MediaRecorder(canvas.captureStream(30), { mimeType: 'video/webm' });
  const chunks: Blob[] = [];
  recorder.ondataavailable = (e) => chunks.push(e.data);
  const done = new Promise<void>((resolve) => (recorder.onstop = () => resolve()));
  recorder.start();
  const start = performance.now();
  await new Promise<void>((resolve) => {
    const frame = () => {
      const t = (performance.now() - start) / 4000;
      ctx.fillStyle = '#16151f';
      ctx.fillRect(0, 0, 640, 400);
      ctx.fillStyle = '#211f2e';
      ctx.fillRect(0, 0, 150, 400);
      ctx.fillStyle = t > 0.6 ? '#6366f1' : '#2f2c40';
      ctx.fillRect(16, 20, 118, 28);
      ctx.fillStyle = '#e7e7ee';
      ctx.font = '600 13px sans-serif';
      ctx.fillText('+ New project', 30, 39);
      ctx.beginPath();
      ctx.arc(500 - 420 * Math.min(t / 0.6, 1), 300 - 266 * Math.min(t / 0.6, 1), 7, 0, 2 * Math.PI);
      ctx.fill();
      if (t < 1) requestAnimationFrame(frame);
      else resolve();
    };
    frame();
  });
  recorder.stop();
  await done;
  return URL.createObjectURL(new Blob(chunks, { type: 'video/webm' }));
}

export function seedReview(queryClient: QueryClient): void {
  client = queryClient;
  seedPages(queryClient);
  // The top bar's meters, which no other fixture seeds.
  queryClient.setQueryData(['claudeLimits'], []);
  waiting = items();
  if (new URLSearchParams(location.search).get('review') === 'many') waiting = [...waiting, ...many()];
  decided = done();
  queryClient.setQueryData(['media', `${PROJECT}/agent-12`], media());
  const dark = document.documentElement.dataset.appearance !== 'light';
  setMediaFile('rv-ci', screen('release-workflow-green-mac-step', 20, dark));
  queryClient.setQueryData(['media', `${PROJECT}/agent-41`], [
    { id: 'rv-ci', agent: `${PROJECT}/agent-41`, agentName: 'agent-41', kind: 'screenshot', name: 'release-workflow', file: 'release-workflow.png', mime: 'image/png', size: 180_000, source: 'agent', meta: { width: 1280, height: 800 }, createdAt: ago(42 * min) },
  ] satisfies T.MediaItem[]);
  for (const agent of ['agent-98', 'agent-07', 'agent-03']) queryClient.setQueryData(['media', `${PROJECT}/${agent}`], []);
  void recording().then((url) => setMediaFile('rv-rec', url));
  devRequests.push(reviewRequest);
}

// reviewRequest answers the dev bridge for "Ready to review": the list, and
// a verdict, which takes the agent off it.
function reviewRequest(method: string, path: string, body?: unknown): { status: number; body: string; contentType: string } | undefined {
  // The top bar re-reads the Claude limits every 30s, past what was seeded.
  if (method === 'GET' && path === '/v1/limits') return { status: 200, body: '[]', contentType: 'application/json' };
  const [route, query] = path.split('?');
  // The composer's AI tool picker (#297), which no other fixture answers.
  if (method === 'GET' && route === `/v1/projects/${PROJECT}/chat/runners`) {
    const runners: T.LeadRunners = { tool: 'claude', runners: [{ tool: 'claude', name: 'Claude Code', ready: true, installed: true }] };
    return { status: 200, body: JSON.stringify(runners), contentType: 'application/json' };
  }
  if (method === 'GET' && (route === '/v1/review' || route === `/v1/projects/${PROJECT}/review`)) {
    return { status: 200, body: JSON.stringify(pageOf(query?.includes('reviewed') ? decided : waiting, query)), contentType: 'application/json' };
  }
  // The project's pull requests, as the review reads them to merge one.
  if (method === 'GET' && route === `/v1/projects/${PROJECT}/pulls`) {
    const list: T.ProjectPullRequests = {
      project: PROJECT,
      github: 'leciric/agentbox',
      githubAccount: 'default',
      fetchedAt: new Date().toISOString(),
      canMerge: true,
      canMergeKnown: true,
      mergeMethods: ['squash', 'merge', 'rebase'],
      pullRequests: [...waiting, ...decided].flatMap((i) => (i.pr ? [{ ...i.pr, agent: i.agent }] : [])),
    };
    return { status: 200, body: JSON.stringify(list), contentType: 'application/json' };
  }
  const merge = method === 'POST' && /^\/v1\/projects\/[^/]+\/pulls\/(\d+)\/merge$/.exec(route);
  if (merge) {
    const number = Number(merge[1]);
    let merged: T.PullRequest | undefined;
    for (const i of [...waiting, ...decided]) if (i.pr?.number === number) merged = i.pr = { ...i.pr, state: 'merged' };
    if (number === pr.number) pr.state = 'merged';
    return { status: 200, body: JSON.stringify(merged ?? {}), contentType: 'application/json' };
  }
  // The pull request modal's detail of #318, and its files.
  const detailOf = method === 'GET' && /^\/v1\/projects\/[^/]+\/pulls\/(\d+)$/.exec(route);
  const listed = detailOf && [...waiting, ...decided].find((i) => i.pr?.number === Number(detailOf[1]))?.pr;
  if (listed && listed.number !== pr.number) {
    const detail: T.PullRequestDetail = {
      ...listed,
      body: 'Waits for the signed Mac zip before publishing.',
      checkRuns: [
        { name: 'Go tests and coverage', status: 'completed', conclusion: 'success' },
        { name: 'Windows', status: 'completed', conclusion: 'failure' },
      ],
      changedFiles: 2,
    };
    return { status: 200, body: JSON.stringify(detail), contentType: 'application/json' };
  }
  if (method === 'GET' && route === `/v1/projects/${PROJECT}/pulls/318`) {
    const detail: T.PullRequestDetail = {
      ...pr,
      body: 'Adds a **New project** button at the top of the sidebar, with a tooltip and the shortcut.\n\nChecked in the preview and in the app; the recording is in Media.',
      checkRuns: [
        { name: 'Go tests and coverage', status: 'completed', conclusion: 'success' },
        { name: 'Desktop', status: 'completed', conclusion: 'success' },
      ],
      changedFiles: 4,
    };
    return { status: 200, body: JSON.stringify(detail), contentType: 'application/json' };
  }
  if (method === 'GET' && /^\/v1\/projects\/[^/]+\/pulls\/\d+\/files/.test(route)) {
    return { status: 200, body: JSON.stringify({ files: [] } satisfies T.PullRequestFiles), contentType: 'application/json' };
  }
  // Request changes (UI v2): the agent works on them for 6s, then is back in
  // Ready to review with new work.
  const changes = method === 'POST' && /^\/v1\/agents\/[^/]+\/([^/]+)\/request-changes$/.exec(path);
  if (changes) {
    const agent = changes[1];
    const text = (body as T.RequestChangesRequest | undefined)?.text ?? '';
    const item = [...waiting, ...decided].find((i) => i.agent === agent);
    waiting = waiting.filter((i) => i.agent !== agent);
    decided = [...(item ? [{ ...item, verdict: 'changes', reviewedAt: new Date().toISOString() }] : []), ...decided.filter((i) => i.agent !== agent)];
    const set = (over: Partial<T.Agent>) => {
      patchDevAgent(`${PROJECT}/${agent}`, over);
      client?.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === `${PROJECT}/${agent}` ? { ...a, ...over } : a)));
    };
    set({ chat: 'running', changesRequested: text });
    setTimeout(() => {
      set({ chat: 'ready', outcome: 'done', changesRequested: undefined });
      decided = decided.filter((i) => i.agent !== agent);
      if (item) waiting = [{ ...item, verdict: undefined, reviewedAt: undefined, finishedAt: new Date().toISOString(), fingerprint: `{"f":"${agent}-again"}`, summary: `Made the changes you asked for: ${text}` }, ...waiting];
      void client?.invalidateQueries({ queryKey: ['review'] });
      void client?.invalidateQueries({ queryKey: ['reviewed'] });
    }, 6_000);
    const reply: T.ChatItem = { id: `rc-${Date.now()}`, turn: '', kind: 'user', text, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() };
    return { status: 202, body: JSON.stringify(reply), contentType: 'application/json' };
  }
  const verdict = method === 'POST' && /^\/v1\/agents\/[^/]+\/([^/]+)\/review$/.exec(path);
  if (!verdict) return undefined;
  const item = waiting.find((i) => i.agent === verdict[1]);
  waiting = waiting.filter((i) => i.agent !== verdict[1]);
  const v = (body as T.ReviewRequest | undefined)?.verdict ?? 'reviewed';
  if (item) decided = [{ ...item, verdict: v, reviewedAt: new Date().toISOString() }, ...decided];
  return { status: 204, body: '', contentType: 'application/json' };
}

// seedLeadLines gives each lead, and Home, a last line for UI v2's sidebar.
export function seedLeadLines(queryClient: QueryClient): void {
  const line = (kind: string, text: string, ms: number): T.ChatLine => ({ kind, text, at: ago(ms) });
  const lines: Record<string, T.ChatLine> = {
    [T_HOME]: line('assistant', 'Three agents across your projects are ready to review, and one is stopped on an error.', 14 * min),
    [PROJECT]: line('assistant', 'agent-12 finished the **New project** button: PR #318 is green, with screenshots and a recording in Media.', 3 * min),
    'a-project-with-a-name-so-long-it-should-never-be-allowed-to-widen-the-sidebar': line('user', 'https://example.com/a/very/long/unbroken/url/that/should/never/widen/the/sidebar/at/all', 3 * 60 * min),
  };
  for (const [project, last] of Object.entries(lines)) {
    queryClient.setQueryData<T.ProjectChat>(['projectChat', project], (info) => ({ ...(info ?? { project, ref: `${project}/lead`, started: true, chat: 'ready' }), last }));
  }
}

// land is what the button below stands for: an agent finishing while the app
// is open, which the same hook as the app's (useReadyAlerts) announces.
function land(queryClient: QueryClient) {
  waiting = [{ project: PROJECT, agent: 'agent-77', ref: `${PROJECT}/agent-77`, title: 'Fix the flaky reconnect test', branch: 'agentbox/flaky-reconnect', ai: 'claude', finishedAt: new Date().toISOString(), fingerprint: `{"f":"agent-77"}` }, ...waiting];
  void queryClient.invalidateQueries({ queryKey: ['review'] });
}

export function ReviewPreview() {
  const queryClient = useQueryClient();
  useReadyAlerts(() => {});
  const view: View = { kind: 'project', project: PROJECT };
  return (
    <div style={{ display: 'flex', height: '100vh', width: '100vw' }}>
      <Sidebar view={view} onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', background: 'var(--color-ink)' }} data-preview-review>
        <TopBar view={view} onSelect={() => {}} onOpenNav={() => {}} onOpenNotice={() => {}} />
        <button type="button" data-preview-land onClick={() => land(queryClient)} style={{ position: 'fixed', bottom: 12, left: 260, zIndex: 50, padding: '6px 12px', borderRadius: 8, background: '#333', color: '#fff', fontSize: 12 }}>
          Land agent-77
        </button>
        <div style={{ flex: 1, minHeight: 0 }}>
          <Suspense>
            <ProjectView name={PROJECT} tab="chat" onSelect={() => {}} onNewAgent={() => {}} />
          </Suspense>
        </div>
      </div>
      <AgentRail view={view} onSelect={() => {}} onNewAgent={() => {}} />
    </div>
  );
}
