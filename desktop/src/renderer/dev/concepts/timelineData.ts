// The made-up day the timeline concept tells (?concept=timeline): five
// agents' sessions, each a run of steps over one continuous desktop
// recording, with the screenshots, clips and Hatch pages they kept placed at
// the second they were taken. Mock only.

export type StepKind = 'task' | 'plan' | 'edit' | 'test' | 'desktop' | 'page' | 'pr';

// A step's media. A shot is a screenshot taken at `at` seconds into the
// recording; a clip is a span of it kept as a recording; a page a Hatch page.
export type Media =
  | { kind: 'shot'; at: number; caption: string; screen: Screen }
  | { kind: 'clip'; from: number; to: number; caption: string }
  | { kind: 'page'; at: number; title: string; id: string };

// What a screenshot shows, drawn by shotSvg.
export type Screen =
  | { app: 'review'; footer: 'fixed' | 'scrolled'; menu?: boolean }
  | { app: 'sidebar'; meter?: number; overflow?: boolean; width?: number }
  | { app: 'phone'; stage: 'card' | 'confirm' | 'merged' }
  | { app: 'qa'; failing: number };

export type Step = {
  kind: StepKind;
  title: string;
  at: number; // seconds into the recording
  dur: number;
  lines?: string[]; // what the terminal or the editor shows
  result?: 'pass' | 'fail';
  media?: Media[];
};

export type TAgent = {
  name: string;
  ai: 'claude' | 'codex' | 'opencode';
  task: string;
  started: string; // the clock time its recording starts
  state: 'review' | 'merged' | 'working' | 'draft';
  pr?: { number: number; title: string; checks: 'passing' | 'pending' | 'failing'; files: number; plus: number; minus: number };
  steps: Step[];
};

export const length = (a: TAgent) => {
  const last = a.steps[a.steps.length - 1]!;
  return last.at + last.dur;
};

export const stepAt = (a: TAgent, t: number) => {
  let i = 0;
  for (let k = 0; k < a.steps.length; k++) if (a.steps[k]!.at <= t) i = k;
  return i;
};

export const clock = (a: TAgent, t: number) => {
  const [h, m] = a.started.split(':').map(Number) as [number, number];
  const mins = h * 60 + m + Math.floor(t / 60);
  return `${String(Math.floor(mins / 60) % 24).padStart(2, '0')}:${String(mins % 60).padStart(2, '0')}`;
};

export const mmss = (s: number) => {
  s = Math.max(0, Math.floor(s));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
};

export const agents: TAgent[] = [
  {
    name: 'agent-446',
    ai: 'claude',
    task: 'Review modal: keep the footer fixed and split Merge',
    started: '09:12',
    state: 'review',
    pr: { number: 358, title: 'fix: the review modal’s footer stays put, and Merge is a split button', checks: 'passing', files: 6, plus: 214, minus: 61 },
    steps: [
      { kind: 'task', title: 'Task from the lead', at: 0, dur: 40, lines: ['The review modal’s footer scrolls away on long PRs.', 'Keep it fixed; make Merge a split button (squash, rebase).', 'Show it in the real app with a recording.'] },
      { kind: 'plan', title: 'Read ReadyToReview.tsx, plan three edits', at: 40, dur: 150, lines: ['1. Pull the footer out of the slide scroller', '2. Merge ▾ with squash / rebase / merge commit', '3. Keys: ⌘↵ merges with the last method'] },
      { kind: 'edit', title: 'ReadyToReview.tsx  +142 −58', at: 190, dur: 520, lines: ['-  <div className="overflow-y-auto">', '-    {slides}{footer}', '+  <div className="flex min-h-0 flex-col">', '+    <div className="min-h-0 flex-1 overflow-y-auto">{slides}</div>', '+    <Footer pr={pr} method={method} />', '+  </div>'] },
      { kind: 'edit', title: 'en-US.ts, pt-BR.ts  +18 −3', at: 710, dur: 120, lines: ["+  'review.merge.squash': 'Squash and merge',", "+  'review.merge.rebase': 'Rebase and merge',", "+  'review.merge.squash': 'Squash e mesclar',"] },
      { kind: 'test', title: 'npm test — 214 passed', at: 830, dur: 210, result: 'pass', lines: ['$ npm --prefix desktop test', '▶ ReadyToReview', '  ✔ footer stays outside the scroller (3ms)', '  ✔ split Merge remembers the method (5ms)', '▶ i18n', '  ✔ pt-BR has every key (41ms)', 'ℹ tests 214  pass 214  fail 0'] },
      {
        kind: 'desktop',
        title: 'Checked in the app on the desktop',
        at: 1040,
        dur: 190,
        media: [
          { kind: 'clip', from: 1040, to: 1230, caption: 'Long PR: the footer stays while the slides scroll' },
          { kind: 'shot', at: 1075, caption: 'Before: footer scrolled out of view', screen: { app: 'review', footer: 'scrolled' } },
          { kind: 'shot', at: 1140, caption: 'After: footer fixed', screen: { app: 'review', footer: 'fixed' } },
          { kind: 'shot', at: 1195, caption: 'Merge ▾ open', screen: { app: 'review', footer: 'fixed', menu: true } },
        ],
      },
      { kind: 'pr', title: 'Opened #358', at: 1230, dur: 60, lines: ['fix: the review modal’s footer stays put, and Merge is a split button', '✓ PR title  ✓ desktop lint  ✓ typecheck  ✓ test  ✓ build'] },
    ],
  },
  {
    name: 'agent-447',
    ai: 'codex',
    task: 'Codex usage meter in the sidebar foot',
    started: '09:40',
    state: 'merged',
    pr: { number: 359, title: 'feat: a Codex usage meter in the sidebar’s foot', checks: 'passing', files: 9, plus: 388, minus: 12 },
    steps: [
      { kind: 'task', title: 'Task from the lead', at: 0, dur: 30, lines: ['Show Codex’s 5-hour and weekly use next to Claude’s.', 'Read it from `codex usage --json` in the daemon.'] },
      { kind: 'plan', title: 'Plan: a reader in internal/usage, a meter in the foot', at: 30, dur: 120, lines: ['internal/usage/codex.go: parse, cache 60s', 'api.Usage.Codex → UPDATE_TS=1', 'SidebarV2 foot: two bars'] },
      { kind: 'edit', title: 'internal/usage/codex.go  +164', at: 150, dur: 600, lines: ['+func ReadCodex(ctx context.Context) (Limits, error) {', '+\tout, err := exec.CommandContext(ctx, "codex", "usage", "--json").Output()', '+\tif err != nil {', '+\t\treturn Limits{}, fmt.Errorf("codex usage: %w", err)', '+\t}'] },
      { kind: 'test', title: 'go test ./internal/usage — 1 failed', at: 750, dur: 150, result: 'fail', lines: ['$ go test ./internal/usage', '--- FAIL: TestReadCodexWeekly (0.00s)', '    codex_test.go:48: resets_at: got 0001-01-01, want 2026-10-21', 'FAIL  github.com/leciric/agentbox/internal/usage'] },
      { kind: 'edit', title: 'Parse resets_at as unix seconds  +6 −2', at: 900, dur: 180, lines: ['-\tResetsAt time.Time `json:"resets_at"`', '+\tResetsAt int64     `json:"resets_at"`', '+\tat := time.Unix(raw.ResetsAt, 0)'] },
      { kind: 'test', title: 'go test ./... — ok', at: 1080, dur: 240, result: 'pass', lines: ['$ go test ./internal/usage ./internal/api', 'ok   github.com/leciric/agentbox/internal/usage  0.21s', 'ok   github.com/leciric/agentbox/internal/api    1.84s'] },
      {
        kind: 'desktop',
        title: 'Meter checked at 0%, 64% and 97%',
        at: 1320,
        dur: 160,
        media: [
          { kind: 'clip', from: 1320, to: 1480, caption: 'The meter filling as usage rises' },
          { kind: 'shot', at: 1350, caption: '64% of the 5-hour window', screen: { app: 'sidebar', meter: 0.64 } },
          { kind: 'shot', at: 1440, caption: '97%: the bar turns amber', screen: { app: 'sidebar', meter: 0.97 } },
        ],
      },
      { kind: 'pr', title: 'Opened #359 · merged 11:02', at: 1480, dur: 60, lines: ['feat: a Codex usage meter in the sidebar’s foot', '✓ Go tests  ✓ lint  ✓ desktop  — merged by Leandro'] },
    ],
  },
  {
    name: 'agent-448',
    ai: 'claude',
    task: 'Phone: approve and merge from the review card',
    started: '10:05',
    state: 'review',
    pr: { number: 360, title: 'feat: approve and merge a pull request from the phone', checks: 'pending', files: 7, plus: 296, minus: 40 },
    steps: [
      { kind: 'task', title: 'Task from the lead', at: 0, dur: 30, lines: ['On the phone’s review card, add Approve and Merge.', 'Confirm before merging. Record it on the emulator.'] },
      { kind: 'plan', title: 'Plan: two hub calls, one confirm sheet', at: 30, dur: 90, lines: ['mobile/app/review/[ref].tsx: actions row', 'hubapi: POST /v1/pulls/:n/merge (exists)', 'Confirm sheet with the method'] },
      { kind: 'edit', title: 'mobile/app/review/[ref].tsx  +188 −31', at: 120, dur: 540, lines: ['+  <ActionRow>', '+    <Button onPress={approve}>Approve</Button>', '+    <Button tone="brand" onPress={() => setConfirm(true)}>Merge</Button>', '+  </ActionRow>'] },
      { kind: 'test', title: 'jest — 61 passed', at: 660, dur: 140, result: 'pass', lines: ['$ npm --prefix mobile test', 'PASS  app/review/review.test.tsx', 'Tests: 61 passed, 61 total'] },
      {
        kind: 'desktop',
        title: 'Walked through it on the Android emulator',
        at: 800,
        dur: 230,
        media: [
          { kind: 'clip', from: 800, to: 1030, caption: 'Review card → Merge → confirm → merged' },
          { kind: 'shot', at: 840, caption: 'The review card with its actions', screen: { app: 'phone', stage: 'card' } },
          { kind: 'shot', at: 930, caption: 'Confirm sheet', screen: { app: 'phone', stage: 'confirm' } },
          { kind: 'shot', at: 1000, caption: 'Merged', screen: { app: 'phone', stage: 'merged' } },
        ],
      },
      { kind: 'page', title: 'Published “Phone review flow”', at: 1030, dur: 70, media: [{ kind: 'page', at: 1060, title: 'Phone review flow, step by step', id: 'k3Pq9z' }] },
      { kind: 'pr', title: 'Opened #360', at: 1100, dur: 50, lines: ['feat: approve and merge a pull request from the phone', '◌ desktop · ✓ PR title · ◌ test'] },
    ],
  },
  {
    name: 'agent-449',
    ai: 'opencode',
    task: 'Sidebar scrolls sideways at 1024px',
    started: '11:20',
    state: 'draft',
    pr: { number: 361, title: 'fix: the sidebar no longer scrolls sideways at 1024px', checks: 'passing', files: 2, plus: 9, minus: 4 },
    steps: [
      { kind: 'task', title: 'Task from the lead (UX report #8)', at: 0, dur: 25, lines: ['The v2 sidebar scrolls sideways at 1024px with a long branch.'] },
      { kind: 'plan', title: 'Reproduce with preview --shots', at: 25, dur: 150, lines: ['$ npm --prefix desktop run preview -- --shots out --only window-', 'overflow: window-1024 sidebar 312px > 288px'] },
      { kind: 'edit', title: 'SidebarV2.tsx  +9 −4', at: 175, dur: 160, lines: ['-  <div className="flex items-center gap-2">', '+  <div className="flex min-w-0 items-center gap-2">', '+    <span className="truncate">{branch}</span>'] },
      {
        kind: 'desktop',
        title: 'Before and after, 1024 and 1920',
        at: 335,
        dur: 120,
        media: [
          { kind: 'shot', at: 350, caption: 'Before: 312px wide, scrolls', screen: { app: 'sidebar', overflow: true } },
          { kind: 'shot', at: 420, caption: 'After: truncates at 288px', screen: { app: 'sidebar', overflow: false } },
        ],
      },
      { kind: 'pr', title: 'Draft #361', at: 455, dur: 40, lines: ['fix: the sidebar no longer scrolls sideways at 1024px'] },
    ],
  },
  {
    name: 'agent-450',
    ai: 'claude',
    task: 'End-to-end QA of the whole app',
    started: '12:30',
    state: 'working',
    steps: [
      { kind: 'task', title: 'Task from the lead', at: 0, dur: 40, lines: ['Walk every screen of the app; fix what’s broken.'] },
      { kind: 'plan', title: 'A checklist of 31 flows', at: 40, dur: 220, lines: ['Home · Lead chat · New agent · Review · Settings ×6 · Phone …'] },
      {
        kind: 'desktop',
        title: 'Flows 1–14 walked, 2 broken',
        at: 260,
        dur: 900,
        media: [
          { kind: 'clip', from: 260, to: 1160, caption: 'Flows 1–14' },
          { kind: 'shot', at: 610, caption: 'AgentInfoCard hover crash', screen: { app: 'qa', failing: 1 } },
          { kind: 'shot', at: 980, caption: 'Send is silent when signed out', screen: { app: 'qa', failing: 2 } },
        ],
      },
      { kind: 'edit', title: 'AgentInfoCard.tsx: guard usage.data  +4 −1', at: 1160, dur: 200, lines: ['-  const mine = usage.data.agents.find((u) => u.ref === ref);', '+  const mine = usage.data?.agents?.find((u) => u.ref === ref);'] },
      { kind: 'test', title: 'npm test — running…', at: 1360, dur: 120, lines: ['$ npm --prefix desktop test', '▶ AgentInfoCard', '  ✔ renders before usage arrives (2ms)'] },
    ],
  },
];

export const agentByName = (n: string | null) => agents.find((a) => a.name === n);

// The day's numbers for the reel's header.
export const today = {
  date: 'Saturday, 17 October',
  prs: agents.filter((a) => a.pr).length,
  merged: agents.filter((a) => a.state === 'merged').length,
  recorded: agents.reduce((s, a) => s + length(a), 0),
  shots: agents.reduce((s, a) => s + a.steps.flatMap((x) => x.media ?? []).filter((m) => m.kind === 'shot').length, 0),
};

// shotSvg draws a screenshot of what the AgentBox app, or the phone app,
// looked like at that moment, as an SVG data URL.
export function shotSvg(s: Screen, dark = true): string {
  const bg = dark ? '#121218' : '#f6f6f9';
  const panel = dark ? '#1b1b24' : '#ffffff';
  const raised = dark ? '#252532' : '#eceef3';
  const line = dark ? '#33334a' : '#d9dbe4';
  const text = dark ? '#e7e7ee' : '#1d1d28';
  const sub = dark ? '#8d8da3' : '#6b6b80';
  const brand = '#7c6cf6';
  const bar = (x: number, y: number, w: number, fill = line, h = 10) => `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${h / 2}" fill="${fill}"/>`;
  const label = (x: number, y: number, t: string, size = 14, fill = text, weight = 500) =>
    `<text x="${x}" y="${y}" font-family="Inter,system-ui,sans-serif" font-size="${size}" font-weight="${weight}" fill="${fill}">${t}</text>`;
  let body = '';
  if (s.app === 'phone') {
    const stage = s.stage;
    body = `<rect width="1280" height="800" fill="${dark ? '#0b0b10' : '#e8e8ee'}"/>
<rect x="490" y="30" width="300" height="740" rx="44" fill="#000"/><rect x="502" y="42" width="276" height="716" rx="34" fill="${bg}"/>
${label(530, 100, 'Review', 22, text, 600)}${label(530, 126, 'agentbox · agent-446', 12, sub)}
<rect x="522" y="150" width="236" height="300" rx="18" fill="${panel}" stroke="${line}"/>
<rect x="538" y="166" width="204" height="128" rx="10" fill="${raised}"/>${bar(554, 186, 120, brand)}${bar(554, 206, 160)}${bar(554, 222, 140)}
${label(538, 320, '#358 Review footer stays put', 13, text, 600)}${label(538, 342, '+214 −61 · 6 files · ✓ checks', 11, '#4ade80')}
${stage === 'card' ? `<rect x="538" y="380" width="96" height="44" rx="12" fill="${raised}"/>${label(556, 408, 'Approve', 13)}<rect x="646" y="380" width="96" height="44" rx="12" fill="${brand}"/>${label(670, 408, 'Merge', 13, '#fff')}` : ''}
${stage === 'confirm' ? `<rect x="502" y="420" width="276" height="338" rx="28" fill="${panel}" stroke="${line}"/>${label(526, 470, 'Merge #358?', 18, text, 600)}${label(526, 498, 'Squash and merge into main', 12, sub)}<rect x="526" y="660" width="228" height="48" rx="14" fill="${brand}"/>${label(590, 690, 'Squash and merge', 13, '#fff')}` : ''}
${stage === 'merged' ? `<rect x="538" y="380" width="204" height="44" rx="12" fill="#7c3aed33"/>${label(590, 408, '✓ Merged', 14, '#c4b5fd', 600)}<rect x="522" y="680" width="236" height="48" rx="14" fill="${raised}"/>${label(560, 709, 'Merged #358 into main', 12, text)}` : ''}`;
  } else {
    // The app: the v2 sidebar, a chat, and whatever this screen is about.
    const sw = s.app === 'sidebar' && s.overflow ? 312 : 288;
    const rows = ['AgentBox', 'Today', 'agent-446  Review footer', 'agent-447  Codex meter', 'agent-448  Phone review', s.app === 'sidebar' && s.overflow ? 'agent-449  agentbox/fix-sidebar-overflow-at-1024' : 'agent-449  Sidebar x-scroll'];
    body = `<rect width="1280" height="800" fill="${bg}"/>
<rect width="${sw}" height="800" fill="${panel}"/><rect x="${sw}" width="1" height="800" fill="${line}"/>
${rows.map((r, i) => `${i === 2 || (s.app === 'sidebar' && i === 5) ? `<rect x="10" y="${68 + i * 40}" width="${sw - 20}" height="32" rx="8" fill="${raised}"/>` : ''}<circle cx="30" cy="${84 + i * 40}" r="4" fill="${i < 2 ? brand : sub}"/>${label(44, 89 + i * 40, r, 13, i < 2 ? text : sub)}`).join('')}
${label(20, 44, 'agentbox', 15, text, 600)}
${s.app === 'sidebar' && s.overflow ? `<rect x="0" y="788" width="${sw}" height="8" fill="${raised}"/><rect x="4" y="789" width="200" height="6" rx="3" fill="${sub}"/><rect x="${sw - 2}" y="0" width="3" height="800" fill="#f43f5e"/>${label(sw + 16, 790, '312px', 12, '#fb7185')}` : ''}
${s.app === 'sidebar' && s.meter !== undefined ? `<rect x="12" y="690" width="${sw - 24}" height="92" rx="12" fill="${raised}"/>${label(26, 716, 'Claude', 12, sub)}${bar(90, 707, 170)}${bar(90, 707, 170 * 0.41, brand)}${label(26, 744, 'Codex', 12, sub)}${bar(90, 735, 170)}${bar(90, 735, 170 * s.meter, s.meter > 0.9 ? '#f59e0b' : '#22c55e')}${label(26, 770, `5-hour · ${Math.round(s.meter * 100)}%`, 11, s.meter > 0.9 ? '#fbbf24' : sub)}` : ''}
${[0, 1, 2, 3].map((i) => `${bar(sw + 60 + (i % 2) * 260, 120 + i * 90, 380 - (i % 2) * 60, raised, 56)}`).join('')}
<rect x="${sw + 40}" y="700" width="${1240 - sw - 60}" height="64" rx="18" fill="${panel}" stroke="${line}"/>${label(sw + 64, 738, 'Message the lead…', 14, sub)}`;
    if (s.app === 'review') {
      const fy = s.footer === 'fixed' ? 690 : 820;
      body += `<rect width="1280" height="800" fill="#000" opacity="0.55"/>
<rect x="190" y="50" width="900" height="${s.footer === 'fixed' ? 700 : 750}" rx="18" fill="${panel}" stroke="${line}"/>
${label(220, 92, 'Review agent-446 · #358', 18, text, 600)}${label(220, 116, 'fix: the review modal’s footer stays put', 13, sub)}
<rect x="220" y="140" width="840" height="${s.footer === 'fixed' ? 530 : 610}" rx="12" fill="${raised}"/>
${[0, 1, 2, 3, 4, 5, 6, 7, 8].map((i) => bar(250, 170 + i * 52, 300 + ((i * 97) % 420))).join('')}
${fy < 800 ? `<rect x="190" y="${fy}" width="900" height="60" fill="${panel}"/><rect x="190" y="${fy}" width="900" height="1" fill="${line}"/>${label(220, fy + 36, '3 of 4', 13, sub)}<rect x="770" y="${fy + 12}" width="150" height="36" rx="10" fill="${raised}"/>${label(788, fy + 35, 'Request changes', 13)}<rect x="930" y="${fy + 12}" width="130" height="36" rx="10" fill="${brand}"/>${label(950, fy + 35, 'Merge   ▾', 13, '#fff', 600)}` : `${label(560, 780, '↓ footer below the fold', 13, '#fb7185')}`}
${s.menu ? `<rect x="900" y="${fy - 132}" width="190" height="124" rx="12" fill="${raised}" stroke="${line}"/>${['Squash and merge', 'Rebase and merge', 'Merge commit'].map((m, i) => `${i === 0 ? `<rect x="906" y="${fy - 126}" width="178" height="34" rx="8" fill="${brand}33"/>` : ''}${label(920, fy - 104 + i * 38, m, 13)}`).join('')}` : ''}`;
    }
    if (s.app === 'qa') {
      body += `<rect x="${sw + 380}" y="180" width="440" height="${s.failing === 1 ? 260 : 120}" rx="14" fill="${panel}" stroke="#f43f5e"/>
${label(sw + 404, 216, s.failing === 1 ? 'TypeError: Cannot read properties of undefined' : 'Sent — nothing happens', 14, '#fb7185', 600)}
${label(sw + 404, 242, s.failing === 1 ? "(reading 'find') at AgentInfoCard.tsx:88" : 'no toast, no error, not signed in', 12, sub)}
${s.failing === 1 ? [0, 1, 2, 3].map((i) => bar(sw + 404, 270 + i * 30, 300 - i * 40)).join('') : ''}`;
    }
  }
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800" viewBox="0 0 1280 800">${body}</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}
