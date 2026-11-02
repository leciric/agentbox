// The weekly recap's fixture (components/RecapView): a busy week ending today,
// for ?app=recap, every cost reported by Claude Code; &recap=estimated the
// same week on Codex and Cursor, which report none, so its cost is estimated
// and one model has no price; and nothing at all with &recap=empty. A week
// before this one is the same week, quieter.
import type * as T from '../../shared/api';

const hour = 3600;

function week(weeks: number): T.Recap {
  const end = new Date();
  end.setHours(24, 0, 0, 0);
  end.setDate(end.getDate() - 7 * weeks);
  const start = new Date(end);
  start.setDate(start.getDate() - 7);
  const scale = weeks ? 0.55 : 1;
  const work = [5.2, 9.8, 7.1, 12.6, 14.3, 3.4, 6.9].map((h) => Math.round(h * hour * scale));
  const awayShare = [0.82, 0.61, 0.7, 0.58, 0.66, 0.9, 0.74];
  const days = work.map((s, i): T.RecapDay => {
    const d = new Date(start);
    d.setDate(d.getDate() + i);
    const date = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    return { date, workSeconds: s, awaySeconds: Math.round(s * awayShare[i]), agents: [4, 7, 6, 9, 11, 2, 5][i] };
  });
  const shape = [0.6, 0.4, 0.3, 0.2, 0.2, 0.3, 0.5, 0.9, 1.6, 2.4, 2.9, 3.1, 2.2, 2.8, 3.4, 3.6, 3.2, 2.7, 2.1, 1.8, 2.0, 2.3, 1.7, 1.1];
  const total = work.reduce((a, b) => a + b, 0);
  const sum = shape.reduce((a, b) => a + b, 0);
  return {
    start: start.toISOString(),
    end: end.toISOString(),
    agents: Math.round(38 * scale),
    prsOpened: Math.round(27 * scale),
    prsMerged: Math.round(21 * scale),
    insertions: Math.round(18_420 * scale),
    deletions: Math.round(6_135 * scale),
    files: Math.round(412 * scale),
    workSeconds: total,
    awaySeconds: days.reduce((a, d) => a + d.awaySeconds, 0),
    days,
    hours: shape.map((s) => Math.round((total * s) / sum)),
    projects: 3,
    busiest: { project: 'agentbox', workSeconds: Math.round(total * 0.64), agents: Math.round(24 * scale) },
    longest: { project: 'agentbox', agent: 'agent-500', title: 'Daemon memory: unload idle chat transcripts', ai: 'claude', workSeconds: Math.round(6.4 * hour * scale) },
    models: [
      { model: 'claude-opus-4-5', tokens: Math.round(184_000_000 * scale), output: 2_100_000, costUsd: 301.4 * scale, estimatedUsd: 0, unpricedTokens: 0 },
      { model: 'claude-sonnet-4-5', tokens: Math.round(61_500_000 * scale), output: 900_000, costUsd: 86.9 * scale, estimatedUsd: 0, unpricedTokens: 0 },
      { model: 'claude-haiku-4-5', tokens: Math.round(22_800_000 * scale), output: 400_000, costUsd: 9.8 * scale, estimatedUsd: 0, unpricedTokens: 0 },
      { model: 'gpt-5-codex', tokens: Math.round(9_100_000 * scale), output: 150_000, costUsd: 14.5 * scale, estimatedUsd: 0, unpricedTokens: 0 },
    ],
    tokens: Math.round(277_400_000 * scale),
    costUsd: 412.6 * scale,
    estimatedUsd: 0,
    unpricedTokens: 0,
    streak: weeks ? 5 : 12,
    bestStreak: 12,
  };
}

function empty(): T.Recap {
  const r = week(0);
  return { ...r, agents: 0, prsOpened: 0, prsMerged: 0, insertions: 0, deletions: 0, files: 0, workSeconds: 0, awaySeconds: 0, days: r.days.map((d) => ({ ...d, workSeconds: 0, awaySeconds: 0, agents: 0 })), hours: Array(24).fill(0), projects: 0, busiest: undefined, longest: undefined, models: [], tokens: 0, costUsd: 0, estimatedUsd: 0, unpricedTokens: 0, streak: 0 };
}

// estimated is the week on tools that report no cost: Codex's and Cursor's
// turns priced from the table, a lead on Claude Code that reported its own,
// and a model with no price.
function estimated(weeks: number): T.Recap {
  const r = week(weeks);
  const scale = weeks ? 0.55 : 1;
  const models: T.RecapModel[] = [
    { model: 'gpt-5.3-codex', tokens: Math.round(152_000_000 * scale), output: 1_800_000, costUsd: 61.2 * scale, estimatedUsd: 61.2 * scale, unpricedTokens: 0 },
    { model: 'composer-2', tokens: Math.round(71_300_000 * scale), output: 1_100_000, costUsd: 7.4 * scale, estimatedUsd: 7.4 * scale, unpricedTokens: 0 },
    { model: 'claude-sonnet-4-5', tokens: Math.round(38_600_000 * scale), output: 520_000, costUsd: 24.1 * scale, estimatedUsd: 0, unpricedTokens: 0 },
    { model: 'cursor-preview-x', tokens: Math.round(15_500_000 * scale), output: 210_000, costUsd: 0, estimatedUsd: 0, unpricedTokens: Math.round(15_500_000 * scale) },
  ];
  const sum = (f: (m: T.RecapModel) => number) => models.reduce((a, m) => a + f(m), 0);
  return { ...r, models, tokens: sum((m) => m.tokens), costUsd: sum((m) => m.costUsd), estimatedUsd: sum((m) => m.estimatedUsd), unpricedTokens: sum((m) => m.unpricedTokens) };
}

// recapRequest answers the dev bridge for /v1/recap.
export function recapRequest(method: string, path: string): { status: number; body: string; contentType: string } | undefined {
  const url = new URL(path, 'http://dev');
  if (method !== 'GET' || url.pathname !== '/v1/recap') return undefined;
  const weeks = Number(url.searchParams.get('weeks') ?? 0);
  const kind = new URLSearchParams(location.search).get('recap');
  const body = kind === 'empty' ? empty() : kind === 'estimated' ? estimated(weeks) : week(weeks);
  return { status: 200, body: JSON.stringify(body), contentType: 'application/json' };
}
