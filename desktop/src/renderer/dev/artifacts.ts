// The ?artifacts scenario's fixtures: a project with Hatch connected whose
// chats published a handful of pages, each in a different state — fresh,
// updated by another agent, about to expire, kept for good, expired — and
// the dev bridge's answers for their previews and pages (fixtures.ts).
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { PROJECT } from './fixtures';

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const later = (ms: number) => new Date(Date.now() + ms).toISOString();
const min = 60_000;
const hour = 60 * min;

export function artifactFixtures(): T.Artifact[] {
  const link = (id: string) => `https://hatch.linting.dev/p/${id}`;
  return [
    {
      id: 'q3Usage7Kd2', title: 'Q3 usage report', url: link('q3Usage7Kd2'), agent: `${PROJECT}/lead`, item: 'h3', agents: [`${PROJECT}/lead`, `${PROJECT}/agent-12`],
      version: 3, createdAt: ago(3 * hour), updatedAt: ago(12 * min), expiresAt: later(21 * hour),
    },
    {
      id: 'sidebarBA9x', title: 'Sidebar: before and after', url: link('sidebarBA9x'), agent: `${PROJECT}/agent-12`, item: 'a12', agents: [`${PROJECT}/agent-12`],
      version: 1, createdAt: ago(40 * min), updatedAt: ago(40 * min), expiresAt: later(23 * hour + 20 * min),
    },
    {
      id: 'perfFlame2Q', title: 'Daemon startup flame graph', url: link('perfFlame2Q'), agent: `${PROJECT}/agent-98`, item: 'a98', agents: [`${PROJECT}/agent-98`],
      version: 2, createdAt: ago(23 * hour + 25 * min), updatedAt: ago(5 * hour), expiresAt: later(35 * min),
    },
    {
      id: 'onboardRm4', title: 'Onboarding flow — design notes', url: link('onboardRm4'), agent: `${PROJECT}/agent-97`, item: 'a97', agents: [`${PROJECT}/agent-97`],
      version: 1, public: true, createdAt: ago(26 * hour), updatedAt: ago(26 * hour), permanent: true,
    },
    {
      id: 'oldSprint1', title: 'Sprint 41 retro board', url: link('oldSprint1'), agent: `${PROJECT}/lead`, item: 'h0', agents: [`${PROJECT}/lead`],
      version: 1, createdAt: ago(30 * hour), updatedAt: ago(30 * hour), expiresAt: ago(6 * hour), expired: true,
    },
  ];
}

export function seedArtifacts(queryClient: QueryClient): void {
  queryClient.setQueryData(['artifacts', PROJECT], { connector: 'hatch', artifacts: artifactFixtures() } satisfies T.Artifacts);
  queryClient.setQueryData(['chat', `${PROJECT}/lead`], artifactsLeadChat());
}

// The project's chat as it stood when the report was published and updated.
function artifactsLeadChat(): T.ChatThread {
  return {
    agent: `${PROJECT}/lead`,
    seq: 1,
    session: { state: 'idle', tool: 'claude', options: [], commands: [] },
    items: [
      { id: 'h1', turn: 'h1', kind: 'user', text: 'Put together a Q3 usage report — agents run, tokens, PRs merged — and publish it on Hatch so I can share it.', result: { state: 'completed', stopReason: 'end_turn', endedAt: ago(3 * hour - 2 * min) }, createdAt: ago(3 * hour + 4 * min), updatedAt: ago(3 * hour + 4 * min) },
      { id: 'h2', turn: 'h1', kind: 'assistant', text: 'Reading the token ledger and the merged pull requests for July to September.', createdAt: ago(3 * hour + 3 * min), updatedAt: ago(3 * hour + 3 * min) },
      {
        id: 'h3', turn: 'h1', kind: 'tool', createdAt: ago(3 * hour), updatedAt: ago(3 * hour),
        tool: {
          callId: 'c1', name: 'mcp__hatch__publish_page', title: 'mcp__hatch__publish_page', kind: 'other', status: 'completed', page: { title: 'Q3 usage report' },
          output: 'Published "Q3 usage report" (id q3Usage7Kd2, version 1): https://hatch.linting.dev/p/q3Usage7Kd2',
        },
      },
      { id: 'h4', turn: 'h1', kind: 'assistant', text: 'Published: **Q3 usage report** — https://hatch.linting.dev/p/q3Usage7Kd2. It is private and expires in 24 hours. I asked agent-12 to add the before/after shots of the sidebar to it.', createdAt: ago(3 * hour - min), updatedAt: ago(3 * hour - min) },
      { id: 'h5', turn: 'h5', kind: 'user', text: 'Nice. What else did the agents publish today?', result: { state: 'completed', stopReason: 'end_turn', endedAt: ago(9 * min) }, createdAt: ago(10 * min), updatedAt: ago(10 * min) },
      { id: 'h6', turn: 'h5', kind: 'assistant', text: "Three more pages: agent-12's sidebar before/after, agent-98's flame graph of the daemon's startup (it expires within the hour), and agent-97's onboarding design notes, which it made permanent and public. They're in the tray above the message box.", createdAt: ago(9 * min), updatedAt: ago(9 * min) },
    ],
  };
}

// artifactRequest answers the dev bridge for an artifact's preview: its
// details from "Hatch", with the HTML available for all but the expired one.
export function artifactRequest(method: string, path: string): { status: number; body: string; contentType: string } | undefined {
  const m = method === 'GET' && new RegExp(`^/v1/projects/${PROJECT}/artifacts/([^/]+)$`).exec(path);
  if (!m) return undefined;
  const a = artifactFixtures().find((x) => x.id === decodeURIComponent(m[1]));
  if (!a) return { status: 404, body: JSON.stringify({ error: 'not found' }), contentType: 'application/json' };
  const preview: T.ArtifactPreview = a.expired
    ? { artifact: a, status: 'expired', page: false, error: 'Hatch no longer has this page: it expired, or was deleted.' }
    : { artifact: a, status: 'active', page: true };
  return { status: 200, body: JSON.stringify(preview), contentType: 'application/json' };
}

// artifactPageUrl is what the dev bridge frames for an artifact's page: a
// self-contained report, like what an agent publishes.
export function artifactPageUrl(path: string): string | undefined {
  const m = /\/artifacts\/([^/]+)\/page$/.exec(path);
  const a = m && artifactFixtures().find((x) => x.id === decodeURIComponent(m[1]));
  if (!a) return undefined;
  return `data:text/html;charset=utf-8,${encodeURIComponent(reportPage(a.title))}`;
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
