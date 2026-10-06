import { useQuery } from '@tanstack/react-query';
import { ArrowRight, Camera, FolderPlus, Layers, Monitor, Plus, Sparkles, SquareTerminal, TriangleAlert, Wrench } from 'lucide-react';
import type { ComponentType, ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { formatList, formatNumber, useT } from '../lib/i18n';
import { summarizeStatus, type StatusTone } from '../lib/agentStatus';
import { cn, humanBytes, humanRate, shortRate, stallPressure, timeAgo } from '../lib/utils';
import { AllAgentsPanel } from './AllAgentsPanel';
import { JobStatusBadge } from './state';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Panel } from './ui/card';
import { Skeleton } from './ui/skeleton';

export function HomeView({ onSelect, onAddProject, onNewAgent }: { onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: () => void }) {
  const t = useT();
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
  // Counts are only counts once both lists are in: "0 of 0 agents" before
  // then would say there are none.
  const counted = agents.data !== undefined && projects.data !== undefined;
  const host = usage.data?.host;
  const pressure = host?.pressure;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-title">{t('shell.home.title')}</h1>
            {counted ? (
              <p className="mt-1 text-sm text-muted">
                {t('shell.home.summary', { running, total: all.length, projects: projects.data.length })}
              </p>
            ) : (
              <Skeleton className="mt-2 h-3.5 w-64" />
            )}
          </div>
          <div className="ml-auto flex gap-2">
            <Button onClick={onAddProject}>
              <FolderPlus />
              {t('shell.home.addProject')}
            </Button>
            <Button variant="ghost" onClick={onNewAgent}>
              <Plus />
              {t('shell.home.newAgent')}
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
            <span className="text-[13px] text-amber-50">{t('shell.home.setupPending')}</span>
            <span className="ml-auto flex items-center gap-1 text-[13px] font-medium text-amber-200">
              {t('shell.home.openSettings')} <ArrowRight className="size-3.5" />
            </span>
          </button>
        )}

        {pressure?.stalling && usage.data && <StallBanner usage={usage.data} agents={all} onSelect={onSelect} className="mt-6" />}

        {/* By the width Home actually gets, between the sidebar and the rail,
            not the window's: five across only where each still fits its value. */}
        <div className="@container mt-6">
          <div className="grid grid-cols-2 gap-3 @xl:grid-cols-3 @5xl:grid-cols-5">
            <Stat label={t('shell.home.agentsRunning')} value={agents.data ? String(running) : '—'} detail={agents.data ? t('shell.home.ofCount', { count: all.length }) : ''} />
            <Stat label={t('shell.home.hostCpu')} value={host ? `${formatNumber(host.cpu, { maximumFractionDigits: 0 })}%` : '—'} detail={host ? t('shell.home.ofCores', { count: host.cores }) : ''} fraction={host ? host.cpu / 100 : undefined} />
            <Stat
              label={t('shell.home.hostMemory')}
              value={host ? humanBytes(host.memUsed) : '—'}
              detail={host ? t('shell.home.ofSize', { size: humanBytes(host.memTotal) }) : ''}
              fraction={host ? host.memUsed / host.memTotal : undefined}
              note={pressure && <Stalled percent={pressure.memoryFull} on="memory" />}
            />
            <Stat
              label={t('shell.home.hostDisk')}
              value={host ? shortRate(host.diskRead + host.diskWrite) : '—'}
              detail=""
              note={
                host && (
                  <>
                    <span className="block truncate" title={t('shell.home.diskTitle', { read: humanRate(host.diskRead), write: humanRate(host.diskWrite) })}>
                      {t('shell.home.diskNote', { read: shortRate(host.diskRead), write: shortRate(host.diskWrite) })}
                    </span>
                    {pressure && <Stalled percent={pressure.ioFull} on="disk" />}
                  </>
                )
              }
            />
            <Stat
              label={t('shell.home.pool')}
              value={host?.poolTotal ? humanBytes(host.poolUsed) : '—'}
              detail={host?.poolTotal ? t('shell.home.ofSize', { size: humanBytes(host.poolTotal) }) : ''}
              fraction={host?.poolTotal ? host.poolUsed / host.poolTotal : undefined}
            />
          </div>
        </div>

        <SectionTitle>
          {t('shell.home.agents')}
          {all.length > 0 && <span className="ml-1.5 rounded-full bg-surface-raised px-1.5 text-[10.5px] normal-case tracking-normal text-subtle">{all.length}</span>}
        </SectionTitle>
        <AllAgentsPanel onSelect={onSelect} onNewAgent={onNewAgent} />

        <SectionTitle>{t('shell.home.recentJobs')}</SectionTitle>
        <Panel className="divide-y divide-line-faint overflow-hidden" aria-busy={!jobs.data}>
          {!jobs.data &&
            ['w-40', 'w-28', 'w-36'].map((width) => (
              <div key={width} className="flex items-center gap-3 px-4 py-3" data-job-skeleton>
                <Skeleton className="h-4 w-16 rounded-full" />
                <Skeleton className={cn('h-3', width)} />
                <Skeleton className="ml-auto h-3 w-12" />
              </div>
            ))}
          {(jobs.data ?? []).slice(0, 6).map((job) => (
            <div key={job.id} className="flex items-center gap-3 px-4 py-2.5 text-[13px]">
              <JobStatusBadge status={job.status} />
              <span className="text-secondary">{job.kind}</span>
              <span className="truncate text-subtle">{job.target}</span>
              <span className="ml-auto text-xs text-subtle">{timeAgo(job.createdAt)}</span>
            </div>
          ))}
          {jobs.data?.length === 0 && <div className="px-4 py-6 text-center text-[13px] text-subtle">{t('shell.home.noJobs')}</div>}
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
  const items = summarizeStatus(agents);

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

// Stalled is one resource's pressure under its stat: the share of the last ten
// seconds nothing on the host could run for waiting on it (PSI's "full
// avg10"), rose past stallPressure, which is when the desktop freezes.
function Stalled({ percent, on }: { percent: number; on: 'disk' | 'memory' }) {
  const t = useT();
  const stalled = percent > stallPressure;
  return (
    <span
      className={cn('block', stalled ? 'font-medium text-rose-300' : 'text-subtle')}
      title={t('shell.home.stalledTitle', { percent: formatNumber(percent, { minimumFractionDigits: 1, maximumFractionDigits: 1 }), on, limit: stallPressure })}
      data-stalled={stalled || undefined}
    >
      {stalled && <TriangleAlert className="-mt-px mr-1 inline size-3" />}
      {t('shell.home.stalled', { percent: formatNumber(percent, { maximumFractionDigits: 0 }), on })}
    </span>
  );
}

// StallBanner says, above everything else on Home, that the host is stalling
// on disk or memory, and which agent is reading and writing the most — the
// first one to look at, or to stop.
function StallBanner({ usage, agents, onSelect, className }: { usage: T.Usage; agents: T.Agent[]; onSelect: (view: View) => void; className?: string }) {
  const t = useT();
  const p = usage.host.pressure!;
  const heaviest = [...usage.agents].sort((a, b) => b.diskRead + b.diskWrite - (a.diskRead + a.diskWrite))[0];
  const heaviestAgent = heaviest && agents.find((a) => a.ref === heaviest.ref);
  const causes = [
    p.ioFull > stallPressure && t('shell.home.waitingOn', { percent: formatNumber(p.ioFull, { maximumFractionDigits: 0 }), on: 'disk' }),
    p.memoryFull > stallPressure && t('shell.home.waitingOn', { percent: formatNumber(p.memoryFull, { maximumFractionDigits: 0 }), on: 'memory' }),
  ].filter(Boolean) as string[];
  return (
    <div className={cn('flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-2xl border border-rose-400/25 bg-rose-400/[0.07] px-4 py-3', className)} data-stall-banner>
      <TriangleAlert className="size-4 shrink-0 text-rose-300" />
      <span className="min-w-0 flex-1 text-[13px] text-primary">
        {t.rich('shell.home.stallBanner', { b: (c) => <span className="font-medium">{c}</span>, causes: formatList(causes) })}
      </span>
      {heaviest && heaviest.diskRead + heaviest.diskWrite > 0 && (
        <button className="flex shrink-0 items-center gap-1 text-[13px] font-medium text-rose-200 transition hover:text-rose-100" onClick={() => onSelect({ kind: 'agent', ref: heaviest.ref })}>
          <span className="max-w-72 truncate">{t('shell.home.busiest', { agent: heaviestAgent?.title || heaviest.ref })}</span>
          <span className="font-mono text-[12px] tabular-nums">{humanRate(heaviest.diskRead + heaviest.diskWrite)}</span>
          <ArrowRight className="size-3.5" />
        </button>
      )}
    </div>
  );
}

function Stat({ label, value, detail, fraction, note }: { label: string; value: string; detail: string; fraction?: number; note?: ReactNode }) {
  return (
    <Panel className="p-4">
      <div className="text-[12px] text-subtle">{label}</div>
      <div className="mt-1.5 flex flex-wrap items-baseline gap-x-1.5">
        <span className="whitespace-nowrap font-mono text-2xl font-medium tabular-nums tracking-tight text-title">{value}</span>
        <span className="text-xs text-subtle">{detail}</span>
      </div>
      {fraction !== undefined && (
        <div className="mt-3 h-1 overflow-hidden rounded-full bg-surface-raised">
          <div className="h-full rounded-full bg-gradient-to-r from-brand-400 to-sky-400" style={{ width: `${Math.max(Math.min(fraction, 1) * 100, 3)}%` }} />
        </div>
      )}
      {note && <div className="mt-2 text-[11.5px] tabular-nums text-subtle">{note}</div>}
    </Panel>
  );
}

function Welcome({ onAddProject, onSetup, setupReady }: { onAddProject: () => void; onSetup: () => void; setupReady?: boolean }) {
  const t = useT();
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-5 pb-12 pt-10 md:px-10 md:pt-16">
        <div className="animate-slide-up">
          <Badge variant="brand">
            <Sparkles />
            {t('shell.home.tagline')}
          </Badge>
          <h1 className="text-gradient mt-5 max-w-3xl text-4xl font-semibold leading-[1.08] tracking-tight md:text-5xl">{t('shell.home.welcome')}</h1>
          <p className="mt-5 max-w-2xl text-[15px] leading-relaxed text-muted">
            {t('shell.home.intro')}
          </p>
          <div className="mt-8 flex flex-wrap gap-2">
            <Button variant="primary" size="lg" onClick={onAddProject}>
              <FolderPlus />
              {t('shell.home.addProject')}
            </Button>
            <Button size="lg" onClick={onSetup}>
              {setupReady === false ? <TriangleAlert className="text-amber-300" /> : <Wrench />}
              {setupReady === false ? t('shell.home.finishSetup') : t('shell.home.checkSetup')}
            </Button>
          </div>
        </div>
        <div className="mt-10 grid grid-cols-1 gap-3 sm:grid-cols-2 md:mt-14 lg:grid-cols-4">
          <Feature icon={SquareTerminal} title={t('shell.home.featTerminal')}>
            {t('shell.home.featTerminalBody')}
          </Feature>
          <Feature icon={Monitor} title={t('shell.home.featDesktop')}>
            {t('shell.home.featDesktopBody')}
          </Feature>
          <Feature icon={Camera} title={t('shell.home.featMedia')}>
            {t('shell.home.featMediaBody')}
          </Feature>
          <Feature icon={Layers} title={t('shell.home.featSnapshots')}>
            {t('shell.home.featSnapshotsBody')}
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
