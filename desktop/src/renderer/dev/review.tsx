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
import { buildFixtures, devRequests, pageOf, patchDevAgent, PROJECT, setMediaFile } from './fixtures';
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
    // agent-15 and agent-17 recorded what they checked, notes marking each
    // check (evidence() below): the lead chat's evidence cards in v2.
    item('agent-15', 'Settings tabs no longer clip at 1024px', 'agentbox/settings-tabs-clip', 2 * min, {
      pr: { ...pr, number: 371, title: 'fix: Settings’ section list fits at 1024px and in pt-BR', additions: 20, deletions: 4, headBranch: 'agentbox/settings-tabs-clip' },
      summary:
        'The section list was a fixed 248px column with nowrap labels, so pt-BR’s longer names pushed the page past the window. It is now `minmax(180px, 248px)` and labels wrap.\n\nChecked dark and light, en-US and pt-BR, at 1024×640 and 800×600.',
      changes: { files: 2, insertions: 20, deletions: 4, dirty: false },
    }),
    item('agent-17', 'Markdown renders in Ctrl+K and sidebar previews', 'agentbox/markdown-previews', 8 * min, {
      ai: 'codex',
      summary: 'Previews now go through the inline Markdown renderer, clamped to two lines. Not pushed yet: waiting on your look before opening a PR.',
      changes: { files: 1, insertions: 3, deletions: 2, dirty: true },
    }),
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

// settingsDiff is agent-15's pull request, #371, file by file.
const settingsDiff: Record<string, string> = {
  'desktop/src/renderer/components/SettingsPage.tsx': [
    '@@ -88,9 +88,11 @@ export function SettingsPage({ section }: { section: Section }) {',
    '   return (',
    '-    <div className="grid h-full grid-cols-[248px_1fr]">',
    '+    <div className="grid h-full grid-cols-[minmax(180px,248px)_minmax(0,1fr)]">',
    '       <nav className="overflow-y-auto border-r border-line p-2">',
    '-        {sections.map((s) => <NavRow key={s.id} {...s} className="whitespace-nowrap" />)}',
    '+        {sections.map((s) => (',
    '+          <NavRow key={s.id} {...s} className="min-w-0 text-balance leading-tight" />',
    '+        ))}',
    '       </nav>',
  ].join('\n'),
  'desktop/src/renderer/dev/scenarios.json': [
    '@@ -1,3 +1,17 @@',
    ' [',
    '+  { "id": "settings-1024-pt", "query": "settings=models&lang=pt-BR", "width": 1024, "height": 640 },',
    '+  { "id": "settings-800", "query": "settings=accounts", "width": 800, "height": 600 },',
  ].join('\n'),
};

// crowded is ?review=media: agent-12 with six recordings and four screenshots,
// the first recording's checks titled at length, for the strip and the
// "What it checked" column to be squeezed (a long strip must scroll on its
// own, the column wraps).
function crowded(): T.MediaItem[] {
  const items = evidence('agent-15', 'settings', 4 * min, 17, [
    [0.3, 'General at 1024×640 with the search box, the long section list and every tab open at once', 'Nothing past the right edge; the search box keeps its width and the Unbrokenly_long_identifier_that_would_not_wrap_unless_told_to stays inside.'],
    [5.2, 'Models in pt-BR: the longest labels wrap to two lines instead of clipping', ''],
    [9.5, 'Resources, light theme', ''],
    [13.5, 'Accounts at 800×600, the narrowest window the app allows', 'No horizontal scroll.'],
  ]);
  for (let i = 2; i <= 6; i++) {
    setMediaFile(`agent-15-rec${i}`, new URL('./concepts/media/settings.webm', import.meta.url).href);
    items.push({ ...items[0], id: `agent-15-rec${i}`, name: `walkthrough-${i}-at-another-size`, createdAt: ago((4 + i) * min) });
  }
  return items;
}

// lots is ?review=lots: agent-15 with 36 pieces of proof, every third a
// recording and the rest screenshots, for the modal to stay inside the window
// (the strip scrolls or wraps in a bounded area, the body and footer stay
// reachable).
function lots(): T.MediaItem[] {
  const items = crowded();
  const shot = items.find((m) => m.kind === 'screenshot');
  for (let i = items.length; i < 36; i++) {
    const id = `agent-15-lot${i}`;
    if (i % 3 === 0) {
      setMediaFile(id, new URL('./concepts/media/settings.webm', import.meta.url).href);
      items.push({ ...items[0], id, name: `walkthrough-${i}-at-another-size`, createdAt: ago((4 + i) * min) });
    } else {
      setMediaFile(id, new URL(`./concepts/media/settings-${(i % 4) + 1}.webp`, import.meta.url).href);
      items.push({ ...(shot ?? items[0]), id, kind: 'screenshot', mime: 'image/webp', name: `screen-${i}-settings`, createdAt: ago((4 + i) * min) });
    }
  }
  return items;
}

// evidence is an agent's recording of what it checked, one of the concept's
// (dev/concepts/media, the preview itself recorded), with a note marking each
// check as it began and a screenshot of each: what an agent following the
// brief leaves, which the review cuts into chapters (lib/review.ts proofOf).
function evidence(agent: string, story: string, startedAgo: number, duration: number, checks: [number, string, string][]): T.MediaItem[] {
  const ref = `${PROJECT}/${agent}`;
  const start = Date.now() - startedAgo;
  const at = (s: number) => new Date(start + s * 1000).toISOString();
  const base = { agent: ref, agentName: agent, source: 'agent', size: 0 };
  setMediaFile(`${agent}-rec`, new URL(`./concepts/media/${story}.webm`, import.meta.url).href);
  const items: T.MediaItem[] = [{ ...base, id: `${agent}-rec`, kind: 'recording', name: `${story}-walkthrough`, mime: 'video/webm', meta: { duration }, createdAt: at(0) }];
  checks.forEach(([s, title, more], i) => {
    items.push({ ...base, id: `${agent}-note-${i}`, kind: 'note', name: title, text: more ? `${title}\n${more}` : title, meta: {}, createdAt: at(s) });
    setMediaFile(`${agent}-shot-${i}`, new URL(`./concepts/media/${story}-${i + 1}.webp`, import.meta.url).href);
    items.push({ ...base, id: `${agent}-shot-${i}`, kind: 'screenshot', name: title.toLowerCase().replace(/[^a-z0-9]+/g, '-'), mime: 'image/webp', meta: { width: 1024, height: 640 }, createdAt: at(s + 1.5) });
  });
  return items;
}

// seedEvidence is the lead chat as agent-15 and agent-17 finishing leave it
// in v2: the notice the daemon writes the lead, which names them (shown as
// their evidence cards), and the lead's answer.
export function seedEvidence(queryClient: QueryClient): void {
  const key = ['chat', `${PROJECT}/lead`];
  const thread = queryClient.getQueryData<T.ChatThread>(key);
  if (!thread) return;
  const now = new Date().toISOString();
  const notice = (id: string, agents: string[], ms: number): T.ChatItem => ({ id, turn: 'l1', kind: 'notice', hidden: true, finished: agents.map((a) => `${PROJECT}/${a}`), text: `${agents.join(', ')} finished.`, createdAt: ago(ms), updatedAt: ago(ms) });
  const said = (id: string, text: string, ms: number): T.ChatItem => ({ id, turn: 'l1', kind: 'assistant', text, createdAt: ago(ms), updatedAt: ago(ms) });
  queryClient.setQueryData<T.ChatThread>(key, {
    ...thread,
    items: [
      ...thread.items,
      notice('ev17', ['agent-17'], 8 * min),
      said('ev17a', 'agent-17 finished the markdown previews. It hasn’t opened a PR yet: its diff is on the review’s Changes tab.', 8 * min - 5_000),
      notice('ev15', ['agent-15'], 4 * min),
      notice('ev15b', ['agent-15'], 2 * min),
      said('ev15a', 'agent-15 fixed Settings’ clipped tabs (item 8 of the UX report). PR #371 is green, and its recording walks all four sizes.', 2 * min - 5_000),
    ].map((it) => ({ ...it, updatedAt: it.updatedAt || now })),
  });
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
  queryClient.setQueryData(['media', `${PROJECT}/agent-15`], new URLSearchParams(location.search).get('review') === 'media' ? crowded() : new URLSearchParams(location.search).get('review') === 'lots' ? lots() : evidence('agent-15', 'settings', 4 * min, 17, [
    [0.3, 'General at 1024×640', 'Nothing past the right edge; the search box keeps its width.'],
    [5.2, 'Models in pt-BR', 'The longest labels wrap to two lines instead of clipping.'],
    [9.5, 'Resources, light theme', ''],
    [13.5, 'Accounts at 800×600', 'The narrowest window the app allows: no horizontal scroll.'],
  ]));
  queryClient.setQueryData(['media', `${PROJECT}/agent-17`], evidence('agent-17', 'markdown', 10 * min, 14, [
    [0.3, 'Rich blocks in the lead chat', ''],
    [5.7, 'The same in a 340px column', 'Long code lines scroll inside their block.'],
    [9.8, 'Sidebar previews', 'Inline markdown, two lines, no raw asterisks.'],
  ]));
  void recording().then((url) => setMediaFile('rv-rec', url));
  devRequests.push(reviewRequest);
}

// notes are each agent's notes on its diff (components/ReviewNotes.tsx), as
// the daemon would keep them: drafts until Request changes sends them, then
// pending, and the first resolved when the agent comes back with new work,
// as if its code had changed.
const notes = new Map<string, T.ReviewNote[]>();
let noteSeq = 0;

function notesRequest(method: string, route: string, body: unknown): { status: number; body: string; contentType: string } | undefined {
  const m = /^\/v1\/agents\/[^/]+\/([^/]+)\/review-notes(?:\/(\d+))?$/.exec(route);
  if (!m) return undefined;
  const [, agent, id] = m;
  const list = notes.get(agent) ?? [];
  const json = (status: number, v: unknown) => ({ status, body: v === undefined ? '' : JSON.stringify(v), contentType: 'application/json' });
  if (method === 'GET' && !id) return json(200, list);
  if (method === 'POST' && !id) {
    const req = body as T.AddReviewNoteRequest;
    const note: T.ReviewNote = { ...req, id: ++noteSeq, status: 'draft', createdAt: new Date().toISOString() };
    notes.set(agent, [...list, note]);
    return json(201, note);
  }
  const note = list.find((n) => n.id === Number(id));
  if (!note) return json(404, { error: 'no such note' });
  if (method === 'DELETE') {
    notes.set(agent, list.filter((n) => n !== note));
    return json(204, undefined);
  }
  const edit = body as T.EditReviewNoteRequest;
  if (edit.body !== undefined) note.body = edit.body;
  if (edit.resolved !== undefined) {
    note.status = edit.resolved ? 'resolved' : 'pending';
    note.resolvedAt = edit.resolved ? new Date().toISOString() : undefined;
  }
  return json(200, note);
}

// reviewRequest answers the dev bridge for "Ready to review": the list, and
// a verdict, which takes the agent off it.
function reviewRequest(method: string, path: string, body?: unknown): { status: number; body: string; contentType: string } | undefined {
  // The top bar re-reads the Claude limits every 30s, past what was seeded.
  if (method === 'GET' && path === '/v1/limits') return { status: 200, body: '[]', contentType: 'application/json' };
  const [route, query] = path.split('?');
  const noted = notesRequest(method, route, body);
  if (noted) return noted;
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
  // #371's files and their diffs, for the review's Changes tab; the others have none.
  if (method === 'GET' && route === `/v1/projects/${PROJECT}/pulls/371/files`) {
    const files: T.PullFile[] = Object.entries(settingsDiff).map(([path, patch]) => ({ path, status: 'modified', additions: patch.split('\n').filter((l) => l.startsWith('+')).length, deletions: patch.split('\n').filter((l) => l.startsWith('-')).length, hasDiff: true }));
    return { status: 200, body: JSON.stringify({ files } satisfies T.PullRequestFiles), contentType: 'application/json' };
  }
  if (method === 'GET' && route === `/v1/projects/${PROJECT}/pulls/371/diff`) {
    const path = new URLSearchParams(query).get('path') ?? '';
    return { status: 200, body: JSON.stringify({ path, patch: settingsDiff[path] ?? '' } satisfies T.PullFileDiff), contentType: 'application/json' };
  }
  if (method === 'GET' && /^\/v1\/projects\/[^/]+\/pulls\/\d+\/files/.test(route)) {
    return { status: 200, body: JSON.stringify({ files: [] } satisfies T.PullRequestFiles), contentType: 'application/json' };
  }
  // Request changes (UI v2): the agent works on them for 6s, then is back in
  // Ready to review with new work.
  const changes = method === 'POST' && /^\/v1\/agents\/[^/]+\/([^/]+)\/request-changes$/.exec(path);
  if (changes) {
    const agent = changes[1];
    const req = body as T.RequestChangesRequest | undefined;
    const text = req?.text ?? '';
    const sentNow = (notes.get(agent) ?? []).filter((n) => req?.notes?.includes(n.id));
    for (const n of sentNow) Object.assign(n, { status: 'pending', sentAt: new Date().toISOString() });
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
      if (sentNow[0]) sentNow[0].status = 'resolved';
      void client?.invalidateQueries({ queryKey: ['reviewNotes'] });
      decided = decided.filter((i) => i.agent !== agent);
      if (item) waiting = [{ ...item, verdict: undefined, reviewedAt: undefined, finishedAt: new Date().toISOString(), fingerprint: `{"f":"${agent}-again"}`, summary: `Made the changes you asked for: ${text}` }, ...waiting];
      void client?.invalidateQueries({ queryKey: ['review'] });
      void client?.invalidateQueries({ queryKey: ['reviewed'] });
    }, 6_000);
    const reply: T.ChatItem = { id: `rc-${Date.now()}`, turn: '', kind: 'user', text, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() };
    return { status: 202, body: JSON.stringify(reply), contentType: 'application/json' };
  }
  // agent-17's diff: it opened no pull request.
  if (method === 'GET' && route === `/v1/agents/${PROJECT}/agent-17/diff`) {
    const diff = [
      'diff --git a/desktop/src/renderer/components/v2/SidebarV2.tsx b/desktop/src/renderer/components/v2/SidebarV2.tsx',
      '--- a/desktop/src/renderer/components/v2/SidebarV2.tsx',
      '+++ b/desktop/src/renderer/components/v2/SidebarV2.tsx',
      '@@ -12,7 +12,8 @@ import { cn } from "../../lib/utils";',
      '+import { InlineMarkdown } from "../chat/Markdown";',
      '   <span className="line-clamp-2 text-[12px] text-subtle">',
      '-    {plain(preview)}',
      '+    <InlineMarkdown text={preview} />',
      '   </span>',
    ].join('\n');
    return { status: 200, body: diff, contentType: 'text/plain' };
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
  landReview(queryClient, { project: PROJECT, agent: 'agent-77', ref: `${PROJECT}/agent-77`, title: 'Fix the flaky reconnect test', branch: 'agentbox/flaky-reconnect', ai: 'claude', finishedAt: new Date().toISOString(), fingerprint: `{"f":"agent-77"}` });
}

// landReview puts an agent at the front of those waiting for review, for a
// scenario where one finishes while it's on screen (dev/concepts/pip.tsx).
export function landReview(queryClient: QueryClient, item: T.ReviewItem) {
  waiting = [item, ...waiting];
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

// seedV2 is UI v2's sidebar with more to order (&v2=1): five more projects,
// each with when the user last wrote to it (two of them long enough ago to
// be on the Settled shelf), their icons (an image found in the repository,
// emoji chosen, a letter), quill's lead mid-turn, and the answers for
// changing an icon. ?pins=agentbox,storefront pins those, in that order.
export function seedV2(queryClient: QueryClient, pins: string | null): void {
  const day = 24 * 60 * min;
  const base = buildFixtures().projects;
  const extra: [string, string, number][] = [
    ['storefront', 'Storefront', 2 * 60 * min],
    ['quill', 'Quill', 30 * min],
    ['courier', 'Courier', 1 * day],
    ['old-blog', 'Old blog', 9 * day],
    ['dotfiles', 'dotfiles', 20 * day],
  ];
  // &idle=12: that many more projects with nothing going on, each lead with a
  // last line, some of it markdown, for the Idle list at full length.
  const idleCount = Number(new URLSearchParams(location.search).get('idle') ?? 0);
  const idleLines = ['**Merged** #214: the `retry` loop backs off now.', 'Done. | file | change |\n|---|---|\n| a.ts | b |', 'You: can you look at the flaky e2e test on CI?', '- Bumped the SDK\n- Fixed the lint', 'Opened https://github.com/example/a-very-long-repository-name/pull/1234 for review.'];
  for (let i = 1; i <= idleCount; i++) extra.push([`idle-${i}`, `Idle project ${i}`, (i + 2) * day]);
  let projects: T.Project[] = [...base, ...extra.map(([name, displayName], i) => ({ ...base[0], name, displayName, root: `/home/me/src/${name}`, iconVersion: 1, position: base.length + i + 1, createdAt: ago(60 * day) }))];
  const lastSaid: Record<string, number> = { [PROJECT]: 3 * min, 'a-project-with-a-name-so-long-it-should-never-be-allowed-to-widen-the-sidebar': 3 * 60 * min, ...Object.fromEntries(extra.map(([n, , ms]) => [n, ms])) };
  const logo = `data:image/svg+xml;base64,${btoa('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#6d5dfc"/><path d="M9 22 16 8l7 14z" fill="#fff"/></svg>')}`;
  const icons: Record<string, T.ProjectIcon> = {
    [PROJECT]: { source: 'detected', image: logo, detectedFrom: 'desktop/build/icon.svg', hasDetected: true },
    storefront: { source: 'chosen', emoji: '🛍️', hasDetected: false },
    quill: { source: 'chosen', emoji: '🪶', hasDetected: false },
    courier: { source: 'chosen', emoji: '📦', hasDetected: false },
    'old-blog': { source: 'chosen', emoji: '📝', hasDetected: false },
  };
  // &sections=1: the user's sections (lib/sidebar.ts, Classic's), Work, Side
  // projects and an empty Later, kept as the daemon keeps them until the page
  // reloads: moves, renames, folds, new ones and deletes. Without it, none.
  const createdAt = ago(30 * day);
  let sections: T.Section[] =
    new URLSearchParams(location.search).get('sections') === '1'
      ? [
          { id: 'work', name: 'Work', position: 1, collapsed: false, createdAt },
          { id: 'side', name: 'Side projects', position: 2, collapsed: false, createdAt },
          { id: 'later', name: 'Later', position: 3, collapsed: false, createdAt },
        ]
      : [];
  const placed: Record<string, [string, number]> = sections.length ? { [PROJECT]: ['work', 1], quill: ['work', 2], courier: ['work', 3], 'old-blog': ['side', 1], dotfiles: ['side', 2] } : {};
  projects = projects.map((p, i) => ({ ...p, section: placed[p.name]?.[0] ?? '', position: placed[p.name]?.[1] ?? i + 1 }));
  const daemonOrder = () => {
    const rank = (p: T.Project) => (p.section ? sections.findIndex((x) => x.id === p.section) : sections.length);
    projects = [...projects].sort((a, b) => rank(a) - rank(b) || a.position - b.position);
  };
  daemonOrder();
  queryClient.setQueryData(['projects'], projects);
  queryClient.setQueryData(['sections'], sections);
  if (pins) localStorage.setItem('agentbox.v2.projects', JSON.stringify({ pins: pins.split(',') }));
  const json = (v: unknown) => ({ status: 200, body: JSON.stringify(v), contentType: 'application/json' });
  // The sidebar's hidden projects (&hidden=a,b), kept as the daemon would
  // until the page reloads.
  const hiddenParam = new URLSearchParams(location.search).get('hidden');
  let sidebar: T.SidebarLayout = { hidden: hiddenParam ? hiddenParam.split(',') : [] };
  devRequests.push((method, path, body) => {
    if (path === '/v1/sidebar' && method === 'GET') return json(sidebar);
    if (path === '/v1/sidebar' && method === 'PUT') {
      sidebar = body as T.SidebarLayout;
      return json(sidebar);
    }
    if (method === 'GET' && path === '/v1/projects') return json(projects);
    if (method === 'GET' && path === '/v1/sections') return json(sections);
    if (method === 'POST' && path === '/v1/sections') {
      const made: T.Section = { id: `s${Date.now()}`, name: (body as T.AddSectionRequest).name, position: sections.length + 1, collapsed: false, createdAt: new Date().toISOString() };
      sections = [...sections, made];
      return json(made);
    }
    const sectionId = /^\/v1\/sections\/([^/]+)$/.exec(path)?.[1];
    if (sectionId && method === 'PATCH') {
      sections = sections.map((x) => (x.id === decodeURIComponent(sectionId) ? { ...x, ...(body as T.UpdateSectionRequest) } : x));
      return json(sections.find((x) => x.id === decodeURIComponent(sectionId)));
    }
    if (sectionId && method === 'DELETE') {
      sections = sections.filter((x) => x.id !== decodeURIComponent(sectionId)).map((x, i) => ({ ...x, position: i + 1 }));
      projects = projects.map((p) => (p.section === decodeURIComponent(sectionId) ? { ...p, section: '' } : p));
      daemonOrder();
      return json(null);
    }
    if (method === 'PUT' && path === '/v1/projects/layout') {
      const order = body as T.ProjectLayout;
      const named = new Map(projects.map((p) => [p.name, p]));
      const lists: [string, string[]][] = [...order.sections.map((x): [string, string[]] => [x.id, x.projects]), ['', order.loose]];
      projects = lists.flatMap(([section, names]) => names.flatMap((name, i) => (named.has(name) ? [{ ...named.get(name)!, section, position: i + 1 }] : [])));
      sections = order.sections.flatMap((x, i) => sections.filter((y) => y.id === x.id).map((y) => ({ ...y, position: i + 1 })));
      return json(projects);
    }
    // Only agentbox has agents waiting; the other projects' lists are empty pages.
    if (method === 'GET' && /^\/v1\/projects\/[^/]+\/review/.test(path) && !path.startsWith(`/v1/projects/${PROJECT}/`)) return json(pageOf([]));
    // The extra projects' leads and lists, which no other fixture answers.
    const own = /^\/v1\/projects\/([^/?]+)\/([^?]*)/.exec(path);
    const extraName = own && extra.some(([n]) => n === decodeURIComponent(own[1])) ? decodeURIComponent(own[1]) : null;
    if (method === 'GET' && extraName && own![2] === 'lead') return json(queryClient.getQueryData(['projectChat', extraName]) ?? { project: extraName, ref: `${extraName}/lead`, started: true, chat: 'ready' });
    if (method === 'GET' && extraName && own![2] === 'chat/runners') return json({ tool: 'claude', runners: [{ tool: 'claude', name: 'Claude Code', ready: true, installed: true }] });
    if (method === 'GET' && extraName && own![2] !== 'icon') return json([]);
    if (method === 'GET' && path.startsWith('/v1/skills')) return json([]);
    if (method === 'GET' && path === '/v1/project-activity') return json(projects.map((p) => ({ project: p.name, lastActivity: ago(lastSaid[p.name] ?? 10 * day), lastMessage: ago(lastSaid[p.name] ?? 10 * day) })));
    const icon = /^\/v1\/projects\/([^/]+)\/icon$/.exec(path);
    if (!icon) return undefined;
    const name = decodeURIComponent(icon[1]);
    if (method === 'PUT') {
      const req = body as T.SetProjectIconRequest;
      icons[name] = { source: 'chosen', emoji: req.emoji, image: req.image, hasDetected: name === PROJECT };
      projects.forEach((p) => p.name === name && (p.iconVersion += 1));
    }
    if (method === 'DELETE') {
      icons[name] = name === PROJECT ? { source: 'detected', image: logo, detectedFrom: 'desktop/build/icon.svg', hasDetected: true } : { source: 'none' };
      projects.forEach((p) => p.name === name && (p.iconVersion += 1));
    }
    return json(icons[name] ?? { source: 'none' });
  });
  for (let i = 1; i <= idleCount; i++) {
    const text = idleLines[(i - 1) % idleLines.length];
    queryClient.setQueryData<T.ProjectChat>(['projectChat', `idle-${i}`], { project: `idle-${i}`, ref: `idle-${i}/lead`, started: true, chat: 'ready', last: { kind: text.startsWith('You: ') ? 'user' : 'assistant', text: text.replace(/^You: /, ''), at: ago((i + 2) * day) } });
  }
  queryClient.setQueryData<T.ProjectChat>(['projectChat', 'quill'], { project: 'quill', ref: 'quill/lead', started: true, chat: 'running', last: { kind: 'assistant', text: 'Reading the editor’s undo stack before I split it.', at: ago(1 * min) } });
  queryClient.setQueryData<T.ProjectChat>(['projectChat', 'storefront'], { project: 'storefront', ref: 'storefront/lead', started: true, chat: 'ready', last: { kind: 'user', text: 'Ship the checkout copy once courier agrees on the address format', at: ago(2 * 60 * min) } });
}
