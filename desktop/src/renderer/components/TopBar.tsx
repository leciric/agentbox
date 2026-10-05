import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, Cpu, Gauge, HardDrive, Menu as MenuIcon, Square, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import type { View } from '../App';
import { api } from '../lib/api';
import { formatNumber, useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { useConnection } from '../lib/events';
import type * as T from '../../shared/api';
import { limitTone, windowNow } from '../lib/tokens';
import { pickMeter } from '../lib/usageMeter';
import { useNow } from '../lib/useNow';
import { useVMPower } from '../lib/vm';
import { bytesOf, cn, humanBytes, timeAgo, timeUntil } from '../lib/utils';
import { AgentSwitcher } from './AgentSwitcher';
import { DiskGuardPill } from './DiskGuardPill';
import { ResourceControls } from './ResourceControls';
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
  const t = useT();
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  const connection = useConnection();
  const vm = useVMPower().data;
  const host = usage.data?.host;

  const crumbs: { label: string; mono?: boolean; view?: View }[] = [];
  switch (view.kind) {
    case 'home':
      crumbs.push({ label: t('shell.nav.home') });
      break;
    case 'homeChat':
      crumbs.push({ label: t('shell.nav.mainChat') });
      break;
    case 'jobs':
      crumbs.push({ label: t('shell.nav.jobs') });
      break;
    case 'settings':
      crumbs.push({ label: t('common.settings') });
      break;
    case 'project':
      crumbs.push({ label: projectLabel(view.project, projects.data) });
      break;
    case 'agent': {
      const [project, name] = view.ref.split('/');
      const agent = agents.data?.find((a) => a.ref === view.ref);
      crumbs.push({ label: projectLabel(project, projects.data), view: { kind: 'project', project } }, { label: agent?.title || name, mono: !agent?.title });
    }
  }

  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 md:gap-4 md:px-5">
      <button
        aria-label={t('shell.top.openMenu')}
        className="-ml-1 rounded-lg p-2 text-muted transition hover:bg-surface-raised hover:text-primary md:hidden"
        onClick={onOpenNav}
      >
        <MenuIcon className="size-5" />
      </button>
      <nav className="flex min-w-0 items-center gap-1.5 text-[13px]" aria-label={t('shell.top.breadcrumb')}>
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
            <span className="hidden sm:inline">{t('shell.home.finishSetup')}</span>
          </button>
        )}
        <DiskGuardPill onSelect={onSelect} />
        <ResourceControls agents={agents.data ?? []} />
        <UsageMeter view={view} agents={agents.data ?? []} />
        {host && <CPUMeter host={host} onSelect={onSelect} />}
        <DiskMeter host={host} vmDisk={vm?.disk} />
        {/* In VM mode, the daemon of a VM that's off or paused can't answer:
            the VM's own pill says why, and this one would only repeat it as
            "Offline". */}
        {(!vm || vm.state === 'running') && (
          <Tip label={connection.error ?? (connection.state === 'connected' ? t('shell.top.connected') : t('shell.top.connectingDaemon'))}>
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
              <span className="hidden sm:inline">{connection.state === 'connected' ? t('shell.top.daemon') : connection.state === 'connecting' ? t('shell.top.connecting') : t('shell.top.offline')}</span>
            </span>
          </Tip>
        )}
      </div>
    </header>
  );
}

// MeterBar is the small filled pill every meter in the top bar shares: amber
// past 65%, rose past 85%; an empty track when percent isn't known.
function MeterBar({ percent, className }: { percent: number | null; className?: string }) {
  return (
    <span className={cn('h-1 w-8 overflow-hidden rounded-full bg-surface-strong', className)}>
      {percent !== null && (
        <span
          className={cn('block h-full rounded-full', percent > 85 ? 'bg-rose-400' : percent > 65 ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400')}
          style={{ width: `${Math.max(percent, 4)}%` }}
        />
      )}
    </span>
  );
}

// DiskMeter is the "Disk" indicator, as used/size. In VM mode it's what
// AgentBox's VM costs the host's disk against the most it can hold: the bytes
// its two disk images (the agents' pool and the VM's system disk) really
// take, against the sizes the VM's disk settings gave them, as the host's
// side measures them (VMPower.disk). Otherwise it's the Incus storage pool
// every agent's machine and saved base share. Clicking it opens a popover
// breaking that down; the breakdown is only measured while it's open — the
// total is cheap to poll, but the breakdown walks every worktree and media
// directory and queries Incus once per machine and base. Used is never shown
// as 0: an image can't take nothing, so 0 is a front end that couldn't
// measure it, shown as "—".
function DiskMeter({ host, vmDisk }: { host?: T.HostUsage; vmDisk?: T.VMDisk }) {
  const t = useT();
  const diskUsage = useQuery({ queryKey: ['diskUsage'], queryFn: api.diskUsage, enabled: false });
  const homeDisk = useQuery({ queryKey: ['homeDisk'], queryFn: () => window.agentbox.vm.disk(), enabled: false });
  const [used, size] = vmDisk ? [vmDisk.allocated, vmDisk.size] : [host?.poolUsed ?? 0, host?.poolTotal ?? 0];
  if (size <= 0) return null;
  const percent = used > 0 ? Math.max(0, Math.min(1, used / size)) * 100 : null;
  return (
    <Popover
      onOpenChange={(open) => {
        if (!open) return;
        void diskUsage.refetch();
        if (vmDisk) void homeDisk.refetch();
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 transition hover:bg-surface-raised lg:flex"
          aria-label={
            vmDisk
              ? t('shell.top.vmTakes', { used: knownBytes(used), size: humanBytes(size) })
              : t('shell.top.agentsDiskUsed', { used: knownBytes(used), size: humanBytes(size) })
          }
        >
          <HardDrive className="size-3.5 text-subtle" />
          <span className="whitespace-nowrap font-mono text-[11px] tabular-nums text-tertiary">{bytesOf(used, size)}</span>
          <MeterBar percent={percent} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-96">
        {vmDisk ? (
          <VMDiskBreakdown disk={vmDisk} host={host} home={homeDisk} pool={diskUsage} />
        ) : (
          <div className="grid gap-2.5">
            <div className="flex items-center justify-between">
              <span className="text-[13px] font-medium text-primary">{t('shell.top.agentsDisk')}</span>
              <span className="font-mono text-[11px] tabular-nums text-tertiary">
                {t('shell.top.usedOf', { used: knownBytes(used), size: humanBytes(size) })}
              </span>
            </div>
            <DiskUsageCategories query={diskUsage} />
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

// VMDiskBreakdown is the VM's disk images, each as what it takes on the
// host's disk (allocated) and the size the VM sees, never one against the
// other's kind; then what's on the host outside them, and what the agents'
// pool holds inside.
function VMDiskBreakdown({
  disk,
  host,
  home,
  pool,
}: {
  disk: T.VMDisk;
  host?: T.HostUsage;
  home: ReturnType<typeof useQuery<T.VMHomeDisk | null>>;
  pool: ReturnType<typeof useQuery<T.DiskUsage>>;
}) {
  const t = useT();
  // A sparse image can't grow past what the host has free.
  const room = disk.size - disk.allocated;
  const short = disk.allocated > 0 && disk.hostFree !== undefined && disk.hostFree > 0 && disk.hostFree < room;
  const freed = host && host.poolTotal > 0 && disk.pool.allocated > 0 ? disk.pool.allocated - host.poolUsed : 0;
  return (
    <div className="grid gap-3">
      <div className="grid gap-1">
        <div className="flex items-center justify-between">
          <span className="text-[13px] font-medium text-primary">{t('shell.top.vm')}</span>
          <span className="font-mono text-[11px] tabular-nums text-tertiary">
            {t('shell.top.usedOf', { used: knownBytes(disk.allocated), size: humanBytes(disk.size) })}
          </span>
        </div>
        <span className="text-[11.5px] text-muted">{t('shell.top.vmDisksNote')}</span>
      </div>
      <div className="grid grid-cols-[1fr_auto_auto] gap-x-4 gap-y-1 text-[12px]">
        <span />
        <span className="text-right text-[11px] text-faint">{t('shell.top.onYourDisk')}</span>
        <span className="text-right text-[11px] text-faint">{t('shell.top.size')}</span>
        <DiskImageRow label={t('shell.top.agentsPool')} image={disk.pool} />
        <DiskImageRow label={t('shell.top.vmSystemDisk')} image={disk.root} />
      </div>
      {host && host.poolTotal > 0 && (
        <span className="text-[11.5px] text-muted">
          {freed > 0.5 * 1024 ** 3
            ? t.rich('shell.top.poolInUseFreed', {
                used: <span className="font-mono tabular-nums text-secondary">{humanBytes(host.poolUsed)}</span>,
                freed: <span className="font-mono tabular-nums text-secondary">{humanBytes(freed)}</span>,
              })
            : t.rich('shell.top.poolInUse', { used: <span className="font-mono tabular-nums text-secondary">{humanBytes(host.poolUsed)}</span> })}
        </span>
      )}
      <div className="grid gap-1 border-t border-line pt-2.5">
        <span className="text-[12px] font-medium text-secondary">{t('shell.top.onYourComputer')}</span>
        {disk.hostFree !== undefined && disk.hostFree > 0 && <DiskRow label={t('shell.top.freeOnDisk')} bytes={disk.hostFree} />}
        {home.isPending || home.isFetching ? (
          <span className="text-[11.5px] text-muted">{t('shell.top.measuringHome')}</span>
        ) : home.isError ? (
          <span className="text-[11.5px] text-rose-300">{home.error instanceof Error ? home.error.message : String(home.error)}</span>
        ) : home.data ? (
          <>
            <DiskRow label={t('shell.top.worktreesHome')} bytes={home.data.worktrees} />
            <DiskRow label={t('shell.top.mediaHome')} bytes={home.data.media} />
          </>
        ) : null}
        {short && (
          <span className="text-[11.5px] text-amber-300">
            {t('shell.top.diskShort', { free: humanBytes(disk.hostFree ?? 0), room: humanBytes(room) })}
          </span>
        )}
      </div>
      <div className="grid gap-2 border-t border-line pt-2.5">
        <span className="text-[12px] font-medium text-secondary">{t('shell.top.inPool')}</span>
        <DiskUsageCategories query={pool} kinds={['machines', 'bases']} />
      </div>
    </div>
  );
}

function DiskImageRow({ label, image }: { label: string; image: T.VMDiskImage }) {
  return (
    <>
      <span className="text-secondary">{label}</span>
      <span className="text-right font-mono tabular-nums text-tertiary">{knownBytes(image.allocated)}</span>
      <span className="text-right font-mono tabular-nums text-faint">{humanBytes(image.size)}</span>
    </>
  );
}

// knownBytes is humanBytes for a disk's used bytes, "—" while they're 0, not
// known yet.
function knownBytes(n: number): string {
  return n > 0 ? humanBytes(n) : '—';
}

function DiskRow({ label, bytes }: { label: string; bytes: number }) {
  return (
    <div className="flex items-center justify-between gap-3 text-[11.5px] text-muted">
      <span className="min-w-0 truncate">{label}</span>
      <span className="shrink-0 font-mono tabular-nums text-tertiary">{humanBytes(bytes)}</span>
    </div>
  );
}

// DiskUsageCategories is the daemon's breakdown (/v1/usage/disk), largest
// first, with its total; only kinds' categories when kinds is given.
function DiskUsageCategories({ query, kinds }: { query: ReturnType<typeof useQuery<T.DiskUsage>>; kinds?: string[] }) {
  const t = useT();
  if (query.isPending) return <span className="py-1 text-[12px] text-muted">{t('shell.top.measuringDisk')}</span>;
  if (query.isError)
    return <span className="py-1 text-[12px] text-rose-300">{query.error instanceof Error ? query.error.message : String(query.error)}</span>;
  const categories = kinds ? query.data.categories.filter((cat) => kinds.includes(cat.kind)) : query.data.categories;
  const total = kinds ? categories.reduce((sum, cat) => sum + cat.bytes, 0) : query.data.total;
  return (
    <>
      <div className="grid max-h-72 gap-3 overflow-y-auto pr-1">
        {categories.map((cat) => (
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
        <span>{t('shell.imageDownloads.total')}</span>
        <span className="font-mono tabular-nums">{humanBytes(total)}</span>
      </div>
    </>
  );
}

// CPUMeter is the "Host CPU" indicator: what Meter would show, but clicking
// it opens a popover breaking the total down by agent, the same way
// DiskMeter does for disk. Each agent's own CPU% is only sampled
// while the popover is open, the same interval the top bar's own figure
// already pays for the host as a whole.
function CPUMeter({ host, onSelect }: { host: T.HostUsage; onSelect: (view: View) => void }) {
  const t = useT();
  const cpuUsage = useQuery({ queryKey: ['cpuUsage'], queryFn: api.cpuUsage, enabled: false });
  const percent = Math.max(0, Math.min(1, host.cpu / 100)) * 100;
  const text = `${formatNumber(host.cpu, { maximumFractionDigits: 0 })}%`;
  const detail = t('shell.top.cores', { count: host.cores });
  return (
    <Popover onOpenChange={(open) => open && cpuUsage.refetch()}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 transition hover:bg-surface-raised sm:flex"
          aria-label={t('shell.top.cpuLabel', { text, detail })}
        >
          <Cpu className="size-3.5 text-subtle" />
          <span className="w-8 whitespace-nowrap font-mono text-[11px] tabular-nums text-tertiary">{text}</span>
          <MeterBar percent={percent} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <CPUUsageBreakdown host={host} query={cpuUsage} onSelect={onSelect} />
      </PopoverContent>
    </Popover>
  );
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
  const t = useT();
  return (
    <div className="grid gap-2.5">
      <div className="flex items-center justify-between">
        <span className="text-[13px] font-medium text-primary">{t('shell.top.hostCpu')}</span>
        <span className="font-mono text-[11px] tabular-nums text-tertiary">
          {t('shell.top.cpuOf', { percent: formatNumber(host.cpu, { maximumFractionDigits: 0 }), count: host.cores })}
        </span>
      </div>
      {query.isPending ? (
        <span className="py-1 text-[12px] text-muted">{t('shell.top.measuringCpu')}</span>
      ) : query.isError ? (
        <span className="py-1 text-[12px] text-rose-300">{query.error instanceof Error ? query.error.message : String(query.error)}</span>
      ) : (
        <div className="grid min-w-0 max-h-72 gap-2 overflow-y-auto pr-1">
          {query.data.agents.map((a) => (
            <AgentUsageRow
              key={a.ref}
              agentRef={a.ref}
              title={a.title}
              state={a.state}
              onSelect={onSelect}
              value={`${formatNumber(a.cpu, { maximumFractionDigits: 0 })}%`}
            />
          ))}
          <div className="flex items-center justify-between gap-3 border-t border-line pt-1.5 text-[11.5px] text-muted">
            <span>{t('shell.top.hostItself')}</span>
            <span className="font-mono tabular-nums text-faint">{formatNumber(query.data.otherCPU, { maximumFractionDigits: 0 })}%</span>
          </div>
        </div>
      )}
    </div>
  );
}

// AgentUsageRow is one agent in the CPU popover: a link to the agent, its
// usage, and a Stop button — Pause isn't enough, since a paused agent still
// holds its memory, and stopping is what frees it.
function AgentUsageRow({
  agentRef,
  title,
  state,
  onSelect,
  value,
}: {
  agentRef: string;
  title?: string;
  state: string;
  onSelect: (view: View) => void;
  value: string;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const stop = useMutation({
    mutationFn: () => api.agentAction(agentRef, 'stop'),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
      await queryClient.invalidateQueries({ queryKey: ['usage'] });
      await queryClient.invalidateQueries({ queryKey: ['cpuUsage'] });
    },
    onError: (err) => toast.error(String(err)),
  });
  const name = agentRef.split('/')[1];
  const label = title || name;
  const canStop = state === 'running' || state === 'paused';
  return (
    <div className="grid min-w-0 gap-0.5">
      <div className="flex min-w-0 items-center justify-between gap-3 text-[11.5px]">
        <button
          type="button"
          className="min-w-0 truncate text-left text-muted transition hover:text-primary hover:underline"
          onClick={() => onSelect({ kind: 'agent', ref: agentRef })}
        >
          {label}
          {state === 'paused' && <span className="ml-1 text-faint">({t('shell.top.paused')})</span>}
        </button>
        <span className="flex shrink-0 items-center gap-1.5">
          <span className="font-mono tabular-nums text-faint">{value}</span>
          {canStop && (
            <Tip label={t('shell.top.stop', { name: label })}>
              <button
                type="button"
                className="rounded p-0.5 text-faint transition hover:bg-surface-raised hover:text-rose-300"
                aria-label={t('shell.top.stop', { name: label })}
                disabled={stop.isPending}
                onClick={() => stop.mutate()}
              >
                <Square className="size-3" />
              </button>
            </Tip>
          )}
        </span>
      </div>
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
// The label counts down to the reset, so it ticks every minute: the query's
// refetch leaves it alone while the reading is unchanged.
function UsageMeter({ view, agents }: { view: View; agents: T.Agent[] }) {
  const t = useT();
  const clock = useNow(60_000);
  const limits = useQuery({ queryKey: ['claudeLimits'], queryFn: api.claudeLimits, refetchInterval: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agent = view.kind === 'agent' ? agents.find((a) => a.ref === view.ref) : undefined;
  const projectName = view.kind === 'project' ? view.project : view.kind === 'agent' ? view.ref.split('/')[0] : undefined;
  const project = projectName ? projects.data?.find((p) => p.name === projectName) : undefined;
  const pick = pickMeter({ limits: limits.data ?? [], project, agent });
  const account = pick.reading;
  const five = account?.windows.find((w) => w.name === 'five_hour') ?? account?.windows[0];
  if (!account || !five) return null;
  const now = windowNow(five, clock);
  const left = timeUntil(five.resetsAt, clock);
  const tone = limitTone(now);
  const percent = now === null ? 0 : Math.max(0, Math.min(1, now)) * 100;
  return (
    <Tip
      label={
        <span className="grid gap-2">
          <span className="text-muted">
            {t.rich('shell.top.showing', { account: <span className="font-medium text-primary">{account.account}</span>, whose: pick.whose })}
          </span>
          <LimitsTip limits={limits.data ?? []} shown={account.account} />
        </span>
      }
    >
      <span
        className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 md:flex"
        aria-label={t('shell.top.claudeLabel', { account: account.account, window: five.label, state: now === null ? 'reset' : 'used', percent: Math.round(percent), left })}
        data-claude-meter={now === null ? 'reset' : Math.round(percent)}
        data-claude-account={account.account}
      >
        <Gauge className="size-3.5 text-subtle" />
        <span className="w-20 whitespace-nowrap font-mono text-[11px] tabular-nums text-tertiary">{now === null ? t('shell.top.fiveHourReset') : `${left} · ${Math.round(percent)}%`}</span>
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
  const t = useT();
  return (
    <span className="grid gap-2">
      {limits.map((l) => (
        <span key={l.account} className="grid gap-0.5">
          <span className="font-medium text-primary">
            {l.account === shown ? '▸ ' : ''}Claude · {l.account}
            {l.default ? ` (${t('common.default').toLowerCase()})` : ''}
          </span>
          {l.windows.map((w) => {
            const now = windowNow(w);
            return (
              <span key={w.name} className="tabular-nums text-muted">
                {w.label}: {now === null ? t('shell.top.resetAgo', { ago: timeAgo(w.resetsAt) }) : t('shell.top.usedResets', { percent: Math.round(now * 100), left: timeUntil(w.resetsAt) })}
              </span>
            );
          })}
          <span className="text-faint">{t('shell.top.asOf', { ago: timeAgo(l.at) })}</span>
        </span>
      ))}
    </span>
  );
}
