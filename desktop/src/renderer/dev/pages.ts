// The ?pages scenario's fixtures: a project with Hatch connected whose chats
// published pages, a few of them new (not opened yet), one expired (which no
// list shows), and the dev bridge's answers for their previews, their pages
// and their thumbnails. "Publish a page" (PublishButton in preview.tsx)
// makes an agent publish one more, to watch it drop onto the stack.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { knowProject, resetSeen } from '../lib/pages';
import { PROJECT } from './fixtures';

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const later = (ms: number) => new Date(Date.now() + ms).toISOString();
const min = 60_000;
const hour = 60 * min;
const link = (id: string) => `https://hatch.linting.dev/p/${id}`;

type Fixture = T.Artifact & { look: Look };
type Look = 'report' | 'compare' | 'flame' | 'notes' | 'board' | 'none';

function page(id: string, title: string, agent: string, look: Look, updated: number, extra: Partial<T.Artifact> = {}): Fixture {
  const ref = `${PROJECT}/${agent}`;
  return {
    id, title, url: link(id), agent: ref, item: `${agent}-${id}`, agents: [ref], version: 1,
    createdAt: ago(updated + 20 * min), updatedAt: ago(updated), expiresAt: later(24 * hour - updated), look, ...extra,
  };
}

// The pages, newest first. The first three are new. Made on first use:
// fixtures.ts imports this file, and PROJECT isn't there yet when it loads.
let made: Fixture[] | undefined;
const all = (): Fixture[] => (made ??= [
  page('sidebarBA9x', 'Sidebar: before and after', 'agent-12', 'compare', 2 * min),
  page('q3Usage7Kd2', 'Q3 usage report', 'lead', 'report', 6 * min, { item: 'h3', version: 3, agents: [`${PROJECT}/lead`, `${PROJECT}/agent-12`] }),
  page('perfFlame2Q', 'Daemon startup flame graph', 'agent-98', 'flame', 14 * min, { version: 2 }),
  page('onboardRm4', 'Onboarding flow — design notes', 'agent-97', 'notes', 3 * hour, { public: true, permanent: true, expiresAt: undefined }),
  page('ciBoard41x', 'Release CI: what failed and why', 'agent-41', 'board', 5 * hour),
  page('sidebarV1aa', 'Sidebar spacing options', 'agent-12', 'none', 7 * hour),
  page('retro41old', 'Sprint 41 retro board', 'lead', 'board', 30 * hour, { expiresAt: ago(6 * hour), expired: true }),
]);
const newCount = 3;
let published = 0;

export const pageFixtures = (): T.Artifact[] =>
  all().map((f) => {
    const a: Partial<Fixture> = { ...f };
    delete a.look;
    return a as T.Artifact;
  });

export function seedPages(queryClient: QueryClient): void {
  resetSeen();
  // The project was known before the newest pages arrived: those are new.
  knowProject(PROJECT, { connector: 'hatch', artifacts: pageFixtures().slice(newCount) });
  queryClient.setQueryData(['pages', PROJECT], { connector: 'hatch', artifacts: pageFixtures() } satisfies T.Artifacts);
  queryClient.setQueryData(['chat', `${PROJECT}/lead`], pagesLeadChat());
  // The fixtures' other project (a very long name) has two pages of its own,
  // for global Media's project filter.
  const other = queryClient.getQueryData<T.Project[]>(['projects'])?.find((p) => p.name !== PROJECT);
  if (other) {
    const mine = (id: string, title: string, agent: string, updated: number): T.Artifact => {
      const ref = `${other.name}/${agent}`;
      return { ...pageFixtures()[0], id, title, url: link(id), agent: ref, item: `${agent}-${id}`, agents: [ref], createdAt: ago(updated + 20 * min), updatedAt: ago(updated), expiresAt: later(24 * hour - updated) };
    };
    knowProject(other.name, { connector: 'hatch', artifacts: [] });
    queryClient.setQueryData(['pages', other.name], { connector: 'hatch', artifacts: [mine('othrA1x', 'Billing export: schema options', 'agent-3', 40 * min), mine('othrB2y', 'Invoice PDF layout', 'lead', 4 * hour)] } satisfies T.Artifacts);
  }
}

// publishOne is an agent publishing another page, as the daemon's next read
// of the project's pages would have it.
export function publishOne(queryClient: QueryClient): void {
  const looks: [string, string, Look][] = [
    ['Token spend by model, last 7 days', 'agent-98', 'report'],
    ['New project dialog: three layouts', 'agent-12', 'compare'],
    ['Flaky tests this week', 'agent-41', 'board'],
  ];
  const [title, agent, look] = looks[published % looks.length];
  published++;
  const fresh = page(`live${published}x${Date.now().toString(36).slice(-4)}`, title, agent, look, 0);
  all().unshift(fresh);
  queryClient.setQueryData(['pages', PROJECT], { connector: 'hatch', artifacts: pageFixtures() } satisfies T.Artifacts);
}

// The project's chat as it stood when the report was published and updated.
function pagesLeadChat(): T.ChatThread {
  return {
    agent: `${PROJECT}/lead`,
    seq: 1,
    session: { state: 'idle', tool: 'claude', options: [], commands: [] },
    items: [
      { id: 'h1', turn: 'h1', kind: 'user', text: 'Put together a Q3 usage report — agents run, tokens, PRs merged — and publish it on Hatch so I can share it.', result: { state: 'completed', stopReason: 'end_turn', endedAt: ago(9 * min) }, createdAt: ago(12 * min), updatedAt: ago(12 * min) },
      { id: 'h2', turn: 'h1', kind: 'assistant', text: 'Reading the token ledger and the merged pull requests for July to September.', createdAt: ago(11 * min), updatedAt: ago(11 * min) },
      {
        id: 'h3', turn: 'h1', kind: 'tool', createdAt: ago(10 * min), updatedAt: ago(10 * min),
        tool: {
          callId: 'c1', name: 'mcp__hatch__publish_page', title: 'mcp__hatch__publish_page', kind: 'other', status: 'completed', page: { title: 'Q3 usage report' },
          output: 'Published "Q3 usage report" (id q3Usage7Kd2, version 1): https://hatch.linting.dev/p/q3Usage7Kd2',
        },
      },
      { id: 'h4', turn: 'h1', kind: 'assistant', text: 'Published: **Q3 usage report**. It is private and expires in 24 hours. I asked agent-12 to add the before/after shots of the sidebar to it.', createdAt: ago(9 * min), updatedAt: ago(9 * min) },
      { id: 'h5', turn: 'h5', kind: 'user', text: 'Nice. What else did the agents publish today?', result: { state: 'completed', stopReason: 'end_turn', endedAt: ago(1 * min) }, createdAt: ago(2 * min), updatedAt: ago(2 * min) },
      { id: 'h6', turn: 'h5', kind: 'assistant', text: "agent-12 just published the sidebar before/after, and agent-98 updated its flame graph of the daemon's startup. Both are on the stack by the message box.", createdAt: ago(1 * min), updatedAt: ago(1 * min) },
    ],
  };
}

// pageRequest answers the dev bridge for a page's preview: its details from
// "Hatch", with the HTML available for all but the expired one.
export function pageRequest(method: string, path: string): { status: number; body: string; contentType: string } | undefined {
  if (method === 'GET' && path === `/v1/projects/${PROJECT}/artifacts`) {
    return { status: 200, body: JSON.stringify({ connector: 'hatch', artifacts: pageFixtures() }), contentType: 'application/json' };
  }
  const m = method === 'GET' && new RegExp(`^/v1/projects/${PROJECT}/artifacts/([^/]+)$`).exec(path);
  if (!m) return undefined;
  const a = pageFixtures().find((x) => x.id === decodeURIComponent(m[1]));
  if (!a) return { status: 404, body: JSON.stringify({ error: 'not found' }), contentType: 'application/json' };
  const preview: T.ArtifactPreview = a.expired
    ? { artifact: a, status: 'expired', page: false, error: 'Hatch no longer has this page: it expired, or was deleted.' }
    : { artifact: a, status: 'active', page: true };
  return { status: 200, body: JSON.stringify(preview), contentType: 'application/json' };
}

// pagePageUrl is what the dev bridge frames for a page: a self-contained
// report, like what an agent publishes.
export function pagePageUrl(path: string): string | undefined {
  const m = /\/artifacts\/([^/]+)\/page$/.exec(path);
  const a = m && all().find((x) => x.id === decodeURIComponent(m[1]));
  if (!a) return undefined;
  return `data:text/html;charset=utf-8,${encodeURIComponent(reportPage(a.title))}`;
}

// pageThumbUrl stands in for the main process's snapshot: the page's first
// screen drawn as SVG, or null for the page whose HTML "can't be had", which
// shows its title tile.
export async function pageThumbUrl(path: string): Promise<string | null> {
  const m = /\/artifacts\/([^/]+)\/page$/.exec(path);
  const a = m && all().find((x) => x.id === decodeURIComponent(m[1]));
  if (!a || a.look === 'none') return null;
  await new Promise((r) => setTimeout(r, 250 + Math.random() * 500));
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(thumbSvg(a.title, a.look))}`;
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function thumbSvg(title: string, look: Look): string {
  const W = 1280;
  const H = 800;
  const heads: Record<Look, [string, string, string]> = {
    report: ['#fff7ed', '#fde68a', '#ea580c'],
    compare: ['#eef2ff', '#c7d2fe', '#4f46e5'],
    flame: ['#1c1917', '#292524', '#f97316'],
    notes: ['#f0fdf4', '#bbf7d0', '#16a34a'],
    board: ['#fdf2f8', '#fbcfe8', '#db2777'],
    none: ['#fff', '#eee', '#999'],
  };
  const [bg, band, accent] = heads[look];
  const dark = look === 'flame';
  const ink = dark ? '#fafaf9' : '#1c1917';
  let body = '';
  if (look === 'report') {
    const bars = [42, 58, 51, 73, 66, 88, 94, 81, 97, 112, 104, 126];
    body += [0, 1, 2, 3].map((i) => `<rect x="${56 + i * 296}" y="250" width="276" height="110" rx="14" fill="#fff" stroke="#e7e5e4"/><rect x="${76 + i * 296}" y="276" width="${110 + i * 12}" height="30" rx="6" fill="${ink}"/><rect x="${76 + i * 296}" y="320" width="120" height="14" rx="7" fill="#a8a29e"/>`).join('');
    body += `<rect x="56" y="390" width="1168" height="360" rx="16" fill="#fff" stroke="#e7e5e4"/>`;
    body += bars.map((v, i) => `<rect x="${96 + i * 92}" y="${710 - v * 2.4}" width="70" height="${v * 2.4}" rx="8" fill="${accent}" opacity="${0.55 + i * 0.035}"/>`).join('');
  } else if (look === 'compare') {
    for (const [i, label] of ['Before', 'After'].entries()) {
      const x = 56 + i * 600;
      body += `<rect x="${x}" y="250" width="568" height="500" rx="16" fill="#fff" stroke="#c7d2fe"/><text x="${x + 24}" y="292" font-size="22" font-weight="600" fill="${accent}" font-family="sans-serif">${label}</text>`;
      body += `<rect x="${x + 24}" y="316" width="150" height="410" rx="10" fill="${i ? '#312e81' : '#e5e7eb'}"/>`;
      for (let r = 0; r < 7; r++) body += `<rect x="${x + 40}" y="${336 + r * 52}" width="${i ? 118 : 96}" height="${i ? 30 : 18}" rx="${i ? 8 : 4}" fill="${i ? (r === 1 ? '#818cf8' : '#4338ca') : '#9ca3af'}"/>`;
      body += `<rect x="${x + 194}" y="316" width="350" height="190" rx="10" fill="#f3f4f6"/><rect x="${x + 194}" y="526" width="350" height="200" rx="10" fill="#f3f4f6"/>`;
    }
  } else if (look === 'flame') {
    const cols = ['#f97316', '#fb923c', '#fdba74', '#ef4444', '#f59e0b'];
    let y = 720;
    for (let row = 0; row < 9; row++) {
      let x = 56;
      while (x < 1210) {
        const w = Math.min(1224 - x, 60 + ((row * 97 + x * 13) % 340));
        if ((row * 7 + x) % 5 !== 0 || row < 2) body += `<rect x="${x}" y="${y}" width="${w - 3}" height="44" rx="3" fill="${cols[(row + x) % cols.length]}"/>`;
        x += w;
      }
      y -= 50;
    }
  } else if (look === 'notes') {
    for (let i = 0; i < 3; i++) body += `<rect x="${56 + i * 396}" y="250" width="372" height="230" rx="16" fill="#fff" stroke="#bbf7d0"/><circle cx="${100 + i * 396}" cy="296" r="22" fill="${accent}" opacity="0.8"/><rect x="${136 + i * 396}" y="284" width="200" height="22" rx="6" fill="${ink}"/>` + [0, 1, 2, 3].map((r) => `<rect x="${80 + i * 396}" y="${346 + r * 30}" width="${300 - r * 40}" height="12" rx="6" fill="#a3a3a3"/>`).join('');
    body += [0, 1, 2, 3, 4, 5].map((r) => `<rect x="56" y="${520 + r * 38}" width="${1100 - r * 90}" height="14" rx="7" fill="#a3a3a3" opacity="0.7"/>`).join('');
  } else if (look === 'board') {
    for (let c = 0; c < 4; c++) {
      const x = 56 + c * 296;
      body += `<rect x="${x}" y="250" width="276" height="500" rx="14" fill="#fff" stroke="#fbcfe8"/><rect x="${x + 18}" y="270" width="120" height="20" rx="6" fill="${accent}" opacity="0.85"/>`;
      for (let r = 0; r < 3 + (c % 2); r++) body += `<rect x="${x + 18}" y="${310 + r * 104}" width="240" height="88" rx="10" fill="#fdf2f8" stroke="#f9a8d4"/><rect x="${x + 32}" y="${328 + r * 104}" width="${150 + ((c + r) % 3) * 25}" height="14" rx="7" fill="#831843" opacity="0.7"/>`;
    }
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}"><rect width="${W}" height="${H}" fill="${bg}"/><rect width="${W}" height="210" fill="${band}"/><text x="56" y="84" font-family="sans-serif" font-size="18" letter-spacing="3" font-weight="700" fill="${accent}">AGENTBOX · HATCH</text><text x="56" y="150" font-family="sans-serif" font-size="54" font-weight="700" fill="${ink}">${esc(title)}</text>${body}</svg>`;
}

function reportPage(title: string): string {
  const bars = [42, 58, 51, 73, 66, 88, 94, 81, 97, 112, 104, 126];
  const top = Math.max(...bars);
  const chart = bars
    .map((v, i) => `<div class="bar" style="height:${(v / top) * 100}%"><span>${i % 4 === 0 ? ['Jul', 'Aug', 'Sep'][i / 4] : ''}</span></div>`)
    .join('');
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${title}</title>
<style>
  :root { color-scheme: light; --ink:#1c1917; --muted:#78716c; --line:#e7e5e4; --accent:#ea580c; }
  * { box-sizing: border-box; }
  body { margin:0; font:15px/1.55 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; color:var(--ink); background:#fafaf9; }
  header { padding:48px 56px 28px; background:linear-gradient(135deg,#fff7ed,#fef3c7 60%,#fff); border-bottom:1px solid var(--line); }
  .eyebrow { font-size:12px; letter-spacing:.12em; text-transform:uppercase; color:var(--accent); font-weight:600; }
  h1 { margin:8px 0 6px; font-size:34px; letter-spacing:-.02em; }
  header p { margin:0; color:var(--muted); max-width:60ch; }
  main { padding:32px 56px 56px; display:grid; gap:28px; }
  .kpis { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:16px; }
  .kpi { background:#fff; border:1px solid var(--line); border-radius:14px; padding:18px 20px; }
  .kpi b { display:block; font-size:28px; letter-spacing:-.02em; }
  .kpi span { color:var(--muted); font-size:13px; }
  .kpi em { font-style:normal; color:#16a34a; font-size:12px; font-weight:600; }
  section { background:#fff; border:1px solid var(--line); border-radius:16px; padding:22px 24px; }
  h2 { margin:0 0 16px; font-size:16px; }
  .chart { display:flex; align-items:flex-end; gap:10px; height:200px; padding-bottom:22px; border-bottom:1px solid var(--line); }
  .bar { flex:1; border-radius:8px 8px 3px 3px; background:linear-gradient(180deg,#fb923c,#ea580c); position:relative; animation:grow .8s ease-out both; transform-origin:bottom; }
  .bar span { position:absolute; bottom:-22px; left:0; right:0; text-align:center; font-size:11px; color:var(--muted); }
  @keyframes grow { from { transform:scaleY(.1); opacity:.3 } }
  table { width:100%; border-collapse:collapse; font-size:14px; }
  td, th { text-align:left; padding:10px 8px; border-bottom:1px solid var(--line); }
  th { color:var(--muted); font-weight:500; font-size:12px; text-transform:uppercase; letter-spacing:.06em; }
  .pill { display:inline-block; padding:2px 8px; border-radius:99px; background:#ffedd5; color:#9a3412; font-size:12px; font-weight:600; }
</style></head><body>
<header><div class="eyebrow">AgentBox · Hatch</div><h1>${title}</h1><p>July to September: the agents AgentBox ran, what they cost in tokens, and what they shipped. Generated from the token ledger and the merged pull requests.</p></header>
<main>
  <div class="kpis">
    <div class="kpi"><b>1,284</b><span>agents run</span> <em>+38%</em></div>
    <div class="kpi"><b>412M</b><span>tokens</span> <em>+21%</em></div>
    <div class="kpi"><b>186</b><span>PRs merged</span> <em>+52%</em></div>
    <div class="kpi"><b>94%</b><span>CI green on first push</span> <em>+6 pts</em></div>
  </div>
  <section><h2>Agents run per week</h2><div class="chart">${chart}</div></section>
  <section><h2>Busiest projects</h2>
    <table><tr><th>Project</th><th>Agents</th><th>PRs</th><th>Top model</th></tr>
    <tr><td>agentbox</td><td>612</td><td>94</td><td><span class="pill">Opus</span></td></tr>
    <tr><td>hatch</td><td>203</td><td>37</td><td><span class="pill">Sonnet</span></td></tr>
    <tr><td>agentbox-hub</td><td>148</td><td>29</td><td><span class="pill">Sonnet</span></td></tr></table>
  </section>
</main></body></html>`;
}
