import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, Cpu, Gauge, HardDrive, Menu as MenuIcon, MemoryStick, Square, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import type { View } from '../App';
import { api } from '../lib/api';
import { useConnection } from '../lib/events';
import type * as T from '../../shared/api';
import { limitTone, windowNow } from '../lib/tokens';
import { pickMeter } from '../lib/usageMeter';
import { cn, humanBytes, timeAgo, timeUntil } from '../lib/utils';
import { AgentSwitcher } from './AgentSwitcher';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';
import { Tip } from './ui/tooltip';

export function TopBar({
  view,
  onSelect,
  onOpenNav,
  onNewAgent,
}: {
  view: View;
  onSelect: (view: View) => void;
  onOpenNav: () => void;
  onNewAgent: (project: string) => void;
}) {
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  const connection = useConnection();
  const host = usage.data?.host;

  const crumbs: { label: string; mono?: boolean; view?: View }[] = [];
  switch (view.kind) {
    case 'home':
      crumbs.push({ label: 'Home' });
      break;
    case 'jobs':
      crumbs.push({ label: 'Jobs' });
      break;
    case 'settings':
      crumbs.push({ label: 'Settings' });
      break;
    case 'project':
      crumbs.push({ label: view.project });
      break;
    case 'agent': {
      const [project, name] = view.ref.split('/');
      const agent = agents.data?.find((a) => a.ref === view.ref);
      crumbs.push({ label: project, view: { kind: 'project', project } }, { label: agent?.title || name, mono: !agent?.title });
    }
  }

  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 md:gap-4 md:px-5">
      <button
        aria-label="Open the menu"
        className="-ml-1 rounded-lg p-2 text-muted transition hover:bg-surface-raised hover:text-primary md:hidden"
        onClick={onOpenNav}
      >
        <MenuIcon className="size-5" />
      </button>
      <nav className="flex min-w-0 items-center gap-1.5 text-[13px]" aria-label="Breadcrumb">
        {crumbs.map((crumb, i) => (
          <span key={i} className="flex min-w-0 items-center gap-1.5">
            {i > 0 && <ChevronRight className="size-3.5 shrink-0 text-faint" />}
            {crumb.view ? (
              <button className="-mx-1 truncate rounded-md px-1 text-muted transition hover:bg-surface-raised hover:text-primary" onClick={() => onSelect(crumb.view!)}>
                {crumb.label}
              </button>
            ) : (
              <span className={cn('truncate font-medium text-primary', crumb.mono && 'font-mono text-[12.5px]')}>{crumb.label}</span>
            )}
          </span>
        ))}
      </nav>

      <div className="lg:hidden">
        <AgentSwitcher view={view} onSelect={onSelect} onNewAgent={onNewAgent} />
      </div>

      <div className="ml-auto flex items-center gap-2">
        {setup.data && !setup.data.ready && view.kind !== 'settings' && (
          <button
            className="flex items-center gap-1.5 rounded-full bg-amber-400/10 px-2.5 py-1 text-xs font-medium text-amber-200 ring-1 ring-inset ring-amber-400/25 transition hover:bg-amber-400/15"
            onClick={() => onSelect({ kind: 'settings' })}
          >
            <TriangleAlert className="size-3.5" />
            <span className="hidden sm:inline">Finish setup</span>
          </button>
        )}
        <UsageMeter view={view} agents={agents.data ?? []} />
        {host && (
          <>
            <CPUMeter host={host} onSelect={onSelect} />
            <MemoryMeter host={host} onSelect={onSelect} />
            {host.poolTotal > 0 && <StoragePoolMeter host={host} />}
          </>
        )}
        <Tip label={connection.error ?? (connection.state === 'connected' ? 'Connected to the AgentBox daemon' : 'Connecting to the daemon…')}>
          <span
            className="flex items-center gap-2 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs text-muted"
            data-connection={connection.state}
          >
            <span
              className={cn(
                'size-1.5 rounded-full',
                connection.state === 'connected' ? 'bg-emerald-400 animate-glow' : connection.state === 'connecting' ? 'bg-amber-400' : 'bg-rose-400',
              )}
            />
            <span className="hidden sm:inline">{connection.state === 'connected' ? 'Daemon' : connection.state === 'connecting' ? 'Connecting' : 'Offline'}</span>
          </span>
        </Tip>
      </div>
    </header>
  );
}

// MeterBar is the small filled pill every meter in the top bar shares: amber
// past 65%, rose past 85%.
function MeterBar({ percent, className }: { percent: number; className?: string }) {
  return (
    <span className={cn('h-1 w-8 overflow-hidden rounded-full bg-surface-strong', className)}>
      <span
        className={cn('block h-full rounded-full', percent > 85 ? 'bg-rose-400' : percent > 65 ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400')}
        style={{ width: `${Math.max(percent, 4)}%` }}
      />
    </span>
  );
}

// StoragePoolMeter is the "Storage pool" indicator: what Meter would show,
// but clicking it opens a popover breaking the total down by what's using it.
// The breakdown is only computed while the popover is open — disk usage is
// cheap to poll as a total (Usage.PoolSpace, a single Incus query already
// fetched for the meter itself) but not to break down, since that walks every
// worktree and media directory on the host and queries Incus once per machine
// and saved base.
function StoragePoolMeter({ host }: { host: T.HostUsage }) {
  const diskUsage = useQuery({ queryKey: ['diskUsage'], queryFn: api.diskUsage, enabled: false });
  const percent = Math.max(0, Math.min(1, host.poolUsed / host.poolTotal)) * 100;
  const text = humanBytes(host.poolUsed);
  const detail = `of ${humanBytes(host.poolTotal)}`;
  return (
    <Popover onOpenChange={(open) => open && diskUsage.refetch()}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 transition hover:bg-surface-raised lg:flex"
          aria-label={`Storage pool: ${text} ${detail}`}
        >
          <HardDrive className="size-3.5 text-subtle" />
          <span className="font-mono text-[11px] tabular-nums text-tertiary">{text}</span>
          <MeterBar percent={percent} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <DiskUsageBreakdown poolUsed={host.poolUsed} poolTotal={host.poolTotal} query={diskUsage} />
      </PopoverContent>
    </Popover>
  );
}

function DiskUsageBreakdown({
  poolUsed,
  poolTotal,
  query,
}: {
  poolUsed: number;
  poolTotal: number;
  query: ReturnType<typeof useQuery<T.DiskUsage>>;
}) {
  return (
    <div className="grid gap-2.5">
      <div className="flex items-center justify-between">
        <span className="text-[13px] font-medium text-primary">Storage pool</span>
        <span className="font-mono text-[11px] tabular-nums text-tertiary">
          {humanBytes(poolUsed)} of {humanBytes(poolTotal)}
        </span>
      </div>
      {query.isPending ? (
        <span className="py-1 text-[12px] text-muted">Measuring what's on disk…</span>
      ) : query.isError ? (
        <span className="py-1 text-[12px] text-rose-300">{query.error instanceof Error ? query.error.message : String(query.error)}</span>
      ) : (
        <>
          <div className="grid max-h-72 gap-3 overflow-y-auto pr-1">
            {query.data.categories.map((cat) => (
              <div key={cat.label} className="grid gap-1">
                <div className="flex items-center justify-between text-[12px] text-secondary">
                  <span className="font-medium">{cat.label}</span>
                  <span className="font-mono tabular-nums text-tertiary">{humanBytes(cat.bytes)}</span>
                </div>
                {cat.items && cat.items.length > 0 && (
                  <div className="grid gap-0.5 border-l border-line pl-2.5">
                    {cat.items.map((item) => (
                      <div key={item.label} className="flex items-center justify-between gap-3 text-[11.5px] text-muted">
                        <span className="min-w-0 truncate">{item.label}</span>
                        <span className="shrink-0 font-mono tabular-nums text-faint">{humanBytes(item.bytes)}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
          <div className="flex items-center justify-between border-t border-line pt-2 text-[12px] font-medium text-primary">
            <span>Total</span>
            <span className="font-mono tabular-nums">{humanBytes(query.data.total)}</span>
          </div>
        </>
      )}
    </div>
  );
}

// CPUMeter is the "Host CPU" indicator: what Meter would show, but clicking
// it opens a popover breaking the total down by agent, the same way
// StoragePoolMeter does for disk. Each agent's own CPU% is only sampled
// while the popover is open, the same interval the top bar's own figure
// already pays for the host as a whole.
function CPUMeter({ host, onSelect }: { host: T.HostUsage; onSelect: (view: View) => void }) {
  const cpuUsage = useQuery({ queryKey: ['cpuUsage'], queryFn: api.cpuUsage, enabled: false });
  const percent = Math.max(0, Math.min(1, host.cpu / 100)) * 100;
  const text = `${host.cpu.toFixed(0)}%`;
  const detail = `${host.cores} cores`;
  return (
    <Popover onOpenChange={(open) => open && cpuUsage.refetch()}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 transition hover:bg-surface-raised sm:flex"
          aria-label={`Host CPU: ${text} ${detail}`}
        >
          <Cpu className="size-3.5 text-subtle" />
          <span className="font-mono text-[11px] tabular-nums text-tertiary">{text}</span>
          <MeterBar percent={percent} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <CPUUsageBreakdown host={host} query={cpuUsage} onSelect={onSelect} />
      </PopoverContent>
    </Popover>
  );
}

function coresLabel(a: T.CPUUsageAgent): string {
  const effective = a.effectiveCores || 'every core';
  const configured = a.configuredCores || 'every core';
  if (effective === configured) return effective === 'every core' ? effective : `${effective} cores`;
  return `${effective} of ${configured} cores`;
}

function CPUUsageBreakdown({
  host,
  query,
  onSelect,
}: {
  host: T.HostUsage;
  query: ReturnType<typeof useQuery<T.CPUUsage>>;
  onSelect: (view: View) => void;
}) {
  return (
    <div className="grid gap-2.5">
      <div className="flex items-center justify-between">
        <span className="text-[13px] font-medium text-primary">Host CPU</span>
        <span className="font-mono text-[11px] tabular-nums text-tertiary">
          {host.cpu.toFixed(0)}% of {host.cores} cores
        </span>
      </div>
      {query.isPending ? (
        <span className="py-1 text-[12px] text-muted">Measuring CPU use…</span>
      ) : query.isError ? (
        <span className="py-1 text-[12px] text-rose-300">{query.error instanceof Error ? query.error.message : String(query.error)}</span>
      ) : (
        <div className="grid max-h-72 gap-2 overflow-y-auto pr-1">
          {query.data.agents.map((a) => (
            <AgentUsageRow
              key={a.ref}
              agentRef={a.ref}
              title={a.title}
              state={a.state}
              onSelect={onSelect}
              value={`${a.cpu.toFixed(0)}%`}
              detail={coresLabel(a)}
            />
          ))}
          <div className="flex items-center justify-between gap-3 border-t border-line pt-1.5 text-[11.5px] text-muted">
            <span>Host itself</span>
            <span className="font-mono tabular-nums text-faint">{query.data.otherCPU.toFixed(0)}%</span>
          </div>
        </div>
      )}
    </div>
  );
}

// MemoryMeter is the "Host memory" indicator, the same shape as CPUMeter: a
// popover breaking the total down by agent, each with the RAM and swap its
// own cgroup holds — a paused agent's row says so, since pausing doesn't
// free either.
function MemoryMeter({ host, onSelect }: { host: T.HostUsage; onSelect: (view: View) => void }) {
  const memoryUsage = useQuery({ queryKey: ['memoryUsage'], queryFn: api.memoryUsage, enabled: false });
  const percent = Math.max(0, Math.min(1, host.memUsed / host.memTotal)) * 100;
  const text = humanBytes(host.memUsed);
  const detail = `of ${humanBytes(host.memTotal)}`;
  return (
    <Popover onOpenChange={(open) => open && memoryUsage.refetch()}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 transition hover:bg-surface-raised lg:flex"
          aria-label={`Host memory: ${text} ${detail}`}
        >
          <MemoryStick className="size-3.5 text-subtle" />
          <span className="font-mono text-[11px] tabular-nums text-tertiary">{text}</span>
          <MeterBar percent={percent} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <MemoryUsageBreakdown host={host} query={memoryUsage} onSelect={onSelect} />
      </PopoverContent>
    </Popover>
  );
}

function MemoryUsageBreakdown({
  host,
  query,
  onSelect,
}: {
  host: T.HostUsage;
  query: ReturnType<typeof useQuery<T.MemoryUsage>>;
  onSelect: (view: View) => void;
}) {
  return (
    <div className="grid gap-2.5">
      <div className="flex items-center justify-between">
        <span className="text-[13px] font-medium text-primary">Host memory</span>
        <span className="font-mono text-[11px] tabular-nums text-tertiary">
          {humanBytes(host.memUsed)} of {humanBytes(host.memTotal)}
        </span>
      </div>
      {query.isPending ? (
        <span className="py-1 text-[12px] text-muted">Measuring memory use…</span>
      ) : query.isError ? (
        <span className="py-1 text-[12px] text-rose-300">{query.error instanceof Error ? query.error.message : String(query.error)}</span>
      ) : (
        <>
          <div className="grid max-h-72 gap-2 overflow-y-auto pr-1">
            {query.data.agents.map((a) => (
              <AgentUsageRow
                key={a.ref}
                agentRef={a.ref}
                title={a.title}
                state={a.state}
                onSelect={onSelect}
                value={humanBytes(a.memory + a.swap)}
                detail={[
                  `${humanBytes(a.memory)} RAM`,
                  a.swap > 0 ? `${humanBytes(a.swap)} swap` : null,
                  a.limit > 0 ? `of ${humanBytes(a.limit)} limit` : null,
                ]
                  .filter(Boolean)
                  .join(', ')}
                note={a.state === 'paused' && a.memory + a.swap > 0 ? 'Paused, but still holds this memory — Stop to free it.' : undefined}
              />
            ))}
            <div className="flex items-center justify-between gap-3 border-t border-line pt-1.5 text-[11.5px] text-muted">
              <span>Host itself</span>
              <span className="font-mono tabular-nums text-faint">{humanBytes(query.data.otherUsed)}</span>
            </div>
          </div>
          {query.data.zram && query.data.swapUsed > 0 && (
            <div className="border-t border-line pt-2 text-[11px] text-muted">
              Swap is zram: {humanBytes(query.data.swapUsed)} of swap really costs {humanBytes(query.data.zram.realBytes)} of RAM, compressed.
            </div>
          )}
        </>
      )}
    </div>
  );
}

// AgentUsageRow is one agent in the CPU or memory popover: a link to the
// agent, its usage, and a Stop button — Pause isn't enough for either meter,
// since a paused agent still holds its memory, and stopping is what frees it.
function AgentUsageRow({
  agentRef,
  title,
  state,
  onSelect,
  value,
  detail,
  note,
}: {
  agentRef: string;
  title?: string;
  state: string;
  onSelect: (view: View) => void;
  value: string;
  detail: string;
  note?: string;
}) {
  const queryClient = useQueryClient();
  const stop = useMutation({
    mutationFn: () => api.agentAction(agentRef, 'stop'),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
      await queryClient.invalidateQueries({ queryKey: ['usage'] });
      await queryClient.invalidateQueries({ queryKey: ['cpuUsage'] });
      await queryClient.invalidateQueries({ queryKey: ['memoryUsage'] });
    },
    onError: (err) => toast.error(String(err)),
  });
  const name = agentRef.split('/')[1];
  const label = title || name;
  const canStop = state === 'running' || state === 'paused';
  return (
    <div className="grid gap-0.5">
      <div className="flex items-center justify-between gap-3 text-[11.5px]">
        <button
          type="button"
          className="min-w-0 truncate text-left text-muted transition hover:text-primary hover:underline"
          onClick={() => onSelect({ kind: 'agent', ref: agentRef })}
        >
          {label}
          {state === 'paused' && <span className="ml-1 text-faint">(paused)</span>}
        </button>
        <span className="flex shrink-0 items-center gap-1.5">
          <span className="font-mono tabular-nums text-faint">{value}</span>
          {canStop && (
            <Tip label={`Stop ${label}`}>
              <button
                type="button"
                className="rounded p-0.5 text-faint transition hover:bg-surface-raised hover:text-rose-300"
                aria-label={`Stop ${label}`}
                disabled={stop.isPending}
                onClick={() => stop.mutate()}
              >
                <Square className="size-3" />
              </button>
            </Tip>
          )}
        </span>
      </div>
      <span className="truncate text-[11px] text-faint">{detail}</span>
      {note && <span className="text-[10.5px] text-amber-300/80">{note}</span>}
    </div>
  );
}

// UsageMeter is how much of a Claude account's five-hour window is used (D85),
// beside the host's own meters: the limit every agent on the account shares.
// The account is the one what's open spends — the agent's, the project's, or
// on Home the machine's default (pickMeter) — and the tooltip names it above
// every account's readings. A reading is what the last chat on that account
// was told, so the tooltip says when that was, and a window that has reset
// since shows no number rather than one that describes a window that is over.
function UsageMeter({ view, agents }: { view: View; agents: T.Agent[] }) {
  const limits = useQuery({ queryKey: ['claudeLimits'], queryFn: api.claudeLimits, refetchInterval: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agent = view.kind === 'agent' ? agents.find((a) => a.ref === view.ref) : undefined;
  const projectName = view.kind === 'project' ? view.project : view.kind === 'agent' ? view.ref.split('/')[0] : undefined;
  const project = projectName ? projects.data?.find((p) => p.name === projectName) : undefined;
  const pick = pickMeter({ limits: limits.data ?? [], project, agent });
  const account = pick.reading;
  const five = account?.windows.find((w) => w.name === 'five_hour') ?? account?.windows[0];
  if (!account || !five) return null;
  const now = windowNow(five);
  const tone = limitTone(now);
  const percent = now === null ? 0 : Math.max(0, Math.min(1, now)) * 100;
  return (
    <Tip
      label={
        <span className="grid gap-2">
          <span className="text-muted">
            Showing <span className="font-medium text-primary">{account.account}</span>, {pick.whose}
          </span>
          <LimitsTip limits={limits.data ?? []} shown={account.account} />
        </span>
      }
    >
      <span
        className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 md:flex"
        aria-label={`Claude account ${account.account}, ${five.label} window: ${now === null ? 'reset since the last reading' : `${Math.round(percent)}% used`}`}
        data-claude-meter={now === null ? 'reset' : Math.round(percent)}
        data-claude-account={account.account}
      >
        <Gauge className="size-3.5 text-subtle" />
        <span className="font-mono text-[11px] tabular-nums text-tertiary">{now === null ? '5h —' : `5h ${Math.round(percent)}%`}</span>
        <span className="h-1 w-8 overflow-hidden rounded-full bg-surface-strong">
          <span
            className={cn('block h-full rounded-full', tone === 'high' ? 'bg-rose-400' : tone === 'warn' ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400')}
            style={{ width: `${Math.max(percent, 4)}%` }}
          />
        </span>
      </span>
    </Tip>
  );
}

function LimitsTip({ limits, shown }: { limits: T.ClaudeLimit[]; shown: string }) {
  return (
    <span className="grid gap-2">
      {limits.map((l) => (
        <span key={l.account} className="grid gap-0.5">
          <span className="font-medium text-primary">
            {l.account === shown ? '▸ ' : ''}Claude · {l.account}
            {l.default ? ' (default)' : ''}
          </span>
          {l.windows.map((w) => {
            const now = windowNow(w);
            return (
              <span key={w.name} className="tabular-nums text-muted">
                {w.label}: {now === null ? `reset ${timeAgo(w.resetsAt)}` : `${Math.round(now * 100)}% used, resets in ${timeUntil(w.resetsAt)}`}
              </span>
            );
          })}
          <span className="text-faint">As of its last chat, {timeAgo(l.at)}</span>
        </span>
      ))}
    </span>
  );
}
