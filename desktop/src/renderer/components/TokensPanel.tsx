import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { ChartColumn, ChevronRight, Coins, Gauge, Users } from 'lucide-react';
import { Fragment, useState } from 'react';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api, type TokenQuery } from '../lib/api';
import { humanTokens, limitTone, share, usd, windowNow } from '../lib/tokens';
import { cn, errorMessage, timeAgo, timeUntil } from '../lib/utils';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Card, Notice, Panel } from './ui/card';
import { Tabs, TabsList, TabsTrigger } from './ui/tabs';
import { Tip } from './ui/tooltip';

// The token ledger, drawn (D83): what every chat spent, over a stretch of time,
// and which agent spent it. The five-hour stretch comes first because it is the
// length of Claude's usage-limit window, and "what ate my limit" is the
// question this is usually opened to answer.
//
// Every model call sends the whole conversation again, so an agent's spend is
// mostly cache reads, and its peak context says why: that is what each call
// of its busiest turn carried. Both are shown wherever the total is.

type Period = '5h' | '24h' | '7d' | '30d' | 'all';

const periods: { id: Period; label: string; since?: string; words: string }[] = [
  { id: '5h', label: '5 hours', since: '5h', words: 'in the last 5 hours' },
  { id: '24h', label: '24 hours', since: '24h', words: 'in the last 24 hours' },
  { id: '7d', label: '7 days', since: '7d', words: 'in the last 7 days' },
  { id: '30d', label: '30 days', since: '30d', words: 'in the last 30 days' },
  { id: 'all', label: 'All time', words: 'since the ledger began' },
];

const kindVariant: Record<string, BadgeVariant> = {
  [A.TokensTurn]: 'default',
  [A.TokensBackground]: 'info',
  [A.TokensCompaction]: 'brand',
  [A.TokensConsolidation]: 'brand',
};

// TokensPanel is the ledger for one project, or for every project when none is
// given. onOpenAgent opens an agent that still exists.
export function TokensPanel({ project, onOpenAgent }: { project?: string; onOpenAgent?: (ref: string) => void }) {
  const [period, setPeriod] = useState<Period>('5h');
  const chosen = periods.find((p) => p.id === period) ?? periods[0];
  const report = useQuery({
    queryKey: ['tokens', project ?? '', period],
    queryFn: () => api.tokens({ project, since: chosen.since }),
    refetchInterval: 15_000,
    // A new stretch holds the last one's picture, dimmed, until it arrives:
    // no flash of nothing between two answers.
    placeholderData: keepPreviousData,
  });
  const data = report.data;

  return (
    <div className="grid grid-cols-1 gap-4" data-tokens-panel>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Tabs value={period} onValueChange={(value) => setPeriod(value as Period)}>
          <TabsList aria-label="How far back">
            {periods.map((p) => (
              <TabsTrigger key={p.id} value={p.id} data-period={p.id}>
                {p.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <p className="min-w-0 flex-1 text-[12px] leading-relaxed text-subtle">
          What each agent's chat spent, kept after the agent is retired. A Claude Code you start by hand in an agent's terminal isn't counted.
        </p>
      </div>

      <ClaudeLimits />

      {report.error && <Notice>{errorMessage(report.error)}</Notice>}
      {report.isPending && <p className="text-[13px] text-subtle">Loading…</p>}

      {data && data.agents.length === 0 && (
        <Panel className="p-6 text-center">
          <Coins className="mx-auto size-5 text-faint" />
          <p className="mt-2 text-[13px] font-medium text-secondary">Nothing spent {chosen.words}.</p>
          <p className="mx-auto mt-1 max-w-md text-[12px] leading-relaxed text-subtle">
            Every turn a chat takes is written here as it ends: its tokens by model, how full its context was, and the AI tool's own estimate of what it cost.
          </p>
        </Panel>
      )}

      {data && data.agents.length > 0 && (
        <div className={cn('grid grid-cols-1 gap-4 transition-opacity', report.isPlaceholderData && 'opacity-60')}>
          <Headline report={data} />
          <SpendOverTime report={data} />
          <AgentsTable report={data} scoped={!!project} onOpenAgent={onOpenAgent} />
        </div>
      )}
    </div>
  );
}

// --- The account's own limits ---------------------------------------------------

// ClaudeLimits is how much of each Claude account's limits is used, as the last
// chat on the account was told (D85): what the ledger below is being spent
// against. Nothing shows until a chat has reported one.
function ClaudeLimits() {
  const limits = useQuery({ queryKey: ['claudeLimits'], queryFn: api.claudeLimits, refetchInterval: 30_000 });
  if (!limits.data?.length) return null;
  return (
    <Card title="Claude limits" icon={Gauge} description="Shared by every agent on the account. As Anthropic reported them to the account's last chat.">
      <div className="grid gap-4">
        {limits.data.map((l) => (
          <div key={l.account} className="min-w-0" data-claude-limit={l.account}>
            <div className="mb-2 flex flex-wrap items-baseline gap-x-2 text-[12px]">
              <span className="font-medium text-secondary">{l.account}</span>
              {l.default && <span className="text-faint">default</span>}
              {l.status && l.status !== 'allowed' && <Badge variant={l.status === 'rejected' ? 'danger' : 'warning'}>{l.status.replace('_', ' ')}</Badge>}
              <span className="ml-auto text-faint">as of {timeAgo(l.at)}</span>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              {l.windows.map((w) => {
                const now = windowNow(w);
                const tone = limitTone(now);
                return (
                  <div key={w.name} className="min-w-0">
                    <div className="flex items-baseline justify-between gap-2 text-[12px]">
                      <span className="text-muted">{w.label}</span>
                      <span className="tabular-nums text-secondary">{now === null ? 'reset since' : `${Math.round(now * 100)}%`}</span>
                    </div>
                    <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-surface-strong" aria-hidden>
                      <div
                        className={cn('h-full rounded-full', tone === 'high' ? 'bg-rose-400' : tone === 'warn' ? 'bg-amber-400' : 'bg-brand-400')}
                        style={{ width: `${now === null ? 0 : Math.max(Math.min(now, 1) * 100, 2)}%` }}
                      />
                    </div>
                    <div className="mt-1 text-[11px] text-faint">{now === null ? `Reset ${timeAgo(w.resetsAt)}; no chat since` : `Resets in ${timeUntil(w.resetsAt)}`}</div>
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}

// --- Headline -----------------------------------------------------------------

function Headline({ report }: { report: T.TokenReport }) {
  const fullest = report.agents.reduce<T.AgentTokens | undefined>((best, a) => (!best || a.maxContext > best.maxContext ? a : best), undefined);
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <Stat label="Tokens" value={humanTokens(report.total)} hint={`${humanTokens(report.output)} of them written by the model`} />
      <Stat label="Estimated cost" value={usd(report.costUSD)} hint="The AI tool's estimate at API prices" />
      <Stat label="Cache reads" value={share(report.cacheRead, report.total)} hint="Conversation sent again with each step" />
      <Stat
        label="Peak context"
        value={fullest ? humanTokens(fullest.maxContext) : '—'}
        hint={fullest ? `Carried by every call of ${agentName(fullest, false).name}'s busiest turn` : undefined}
      />
    </div>
  );
}

// Stat is one headline number. Its value keeps proportional figures: it stands
// alone, and equal-width digits only earn their place in a column.
export function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="min-w-0 rounded-xl border border-line-faint bg-surface-faint p-3">
      <div className="text-[11px] uppercase tracking-wider text-subtle">{label}</div>
      <div className="mt-1 text-xl font-semibold tracking-tight text-title">{value}</div>
      {hint && <div className="mt-0.5 line-clamp-2 text-[11.5px] leading-snug text-faint">{hint}</div>}
    </div>
  );
}

// --- Over time ----------------------------------------------------------------

interface Slot {
  start: number;
  counts?: T.TokenCounts;
}

// slots fills in the stretches the ledger has no bucket for, so the columns
// sit on a real time axis and a quiet hour reads as quiet rather than missing.
function slots(report: T.TokenReport): Slot[] {
  const step = report.bucketSeconds * 1000;
  if (step <= 0) return [];
  const byStart = new Map(report.buckets.map((b) => [Date.parse(b.start), b] as const));
  const floor = (t: number) => Math.floor(t / step) * step;
  const first = report.since ? floor(Date.parse(report.since)) : report.buckets.length ? Date.parse(report.buckets[0].start) : floor(Date.parse(report.until));
  const last = floor(Date.parse(report.until));
  const out: Slot[] = [];
  // Bounded, so a ledger years long at a day a column still draws.
  for (let t = Math.max(first, last - 400 * step); t <= last; t += step) out.push({ start: t, counts: byStart.get(t) });
  return out;
}

// axisTokens is a tick label: niceCeiling's round numbers, without the ".0"
// humanTokens keeps for the ledger's own figures.
function axisTokens(n: number): string {
  return humanTokens(n).replace(/\.0+(?=[KMB]$)/, '');
}

// niceCeiling rounds an axis maximum up to 1, 2 or 5 of a power of ten, so the
// gridlines land on numbers a person would say.
function niceCeiling(v: number): number {
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function slotLabel(start: number, stepSeconds: number): string {
  const from = new Date(start);
  if (stepSeconds >= 86_400) return from.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  const to = new Date(start + stepSeconds * 1000);
  const time = (d: Date) => d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  const day = stepSeconds >= 3600 ? `${from.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}, ` : '';
  return `${day}${time(from)}–${time(to)}`;
}

function stepWords(seconds: number): string {
  if (seconds >= 86_400) return 'a day';
  if (seconds >= 3600) return seconds === 3600 ? 'an hour' : `${seconds / 3600} hours`;
  return `${seconds / 60} minutes`;
}

function SpendOverTime({ report }: { report: T.TokenReport }) {
  const [asTable, setAsTable] = useState(false);
  const series = slots(report);
  const peak = niceCeiling(Math.max(0, ...series.map((s) => s.counts?.total ?? 0)));
  const ticks = [0, 0.5, 1];
  const labelled = series.length > 1 ? [series[0], series[Math.floor((series.length - 1) / 2)], series[series.length - 1]] : series;

  return (
    <Card
      title="Tokens over time"
      icon={ChartColumn}
      description={`Each column is ${stepWords(report.bucketSeconds)}.`}
      action={
        <Button variant="ghost" size="sm" onClick={() => setAsTable((v) => !v)} data-tokens-as-table={asTable}>
          {asTable ? 'Show the chart' : 'Show as a table'}
        </Button>
      }
    >
      {asTable ? (
        <div className="max-h-72 overflow-auto rounded-xl border border-line-faint">
          <table className="w-full text-[12px]">
            <thead className="sticky top-0 bg-surface">
              <tr className="whitespace-nowrap border-b border-line text-left text-[10.5px] uppercase tracking-[0.06em] text-subtle">
                <th className="py-1.5 pl-3 pr-2 font-medium">When</th>
                <th className="px-2 py-1.5 text-right font-medium">Tokens</th>
                <th className="px-2 py-1.5 text-right font-medium">Cache read</th>
                <th className="px-2 py-1.5 text-right font-medium">Output</th>
                <th className="py-1.5 pl-2 pr-3 text-right font-medium">Cost</th>
              </tr>
            </thead>
            <tbody>
              {series
                .filter((s) => s.counts)
                .map((s) => (
                  <tr key={s.start} className="border-t border-line-faint first:border-t-0">
                    <td className="py-1.5 pl-3 pr-2 whitespace-nowrap text-muted">{slotLabel(s.start, report.bucketSeconds)}</td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-secondary">{humanTokens(s.counts!.total)}</td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-tertiary">{humanTokens(s.counts!.cacheRead)}</td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-tertiary">{humanTokens(s.counts!.output)}</td>
                    <td className="py-1.5 pl-2 pr-3 text-right tabular-nums text-tertiary">{usd(s.counts!.costUSD)}</td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div>
          <div className="relative h-40">
            {ticks.map((f) => (
              <div key={f} className="absolute left-12 right-0 border-t border-line-faint" style={{ bottom: `${f * 100}%` }}>
                <span className="absolute -left-12 w-10 -translate-y-1/2 text-right text-[10.5px] tabular-nums text-faint">{axisTokens(peak * f)}</span>
              </div>
            ))}
            <div className="absolute inset-y-0 left-12 right-0 flex items-end gap-[2px]" role="list" aria-label="Tokens over time">
              {series.map((s) => {
                const total = s.counts?.total ?? 0;
                const height = total ? Math.max((total / peak) * 100, 1.5) : 0;
                return (
                  <Tip
                    key={s.start}
                    side="top"
                    label={
                      <span className="grid gap-0.5">
                        <span className="font-semibold text-title">{total ? `${humanTokens(total)} tokens` : 'Nothing spent'}</span>
                        {s.counts && (
                          <span className="text-muted">
                            {share(s.counts.cacheRead, total)} cache reads · {usd(s.counts.costUSD)}
                          </span>
                        )}
                        <span className="text-faint">{slotLabel(s.start, report.bucketSeconds)}</span>
                      </span>
                    }
                  >
                    <button
                      type="button"
                      role="listitem"
                      aria-label={`${slotLabel(s.start, report.bucketSeconds)}: ${humanTokens(total)} tokens`}
                      className="group flex h-full min-w-0 flex-1 items-end justify-center rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
                    >
                      <span
                        className="w-full max-w-6 rounded-t-[4px] bg-brand-400 transition-colors group-hover:bg-brand-300"
                        style={{ height: `${height}%` }}
                      />
                    </button>
                  </Tip>
                );
              })}
            </div>
          </div>
          <div className="ml-12 mt-1.5 flex justify-between gap-2 text-[10.5px] tabular-nums text-faint">
            {labelled.map((s, i) => (
              <span key={`${s.start}-${i}`} className="truncate">
                {slotLabel(s.start, report.bucketSeconds).split('–')[0]}
              </span>
            ))}
          </div>
        </div>
      )}
    </Card>
  );
}

// --- By agent -----------------------------------------------------------------

// agentName is what an agent is called in the ledger's tables: its title while
// it has one, and the project's chat for the lead. scoped leaves the project
// out, on a page that is already about one.
function agentName(a: T.AgentTokens, scoped: boolean): { name: string; sub: string } {
  if (a.agent === A.LeadName) return { name: scoped ? 'Project chat' : `${a.project} chat`, sub: a.ref };
  return { name: a.title || a.agent, sub: scoped ? a.agent : a.ref };
}

function AgentsTable({ report, scoped, onOpenAgent }: { report: T.TokenReport; scoped: boolean; onOpenAgent?: (ref: string) => void }) {
  const [open, setOpen] = useState<string | null>(null);
  const top = Math.max(1, ...report.agents.map((a) => a.total));
  const since = report.since;

  return (
    <Card title="By agent" icon={Users} description="Most expensive first. Open one for its models and its last turns.">
      <div className="overflow-x-auto rounded-xl border border-line-faint">
        <table className="w-full min-w-[38rem] text-[12px]">
          <thead>
            <tr className="whitespace-nowrap border-b border-line text-left text-[10.5px] uppercase tracking-[0.06em] text-subtle">
              <th className="py-1.5 pl-3 pr-2 font-medium">Agent</th>
              <th className="px-2 py-1.5 font-medium">Tokens</th>
              <th className="px-2 py-1.5 text-right font-medium">Cache read</th>
              <th className="px-2 py-1.5 text-right font-medium">Cost</th>
              <th className="py-1.5 pl-2 pr-3 text-right font-medium" title="The fullest its context was when a turn ended: what each call of that turn carried">
                Peak context
              </th>
            </tr>
          </thead>
          <tbody>
            {report.agents.map((a) => {
              const { name, sub } = agentName(a, scoped);
              const expanded = open === a.ref;
              const canOpen = a.exists && a.agent !== A.LeadName && onOpenAgent;
              return (
                <Fragment key={a.ref}>
                  <tr
                    className={cn('cursor-pointer border-t border-line-faint first:border-t-0 hover:bg-surface-faint', expanded && 'bg-surface-faint')}
                    onClick={() => setOpen(expanded ? null : a.ref)}
                    data-token-agent={a.ref}
                  >
                    <td className="py-2 pl-3 pr-2 align-top">
                      <div className="flex min-w-0 items-start gap-1.5">
                        <ChevronRight className={cn('mt-0.5 size-3.5 shrink-0 text-faint transition-transform', expanded && 'rotate-90')} />
                        {/* A title can be a sentence: it truncates rather than
                            pushing the numbers out of the column. */}
                        <div className="min-w-0 max-w-[16rem]">
                          <div className="flex min-w-0 items-center gap-1.5">
                            {canOpen ? (
                              <button
                                type="button"
                                className="truncate text-left font-medium text-primary hover:underline"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  onOpenAgent(a.ref);
                                }}
                              >
                                {name}
                              </button>
                            ) : (
                              <span className="truncate font-medium text-primary" title={name}>
                                {name}
                              </span>
                            )}
                            {!a.exists && a.agent !== A.LeadName && <Badge>retired</Badge>}
                          </div>
                          <div className="truncate text-[10.5px] text-faint">
                            <span className="font-mono">{sub}</span> · {timeAgo(a.lastAt)}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td className="px-2 py-2 align-top">
                      <div className="flex items-center gap-2">
                        <span className="w-14 shrink-0 text-right tabular-nums text-secondary">{humanTokens(a.total)}</span>
                        <span className="h-1.5 w-20 overflow-hidden rounded-full bg-surface-strong" aria-hidden>
                          <span className="block h-full rounded-full bg-brand-400" style={{ width: `${Math.max((a.total / top) * 100, 2)}%` }} />
                        </span>
                        <span className="w-9 shrink-0 tabular-nums text-faint">{share(a.total, report.total)}</span>
                      </div>
                    </td>
                    <td className="px-2 py-2 text-right align-top tabular-nums text-tertiary">{humanTokens(a.cacheRead)}</td>
                    <td className="px-2 py-2 text-right align-top tabular-nums text-secondary">{usd(a.costUSD)}</td>
                    <td className="py-2 pl-2 pr-3 text-right align-top tabular-nums text-tertiary">{a.maxContext ? humanTokens(a.maxContext) : '—'}</td>
                  </tr>
                  {expanded && (
                    <tr className="bg-surface-faint">
                      <td colSpan={5} className="px-3 pb-3 pt-1">
                        {/* w-0 min-w-full: the detail takes the row's width
                            instead of lending its own to the table, which
                            would push the numbers above out of view. */}
                        <div className="w-0 min-w-full">
                          <AgentDetail agent={a} since={since} />
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function AgentDetail({ agent, since }: { agent: T.AgentTokens; since?: string }) {
  return (
    <div className="grid grid-cols-1 gap-3">
      <p className="text-[11.5px] tabular-nums text-muted">
        {agent.turns} turn{agent.turns === 1 ? '' : 's'} · {humanTokens(agent.output)} output · {humanTokens(agent.cacheWrite)} cache writes ·{' '}
        {humanTokens(agent.input)} fresh input
      </p>
      <ModelBreakdown models={agent.models} />
      <RecentTurns query={{ project: agent.project, agent: agent.agent, since }} limit={12} />
    </div>
  );
}

// ModelBreakdown is an agent's spend split by model. A subagent on another
// model is its own line: Claude Code bills it to the turn that started it.
export function ModelBreakdown({ models }: { models: T.ModelTokens[] }) {
  const total = models.reduce((sum, m) => sum + m.total, 0);
  return (
    <div className="min-w-0">
      <div className="mb-1.5 text-[10.5px] font-medium uppercase tracking-[0.06em] text-subtle">By model</div>
      <ul className="grid gap-x-6 gap-y-1.5 sm:grid-cols-2">
        {models.map((m) => (
          <li key={m.model || 'unnamed'} className="min-w-0">
            <div className="flex items-baseline justify-between gap-2 text-[12px]">
              <span className="truncate font-mono text-[11.5px] text-secondary">{m.model || 'unnamed model'}</span>
              <span className="shrink-0 tabular-nums text-muted">{humanTokens(m.total)}</span>
            </div>
            <div className="mt-1 h-1 overflow-hidden rounded-full bg-surface-strong" aria-hidden>
              <div className="h-full rounded-full bg-brand-400" style={{ width: `${Math.max((m.total / Math.max(total, 1)) * 100, 2)}%` }} />
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

// RecentTurns is the ledger itself, newest first: the lines an audit reads.
export function RecentTurns({ query, limit }: { query: TokenQuery; limit: number }) {
  const turns = useQuery({
    queryKey: ['tokenTurns', query.project ?? '', query.agent ?? '', query.since ?? '', limit],
    queryFn: () => api.tokenTurns(query, limit),
    refetchInterval: 15_000,
  });
  return (
    <div className="min-w-0">
      <div className="mb-1.5 text-[10.5px] font-medium uppercase tracking-[0.06em] text-subtle">Last turns</div>
      {turns.error && <Notice>{errorMessage(turns.error)}</Notice>}
      {turns.data && turns.data.length === 0 && <p className="text-[12px] text-subtle">None in this stretch.</p>}
      {turns.data && turns.data.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-line-faint bg-surface">
          <table className="w-full text-[11.5px]">
            <thead>
              <tr className="whitespace-nowrap border-b border-line text-left text-[10px] uppercase tracking-[0.06em] text-subtle">
                <th className="py-1 pl-2.5 pr-2 font-medium">When</th>
                <th className="px-2 py-1 font-medium">Kind</th>
                <th className="px-2 py-1 font-medium">Model</th>
                <th className="px-2 py-1 text-right font-medium">Tokens</th>
                <th className="px-2 py-1 text-right font-medium">Cost</th>
                <th className="py-1 pl-2 pr-2.5 text-right font-medium">Context</th>
              </tr>
            </thead>
            <tbody>
              {turns.data.map((t, i) => (
                <tr key={`${t.turn}-${t.model}-${i}`} className="border-t border-line-faint first:border-t-0">
                  <td className="py-1 pl-2.5 pr-2 whitespace-nowrap text-muted" title={new Date(t.at).toLocaleString()}>
                    {timeAgo(t.at)}
                  </td>
                  <td className="px-2 py-1">
                    <Badge variant={kindVariant[t.kind] ?? 'default'}>{t.kind}</Badge>
                  </td>
                  <td className="max-w-[9rem] truncate px-2 py-1 font-mono text-[11px] text-tertiary" title={t.model}>
                    {t.model || '—'}
                  </td>
                  <td className="px-2 py-1 text-right tabular-nums text-secondary" title={`${humanTokens(t.cacheRead)} cache read · ${humanTokens(t.cacheWrite)} cache write · ${humanTokens(t.input)} input · ${humanTokens(t.output)} output`}>
                    {t.total ? humanTokens(t.total) : '—'}
                  </td>
                  <td className="px-2 py-1 text-right tabular-nums text-tertiary">{usd(t.costUSD)}</td>
                  <td className="py-1 pl-2 pr-2.5 text-right tabular-nums text-tertiary">{t.context ? humanTokens(t.context) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// AgentTokensCard is one agent's line of the ledger, on its Overview tab.
export function AgentTokensCard({ agent }: { agent: T.Agent }) {
  const [project, name] = agent.ref.split('/');
  const all = useQuery({ queryKey: ['tokens', project, name, 'all'], queryFn: () => api.tokens({ project, agent: name }), refetchInterval: 15_000 });
  const recent = useQuery({
    queryKey: ['tokens', project, name, '5h'],
    queryFn: () => api.tokens({ project, agent: name, since: '5h' }),
    refetchInterval: 15_000,
  });
  const mine = all.data?.agents[0];
  const lately = recent.data?.agents[0];

  return (
    <Card
      className="mt-3"
      title="What it spent"
      icon={Coins}
      description="This agent's chat, from the token ledger. A Claude Code started by hand in its terminal isn't counted."
    >
      {all.error && <Notice>{errorMessage(all.error)}</Notice>}
      {all.isPending && <p className="text-[13px] text-subtle">Loading…</p>}
      {all.data && !mine && <p className="text-[13px] text-subtle">Nothing yet: its chat hasn't finished a turn since AgentBox started keeping the ledger.</p>}
      {mine && (
        <div className="grid grid-cols-1 gap-4" data-agent-tokens={agent.ref}>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Stat label="Tokens" value={humanTokens(mine.total)} hint={`${share(mine.cacheRead, mine.total)} of them cache reads`} />
            <Stat label="Last 5 hours" value={lately ? humanTokens(lately.total) : '0'} hint={lately ? usd(lately.costUSD) : 'Nothing spent'} />
            <Stat label="Estimated cost" value={usd(mine.costUSD)} hint="At API prices" />
            <Stat label="Peak context" value={mine.maxContext ? humanTokens(mine.maxContext) : '—'} hint={`${mine.turns} turns`} />
          </div>
          <ModelBreakdown models={mine.models} />
          <RecentTurns query={{ project, agent: name }} limit={15} />
        </div>
      )}
    </Card>
  );
}
