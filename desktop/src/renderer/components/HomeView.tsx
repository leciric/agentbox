import { useQuery } from '@tanstack/react-query';
import { ArrowRight, Camera, FolderPlus, Layers, Monitor, Plus, Sparkles, SquareTerminal, TriangleAlert, Wrench } from 'lucide-react';
import type { ComponentType, ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { chatLabel, type StatusTone } from '../lib/agentStatus';
import { cn, humanBytes, timeAgo } from '../lib/utils';
import { AllAgentsPanel } from './AllAgentsPanel';
import { JobStatusBadge } from './state';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Panel } from './ui/card';

export function HomeView({ onSelect, onAddProject, onNewAgent }: { onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: () => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });

  if (projects.data?.length === 0) {
    return <Welcome onAddProject={onAddProject} onSetup={() => onSelect({ kind: 'settings' })} setupReady={setup.data?.ready} />;
  }

  const all = agents.data ?? [];
  const running = all.filter((a) => a.state === 'running').length;
  const host = usage.data?.host;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-title">Home</h1>
            <p className="mt-1 text-sm text-muted">
              {running} of {all.length} agent{all.length === 1 ? '' : 's'} running, across {projects.data?.length ?? 0} project
              {projects.data?.length === 1 ? '' : 's'}
            </p>
          </div>
          <div className="ml-auto flex gap-2">
            <Button onClick={onAddProject}>
              <FolderPlus />
              Add project
            </Button>
            <Button variant="ghost" onClick={onNewAgent}>
              <Plus />
              New agent
            </Button>
          </div>
        </div>

        {all.length > 0 && <StatusSummary agents={all} className="mt-4" />}

        {setup.data && !setup.data.ready && (
          <button
            className="mt-6 flex w-full items-center gap-3 rounded-2xl border border-amber-400/20 bg-amber-400/[0.06] px-4 py-3 text-left transition hover:bg-amber-400/[0.09]"
            onClick={() => onSelect({ kind: 'settings' })}
          >
            <TriangleAlert className="size-4 text-amber-300" />
            <span className="text-[13px] text-amber-50">Something AgentBox needs isn't set up yet.</span>
            <span className="ml-auto flex items-center gap-1 text-[13px] font-medium text-amber-200">
              Open Settings <ArrowRight className="size-3.5" />
            </span>
          </button>
        )}

        <div className="mt-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Stat label="Agents running" value={String(running)} detail={`of ${all.length}`} />
          <Stat label="Host CPU, whole machine" value={host ? `${host.cpu.toFixed(0)}%` : '—'} detail={host ? `of all ${host.cores} cores` : ''} fraction={host ? host.cpu / 100 : undefined} />
          <Stat label="Host memory" value={host ? humanBytes(host.memUsed) : '—'} detail={host ? `of ${humanBytes(host.memTotal)}` : ''} fraction={host ? host.memUsed / host.memTotal : undefined} />
          <Stat
            label="Storage pool"
            value={host?.poolTotal ? humanBytes(host.poolUsed) : '—'}
            detail={host?.poolTotal ? `of ${humanBytes(host.poolTotal)}` : ''}
            fraction={host?.poolTotal ? host.poolUsed / host.poolTotal : undefined}
          />
        </div>

        <SectionTitle>
          Agents
          {all.length > 0 && <span className="ml-1.5 rounded-full bg-surface-raised px-1.5 text-[10.5px] normal-case tracking-normal text-subtle">{all.length}</span>}
        </SectionTitle>
        <AllAgentsPanel onSelect={onSelect} onNewAgent={onNewAgent} />

        <SectionTitle>Recent jobs</SectionTitle>
        <Panel className="divide-y divide-line-faint overflow-hidden">
          {(jobs.data ?? []).slice(0, 6).map((job) => (
            <div key={job.id} className="flex items-center gap-3 px-4 py-2.5 text-[13px]">
              <JobStatusBadge status={job.status} />
              <span className="text-secondary">{job.kind}</span>
              <span className="truncate text-subtle">{job.target}</span>
              <span className="ml-auto text-xs text-subtle">{timeAgo(job.createdAt)}</span>
            </div>
          ))}
          {jobs.data?.length === 0 && <div className="px-4 py-6 text-center text-[13px] text-subtle">Nothing has run yet.</div>}
        </Panel>
      </div>
    </div>
  );
}

function SectionTitle({ children }: { children: ReactNode }) {
  return <h2 className="mb-3 mt-8 flex items-center text-[12px] font-semibold uppercase tracking-[0.08em] text-subtle">{children}</h2>;
}

const pillTone: Record<StatusTone, string> = {
  urgent: 'border-amber-400/25 bg-amber-400/[0.08] text-amber-200',
  error: 'border-rose-400/25 bg-rose-400/[0.08] text-rose-200',
  live: 'border-sky-400/20 bg-sky-400/[0.07] text-sky-200',
  muted: 'border-line-strong bg-surface-faint text-muted',
};

const pillDot: Record<StatusTone, string> = {
  urgent: 'bg-amber-400 animate-pulse',
  error: 'bg-rose-400',
  live: 'bg-sky-400 animate-pulse',
  muted: 'bg-faint',
};

// A one-line breakdown of what the whole fleet is up to, grouped by the same
// labels lib/agentStatus hands the rail and the sidebar. Ordered by urgency,
// so whatever needs you is the first thing you see on the page - before you've
// scrolled to a single row of the list below.
function StatusSummary({ agents, className }: { agents: T.Agent[]; className?: string }) {
  const order: StatusTone[] = ['urgent', 'error', 'live', 'muted'];
  const counts = new Map<string, { tone: StatusTone; count: number }>();
  for (const agent of agents) {
    const { text, tone } = chatLabel(agent);
    const entry = counts.get(text);
    if (entry) entry.count++;
    else counts.set(text, { tone, count: 1 });
  }
  const items = Array.from(counts, ([text, v]) => ({ text, ...v })).sort((a, b) => order.indexOf(a.tone) - order.indexOf(b.tone));

  return (
    <div className={cn('flex flex-wrap items-center gap-2', className)}>
      {items.map((item) => (
        <span key={item.text} className={cn('inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[12px] font-medium', pillTone[item.tone])}>
          <span className={cn('size-1.5 shrink-0 rounded-full', pillDot[item.tone])} />
          {item.count} {item.text}
        </span>
      ))}
    </div>
  );
}

function Stat({ label, value, detail, fraction }: { label: string; value: string; detail: string; fraction?: number }) {
  return (
    <Panel className="p-4">
      <div className="text-[12px] text-subtle">{label}</div>
      <div className="mt-1.5 flex items-baseline gap-1.5">
        <span className="font-mono text-2xl font-medium tabular-nums tracking-tight text-title">{value}</span>
        <span className="text-xs text-subtle">{detail}</span>
      </div>
      {fraction !== undefined && (
        <div className="mt-3 h-1 overflow-hidden rounded-full bg-surface-raised">
          <div className="h-full rounded-full bg-gradient-to-r from-brand-400 to-sky-400" style={{ width: `${Math.max(Math.min(fraction, 1) * 100, 3)}%` }} />
        </div>
      )}
    </Panel>
  );
}

function Welcome({ onAddProject, onSetup, setupReady }: { onAddProject: () => void; onSetup: () => void; setupReady?: boolean }) {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-5 pb-12 pt-10 md:px-10 md:pt-16">
        <div className="animate-slide-up">
          <Badge variant="brand">
            <Sparkles />
            Isolated machines for AI coding agents
          </Badge>
          <h1 className="text-gradient mt-5 max-w-3xl text-4xl font-semibold leading-[1.08] tracking-tight md:text-5xl">Welcome to AgentBox</h1>
          <p className="mt-5 max-w-2xl text-[15px] leading-relaxed text-muted">
            Every agent gets its own Linux machine: a worktree on its own branch, a terminal, a real browser you can watch and take over, and a place for the
            screenshots and recordings that show its work. Run several side by side.
          </p>
          <div className="mt-8 flex flex-wrap gap-2">
            <Button variant="primary" size="lg" onClick={onAddProject}>
              <FolderPlus />
              Add project
            </Button>
            <Button size="lg" onClick={onSetup}>
              {setupReady === false ? <TriangleAlert className="text-amber-300" /> : <Wrench />}
              {setupReady === false ? 'Finish setup' : 'Check setup'}
            </Button>
          </div>
        </div>
        <div className="mt-10 grid grid-cols-1 gap-3 sm:grid-cols-2 md:mt-14 lg:grid-cols-4">
          <Feature icon={SquareTerminal} title="Terminal">
            Claude Code, Codex or OpenCode in its own tmux session, still there when you come back.
          </Feature>
          <Feature icon={Monitor} title="Desktop">
            Chromium on the agent's own display. Watch it work, then take over.
          </Feature>
          <Feature icon={Camera} title="Media">
            Screenshots, recordings and test reports the agent keeps as proof.
          </Feature>
          <Feature icon={Layers} title="Snapshots">
            Save the whole machine, restore it, or fork a new agent from it.
          </Feature>
        </div>
      </div>
    </div>
  );
}

function Feature({ icon: Icon, title, children }: { icon: ComponentType<{ className?: string }>; title: string; children: ReactNode }) {
  return (
    <Panel className="p-4">
      <span className="flex size-9 items-center justify-center rounded-xl bg-gradient-to-br from-brand-500/25 to-sky-500/10 text-brand-200 ring-1 ring-inset ring-line-strong">
        <Icon className="size-4" />
      </span>
      <div className="mt-3 text-sm font-semibold text-primary">{title}</div>
      <p className="mt-1 text-[13px] leading-relaxed text-subtle">{children}</p>
    </Panel>
  );
}
