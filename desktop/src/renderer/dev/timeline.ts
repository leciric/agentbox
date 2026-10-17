// Timelines for the preview (components/timeline): what the daemon's
// GET …/timeline (internal/timeline) would answer for three of the fixture
// agents, one done with a recording of its check on the desktop, one whose
// fix left CI red, and one still at work, all within the last hour so they
// make up Today too. The recording is drawn on a canvas as the page loads
// (it takes as long to make as it lasts), from the same made-up screens as
// the screenshots; until it's ready the player shows nothing for it.
import type * as T from '../../shared/api';
import { devRequests, PROJECT, setMediaFile } from './fixtures';
import { screen } from './notifications';

const sec = 1000;
const min = 60 * sec;
const at = (ago: number) => new Date(Date.now() - ago).toISOString();
const recordingLength = 24;

type Step = Omit<T.TimelineStep, 'start' | 'end' | 'item'> & { from: number; to: number };
const steps = (ref: string, list: Step[]): T.TimelineStep[] =>
  list.map(({ from, to, ...s }, i) => ({ ...s, start: at(from), end: at(to), item: `${ref}-${i}` }));

function shot(agent: string, id: string, name: string, hue: number, ago: number, dark: boolean): T.MediaItem {
  setMediaFile(id, screen(name, hue, dark));
  return { id, agent: `${PROJECT}/${agent}`, agentName: agent, kind: 'screenshot', name, file: `${name}.png`, mime: 'image/png', size: 210_000, source: 'agent', meta: { width: 1280, height: 800 }, createdAt: at(ago) };
}

function timelines(dark: boolean): Record<string, T.Timeline> {
  const rec: T.MediaItem = {
    id: 'tl-rec',
    agent: `${PROJECT}/agent-12`,
    agentName: 'agent-12',
    kind: 'recording',
    name: 'new-project-button-walkthrough',
    file: 'new-project-button-walkthrough.webm',
    mime: 'video/webm',
    size: 2_100_000,
    source: 'agent',
    meta: { duration: recordingLength, width: 1280, height: 800, target: 'display' },
    createdAt: at(15 * min + 50 * sec),
  };
  const a12 = steps(`${PROJECT}/agent-12`, [
    { kind: 'task', title: 'Add a "New project" button at the top of the sidebar, with a tooltip and Ctrl+Shift+N.', lines: ['Add a "New project" button at the top of the sidebar, with a tooltip and Ctrl+Shift+N.', 'Show it working with a recording, then open a pull request.'], from: 55 * min, to: 55 * min },
    { kind: 'read', title: '', calls: 7, paths: ['desktop/src/renderer/components/v2/SidebarV2.tsx', 'desktop/src/renderer/components/AddProjectDialog.tsx', 'desktop/src/renderer/lib/shortcuts.ts', 'desktop/src/shared/i18n/en-US.ts'], from: 54.5 * min, to: 52 * min },
    { kind: 'plan', title: '', lines: ['[x] A button in the sidebar header, calling onAddProject', '[x] Ctrl+Shift+N, in the shortcut sheet too', '[x] Strings in en-US and pt-BR', '[x] Record it on the desktop'], from: 52 * min, to: 51.5 * min },
    {
      kind: 'edit',
      title: '',
      calls: 9,
      paths: ['desktop/src/renderer/components/v2/SidebarV2.tsx', 'desktop/src/renderer/components/v2/NewProjectButton.tsx', 'desktop/src/renderer/lib/shortcuts.ts', 'desktop/src/shared/i18n/en-US.ts'],
      added: 138,
      removed: 17,
      lines: ['--- desktop/src/renderer/components/v2/SidebarV2.tsx', '-      <span className="text-[13.5px] font-semibold">{t(\'v2.sidebar.projects\')}</span>', '+      <span className="min-w-0 flex-1 truncate text-[13.5px] font-semibold">{t(\'v2.sidebar.projects\')}</span>', '+      <NewProjectButton onAdd={onAddProject} />', '--- desktop/src/renderer/lib/shortcuts.ts', "+  newProject: 'mod+shift+n',"],
      from: 51 * min,
      to: 38 * min,
    },
    { kind: 'test', title: 'npm --prefix desktop test', status: 'failed', calls: 1, lines: ['$ npm --prefix desktop test', '▶ i18n', "  ✖ pt-BR has every key: 'v2.sidebar.newProject.tip' is missing", 'ℹ tests 642', 'ℹ pass 641', 'ℹ fail 1'], from: 37 * min, to: 35 * min },
    { kind: 'edit', title: '', calls: 1, paths: ['desktop/src/shared/i18n/pt-BR.ts'], added: 2, removed: 0, lines: ['--- desktop/src/shared/i18n/pt-BR.ts', "+  'v2.sidebar.newProject': 'Novo projeto',", "+  'v2.sidebar.newProject.tip': 'Adicionar um projeto (Ctrl+Shift+N)',"], from: 35 * min, to: 34 * min },
    { kind: 'test', title: 'npm --prefix desktop test', status: 'passed', calls: 2, lines: ['$ npm --prefix desktop test', '▶ i18n', '  ✔ pt-BR has every key (41ms)', 'ℹ tests 642', 'ℹ pass 642', 'ℹ fail 0'], from: 34 * min, to: 31 * min },
    { kind: 'run', title: 'npm --prefix desktop run build', calls: 1, lines: ['$ npm --prefix desktop run build', 'vite v8.1.0 building for production...', '✓ 2140 modules transformed.', '✓ built in 9.84s'], from: 30 * min, to: 28 * min },
    {
      kind: 'desktop',
      title: 'Record the New project button working in the app',
      calls: 14,
      media: [shot('agent-12', 'tl-before', 'Sidebar, before', 262, 16 * min, dark), rec, shot('agent-12', 'tl-after', 'Sidebar with New project', 150, 15 * min + 33 * sec, dark), shot('agent-12', 'tl-dialog', 'Add a project', 200, 15 * min, dark)],
      from: 16 * min + 10 * sec,
      to: 15 * min,
    },
    { kind: 'page', title: 'New project button: before and after', calls: 1, pages: [{ title: 'New project button: before and after', item: 'tl-page-12' }], from: 12 * min, to: 11 * min },
    { kind: 'pr', title: "gh pr create --title 'feat: a \"New project\" button at the top of the sidebar'", calls: 2, lines: ["$ gh pr create --title 'feat: a \"New project\" button at the top of the sidebar'", 'https://github.com/leciric/agentbox/pull/318'], from: 10 * min, to: 9 * min },
  ]);
  const a41 = steps(`${PROJECT}/agent-41`, [
    { kind: 'task', title: 'Release CI failed on the tag: find what failed and fix it.', lines: ['Release CI failed on the tag: find what failed and fix it.'], from: 48 * min, to: 48 * min },
    { kind: 'run', title: 'gh run view 18233101 --log-failed', calls: 3, lines: ['$ gh run view 18233101 --log-failed', 'mac  Upload  ##[error] AgentBox-0.14.0-arm64-mac.zip: not found', 'release  Publish  ##[error] Process completed with exit code 1.'], from: 47 * min, to: 44 * min },
    { kind: 'read', title: '', calls: 4, paths: ['.github/workflows/release.yml', 'scripts/release-build.sh'], from: 44 * min, to: 42 * min },
    { kind: 'edit', title: '', calls: 2, paths: ['.github/workflows/release.yml'], added: 12, removed: 3, lines: ['--- .github/workflows/release.yml', '-    needs: [linux, windows]', '+    needs: [linux, windows, mac]', '+    if: needs.mac.result == \'success\''], from: 42 * min, to: 40 * min },
    { kind: 'pr', title: "gh pr create --title 'fix: the release build waits for the signed Mac zip'", calls: 1, lines: ["$ gh pr create --title 'fix: the release build waits for the signed Mac zip'"], from: 40 * min, to: 39 * min },
    { kind: 'test', title: 'gh pr checks 312 --watch', status: 'failed', calls: 1, media: [shot('agent-41', 'tl-ci', 'Release workflow: Windows red', 20, 38 * min, dark)], lines: ['$ gh pr checks 312 --watch', '✓ linux  ✓ mac', 'X windows  TestDaemonRestart (flaky): 1 failed'], from: 39 * min, to: 33 * min },
  ]);
  const a96 = steps(`${PROJECT}/agent-96`, [
    { kind: 'task', title: 'The agent rail overflows on wide text: long paths, URLs and stack traces push it wider.', lines: ['The agent rail overflows on wide text: long paths, URLs and stack traces push it wider.'], from: 21 * min, to: 21 * min },
    { kind: 'run', title: 'npm --prefix desktop run preview -- --shots out --only rail-', calls: 1, lines: ['$ npm --prefix desktop run preview -- --shots out --only rail-', 'overflow: rail-long-path 412px > 340px', 'overflow: rail-stack-trace 518px > 340px'], from: 20 * min, to: 18 * min },
    { kind: 'edit', title: '', calls: 5, paths: ['desktop/src/renderer/components/AgentRail.tsx', 'desktop/src/renderer/components/chat/Markdown.tsx'], added: 11, removed: 6, lines: ['--- desktop/src/renderer/components/AgentRail.tsx', '-  <div className="flex items-center gap-2">', '+  <div className="flex min-w-0 items-center gap-2">'], from: 17 * min, to: 6 * min },
    { kind: 'test', title: 'npm --prefix desktop test', status: 'running', calls: 1, lines: ['$ npm --prefix desktop test', '▶ AgentRail', '  ✔ a long path wraps (3ms)'], from: 5 * min, to: 1 * min },
  ]);
  const of = (list: T.TimelineStep[], working = false): T.Timeline => ({ steps: list, start: list[0]!.start, end: list[list.length - 1]!.end, working });
  return { [`${PROJECT}/agent-12`]: of(a12), [`${PROJECT}/agent-41`]: of(a41), [`${PROJECT}/agent-96`]: of(a96, true) };
}

// recording draws the agent's walk through the app: the sidebar as it was,
// the cursor going to the new button, the button lit, and the dialog it opens.
async function recording(dark: boolean): Promise<string> {
  const load = (src: string) => new Promise<HTMLImageElement>((resolve) => Object.assign(new Image(), { src, onload: function (this: HTMLImageElement) { resolve(this); } }));
  const [before, after, dialog] = await Promise.all([load(screen('Sidebar, before', 262, dark)), load(screen('Sidebar with New project', 150, dark)), load(screen('Add a project', 200, dark))]);
  const canvas = Object.assign(document.createElement('canvas'), { width: 960, height: 600 });
  const ctx = canvas.getContext('2d')!;
  const recorder = new MediaRecorder(canvas.captureStream(24), { mimeType: 'video/webm' });
  const chunks: Blob[] = [];
  recorder.ondataavailable = (e) => chunks.push(e.data);
  const done = new Promise<void>((resolve) => (recorder.onstop = () => resolve()));
  recorder.start();
  const start = performance.now();
  // Where the cursor goes, as fractions of the frame, and when (seconds).
  const path = [[0.7, 0.75, 0], [0.12, 0.21, 6], [0.12, 0.21, 9], [0.55, 0.6, 15], [0.85, 0.89, 20], [0.85, 0.89, 24]] as const;
  await new Promise<void>((resolve) => {
    const frame = () => {
      const s = (performance.now() - start) / 1000;
      ctx.drawImage(s < 9 ? before : s < 16 ? after : dialog, 0, 0, 960, 600);
      let k = 0;
      while (k < path.length - 2 && path[k + 1]![2] <= s) k++;
      const [x0, y0, t0] = path[k]!;
      const [x1, y1, t1] = path[k + 1]!;
      const f = Math.min(1, Math.max(0, (s - t0) / (t1 - t0 || 1)));
      const e = f < 0.5 ? 2 * f * f : 1 - (-2 * f + 2) ** 2 / 2;
      const x = (x0 + (x1 - x0) * e) * 960;
      const y = (y0 + (y1 - y0) * e) * 600;
      // A click where the cursor stops: on the button, then in the dialog.
      for (const c of [9, 20]) {
        const since = s - c;
        if (since > 0 && since < 0.6) {
          ctx.strokeStyle = `rgba(255,255,255,${1 - since / 0.6})`;
          ctx.lineWidth = 3;
          ctx.beginPath();
          ctx.arc(x, y, 10 + since * 40, 0, 2 * Math.PI);
          ctx.stroke();
        }
      }
      ctx.fillStyle = '#fff';
      ctx.strokeStyle = '#000';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.moveTo(x, y);
      ctx.lineTo(x, y + 22);
      ctx.lineTo(x + 6, y + 16);
      ctx.lineTo(x + 11, y + 25);
      ctx.lineTo(x + 14, y + 23);
      ctx.lineTo(x + 9, y + 15);
      ctx.lineTo(x + 17, y + 15);
      ctx.closePath();
      ctx.fill();
      ctx.stroke();
      if (s < recordingLength) requestAnimationFrame(frame);
      else resolve();
    };
    frame();
  });
  recorder.stop();
  await done;
  return URL.createObjectURL(new Blob(chunks, { type: 'video/webm' }));
}

// seedTimelines answers the timeline route for the fixture agents (an empty
// one for the rest), and starts drawing the recording.
export function seedTimelines(): void {
  const dark = document.documentElement.dataset.appearance !== 'light';
  const all = timelines(dark);
  devRequests.push((method, path) => {
    const m = method === 'GET' ? /^\/v1\/agents\/([^/]+)\/([^/]+)\/timeline$/.exec(path) : null;
    if (!m) return undefined;
    const ref = `${decodeURIComponent(m[1]!)}/${decodeURIComponent(m[2]!)}`;
    return { status: 200, body: JSON.stringify(all[ref] ?? { steps: [], start: '', end: '' }), contentType: 'application/json' };
  });
  void recording(dark).then((url) => setMediaFile('tl-rec', url));
}
