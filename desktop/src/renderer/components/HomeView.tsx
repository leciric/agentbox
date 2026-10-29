import { useQuery } from '@tanstack/react-query';
import { ArrowRight, Camera, ChevronDown, FolderPlus, Layers, Link, Monitor, Plus, Sparkles, SquareTerminal, TriangleAlert, Wrench } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ComponentProps, type ComponentType, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { formatList, formatNumber, useT } from '../lib/i18n';
import { summarizeStatus, type StatusTone } from '../lib/agentStatus';
import { fitCount, meterPercent } from '../lib/meter';
import { cn, humanBytes, humanRate, shortRate, stallPressure, timeAgo } from '../lib/utils';
import { AllAgentsPanel } from './AllAgentsPanel';
import { WorkList } from './WorkView';
import { JobStatusBadge } from './state';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Panel } from './ui/card';
import { Menu, MenuContent, MenuTrigger } from './ui/menu';
import { Skeleton } from './ui/skeleton';
import { openSettingsSection } from './SettingsPage';

// heading, project, filter and pastAgents are v2's, where this page is Home's
// Status tab: no title of its own (Home's is above it), a project filter
// (filter, drawn beside the summary) narrowing its lists, and no Past agents,
// which the sidebar's foot opens.
export function HomeView({
  onSelect,
  onAddProject,
  onCloneProject,
  onNewAgent,
  heading = true,
  project,
  filter,
  pastAgents = true,
}: {
  onSelect: (view: View) => void;
  onAddProject: () => void;
  onCloneProject?: () => void;
  onNewAgent: () => void;
  heading?: boolean;
  project?: string;
  filter?: ReactNode;
  pastAgents?: boolean;
}) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });

  if (projects.data?.length === 0) {
    return <Welcome onAddProject={onAddProject} onCloneProject={onCloneProject ?? onAddProject} onSetup={() => openSettingsSection('setup')} setupReady={setup.data?.ready} />;
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
      {/* v2's Status tab is as wide as Home's tab bar above it; v1's page keeps
          its centred column. */}
      <div className={cn('px-4 md:px-8', heading ? 'mx-auto max-w-6xl py-6 md:py-8' : 'pb-6 pt-2 md:pb-8')}>
        <div className={cn('flex flex-wrap gap-4', heading ? 'items-end' : 'items-center')}>
          <div>
            {heading && <h1 className="text-2xl font-semibold tracking-tight text-title">{t('shell.home.title')}</h1>}
            {counted ? (
              <p className={cn('text-sm text-muted', heading && 'mt-1')}>
                {t('shell.home.summary', { running, total: all.length, projects: projects.data.length })}
              </p>
            ) : (
              <Skeleton className="mt-2 h-3.5 w-64" />
            )}
          </div>
          <div className="ml-auto flex flex-wrap gap-2">
            {filter}
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

        {/* One strip, its cells divided by lines, that wraps by the width Home
            actually gets: each cell grows to fill its row, so a short last row
            never leaves a hole. */}
        <Panel className="mt-6 overflow-hidden" data-home-stats>
          <div className="-ml-px -mt-px flex flex-wrap">
            <Stat label={t('shell.home.agentsRunning')} value={agents.data ? String(running) : '—'} detail={agents.data ? t('shell.home.ofCount', { count: all.length }) : ''} />
            <Stat label={t('shell.home.hostCpu')} value={host ? `${formatNumber(host.cpu, { maximumFractionDigits: 0 })}%` : '—'} detail={host?.cores ? t('shell.home.ofCores', { count: host.cores }) : ''} percent={host?.cores ? meterPercent(host.cpu, 100) : undefined} meter={!!host} />
            <Stat
              label={t('shell.home.hostMemory')}
              value={host?.memTotal ? humanBytes(host.memUsed) : '—'}
              detail={host ? (host.memTotal ? t('shell.home.ofSize', { size: humanBytes(host.memTotal) }) : t('common.unknown')) : ''}
              percent={host?.memTotal ? meterPercent(host.memUsed, host.memTotal) : undefined}
              meter={!!host}
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
              percent={host?.poolTotal ? meterPercent(host.poolUsed, host.poolTotal) : undefined}
            />
          </div>
        </Panel>

        <SectionTitle>
          {t('shell.home.agents')}
          {all.length > 0 && <span className="ml-1.5 rounded-full bg-surface-raised px-1.5 text-xs font-medium text-subtle">{all.length}</span>}
        </SectionTitle>
        <AllAgentsPanel project={project || undefined} onSelect={onSelect} onNewAgent={onNewAgent} />

        {/* The latest work, live agents' and past ones' (WorkView). */}
        {pastAgents && (
          <>
            <SectionTitle>{t('work.title')}</SectionTitle>
            <WorkList key={project ?? ''} project={project || undefined} limit={8} onSelect={onSelect} onShowAll={() => onSelect({ kind: 'work', project: project || undefined })} />
          </>
        )}

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
  return <h2 className="mb-3 mt-8 flex items-center text-base font-semibold text-title">{children}</h2>;
}

const pillDot: Record<StatusTone, string> = {
  urgent: 'bg-amber-400 animate-pulse',
  error: 'bg-rose-400',
  warning: 'bg-amber-400/70',
  live: 'bg-sky-400 animate-pulse',
  muted: 'bg-faint',
};

function StatusPill({ item }: { item: ReturnType<typeof summarizeStatus>[number] }) {
  return (
    <span className="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs font-medium text-secondary" data-status-pill={item.tone}>
      <span className={cn('size-1.5 shrink-0 rounded-full', pillDot[item.tone])} />
      {item.count} {item.text}
    </span>
  );
}

// A one-line breakdown of what the whole fleet is up to, grouped by the same
// labels lib/agentStatus hands the rail and the sidebar. Ordered by urgency,
// so whatever needs you is the first thing you see on the page - before you've
// scrolled to a single row of the list below. It stays one line whatever the
// width: what doesn't fit goes behind a "more" button at its end, so the
// list below it is never pushed off a small window. The chips are neutral,
// with only their dot in the status's colour.
function StatusSummary({ agents, className }: { agents: T.Agent[]; className?: string }) {
  const t = useT();
  const items = summarizeStatus(agents);
  const row = useRef<HTMLDivElement>(null);
  const measure = useRef<HTMLDivElement>(null);
  const [shown, setShown] = useState(items.length);
  const key = items.map((i) => `${i.count} ${i.text}`).join('|');

  // The chips are measured in a hidden copy of the whole row, so how many
  // fit doesn't depend on how many are showing.
  useLayoutEffect(() => {
    const el = row.current;
    const copy = measure.current;
    if (!el || !copy) return;
    const fit = () => {
      const chips = Array.from(copy.children) as HTMLElement[];
      const more = chips.pop();
      const gap = parseFloat(getComputedStyle(copy).columnGap) || 0;
      setShown(fitCount(chips.map((c) => c.offsetWidth), el.clientWidth, gap, (more?.offsetWidth ?? 0) + gap));
    };
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(el);
    return () => observer.disconnect();
  }, [key]);

  const hidden = items.slice(shown);
  return (
    <div ref={row} className={cn('relative flex min-w-0 items-center gap-2 overflow-hidden', className)} data-status-summary>
      <div ref={measure} aria-hidden className="pointer-events-none invisible absolute left-0 top-0 flex gap-2">
        {items.map((item) => (
          <StatusPill key={item.text} item={item} />
        ))}
        <MoreButton count={99} />
      </div>
      {items.slice(0, shown).map((item) => (
        <StatusPill key={item.text} item={item} />
      ))}
      {hidden.length > 0 && (
        <Menu>
          <MenuTrigger asChild>
            <MoreButton data-status-more count={hidden.length} label={t('shell.home.moreStatuses', { count: hidden.length })} />
          </MenuTrigger>
          <MenuContent align="start" className="flex max-w-md flex-wrap gap-2 p-2">
            {hidden.map((item) => (
              <StatusPill key={item.text} item={item} />
            ))}
          </MenuContent>
        </Menu>
      )}
    </div>
  );
}

function MoreButton({ count, label, ...props }: ComponentProps<'button'> & { count: number; label?: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className="inline-flex shrink-0 items-center gap-1 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs font-medium text-secondary transition hover:bg-surface-raised"
      {...props}
    >
      +{formatNumber(count)}
      <ChevronDown className="size-3.5 text-subtle" />
    </button>
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

// Stat is one cell of Home's load strip. percent draws its bar; meter
// without a percent draws the bar's empty track, for a total that isn't known.
function Stat({ label, value, detail, percent, meter, note }: { label: string; value: string; detail: string; percent?: number; meter?: boolean; note?: ReactNode }) {
  return (
    <div className="min-w-36 flex-1 border-l border-t border-line-faint px-4 py-3" data-stat>
      <div className="text-xs text-subtle">{label}</div>
      <div className="mt-1.5 flex flex-wrap items-baseline gap-x-1.5">
        <span className="whitespace-nowrap font-mono text-xl font-medium tabular-nums tracking-tight text-title">{value}</span>
        <span className="text-xs text-subtle">{detail}</span>
      </div>
      {(percent !== undefined || meter) && (
        <div className="mt-2.5 h-1 overflow-hidden rounded-full bg-surface-raised" data-meter={percent ?? 'unknown'}>
          {percent !== undefined && <div className="h-full rounded-full bg-gradient-to-r from-brand-400 to-sky-400" style={{ width: `${percent}%` }} />}
        </div>
      )}
      {note && <div className="mt-2 text-xs tabular-nums text-subtle">{note}</div>}
    </div>
  );
}

function Welcome({
  onAddProject,
  onCloneProject,
  onSetup,
  setupReady,
}: {
  onAddProject: () => void;
  onCloneProject: () => void;
  onSetup: () => void;
  setupReady?: boolean;
}) {
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
            <Button size="lg" onClick={onCloneProject} data-welcome-clone>
              <Link />
              {t('shell.home.cloneProject')}
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
