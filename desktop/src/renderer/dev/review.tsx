// ?review=1: "Ready to review" (components/ReadyToReview.tsx) with three
// agents waiting, the top bar's button and the project header's. agent-12's
// carousel has two screenshots, a recording (drawn here, on a canvas) and its
// Hatch pages (dev/pages.ts), then its pull request in full; Reviewed and Dismiss
// take an agent off, which the dev bridge remembers (reviewRequest).
import type { QueryClient } from '@tanstack/react-query';
import { Suspense } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { AgentRail } from '../components/AgentRail';
import { ProjectView } from '../components/ProjectView';
import { Sidebar } from '../components/Sidebar';
import { TopBar } from '../components/TopBar';
import { devRequests, PROJECT, setMediaFile } from './fixtures';
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
      summary: 'Added a New project button at the top of the sidebar, with a tooltip and the shortcut. Screenshots and a recording are in Media.',
    }),
    item('agent-41', 'Release CI: find what failed', 'agentbox/release-ci-failures', 40 * min, {
      ai: 'codex',
      pr: { ...pr, number: 312, title: 'fix: the release build waits for the signed Mac zip', checks: 'failing', additions: 12, deletions: 3 },
    }),
    item('agent-98', 'Speed up the daemon’s startup', 'agentbox/agent-98-startup-is-slow-on-a-cold-cache-with-many-projects', 3 * 60 * min),
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
  seedPages(queryClient);
  // The top bar's meters, which no other fixture seeds.
  queryClient.setQueryData(['claudeLimits'], []);
  waiting = items();
  queryClient.setQueryData(['media', `${PROJECT}/agent-12`], media());
  for (const agent of ['agent-41', 'agent-98']) queryClient.setQueryData(['media', `${PROJECT}/${agent}`], []);
  void recording().then((url) => setMediaFile('rv-rec', url));
  devRequests.push(reviewRequest);
}

// reviewRequest answers the dev bridge for "Ready to review": the list, and
// a verdict, which takes the agent off it.
function reviewRequest(method: string, path: string): { status: number; body: string; contentType: string } | undefined {
  // The top bar re-reads the Claude limits every 30s, past what was seeded.
  if (method === 'GET' && path === '/v1/limits') return { status: 200, body: '[]', contentType: 'application/json' };
  if (method === 'GET' && (path === '/v1/review' || path === `/v1/projects/${PROJECT}/review`)) {
    return { status: 200, body: JSON.stringify(waiting), contentType: 'application/json' };
  }
  // The pull request modal's detail of #318, and its files.
  if (method === 'GET' && path === `/v1/projects/${PROJECT}/pulls/318`) {
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
  if (method === 'GET' && path.startsWith(`/v1/projects/${PROJECT}/pulls/318/files`)) {
    return { status: 200, body: JSON.stringify({ files: [] } satisfies T.PullRequestFiles), contentType: 'application/json' };
  }
  const verdict = method === 'POST' && /^\/v1\/agents\/[^/]+\/([^/]+)\/review$/.exec(path);
  if (!verdict) return undefined;
  waiting = waiting.filter((i) => i.agent !== verdict[1]);
  return { status: 204, body: '', contentType: 'application/json' };
}

export function ReviewPreview() {
  const view: View = { kind: 'project', project: PROJECT };
  return (
    <div style={{ display: 'flex', height: '100vh', width: '100vw' }}>
      <Sidebar view={view} onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', background: 'var(--color-ink)' }} data-preview-review>
        <TopBar view={view} onSelect={() => {}} onOpenNav={() => {}} onNewAgent={() => {}} onOpenNotice={() => {}} />
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
