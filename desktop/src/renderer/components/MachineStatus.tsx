import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, Square, TriangleAlert, Unplug } from 'lucide-react';
import type { ReactNode } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import type { VMPower } from '../../preload';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useConnection } from '../lib/events';
import { formatNumber, useT } from '../lib/i18n';
import { machineStatus, memoryFull, type MachineStatus as Status } from '../lib/machineStatus';
import { setTopBarDetailed, useTopBarDetailed } from '../lib/topBarLayout';
import { cn, humanBytes } from '../lib/utils';
import { FreeButton, useFreeResources, VMActions, VMMemory, VMPanel, vmStateText, type FreeResources } from './ResourceControls';
import { Notice } from './ui/card';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';
import { Tip } from './ui/tooltip';

// MachineStatus is the top bar's machine: AgentBox's VM, or this computer in
// host mode, with the daemon in it. By default it's one bubble saying in
// words how things are, the worst first (lib/machineStatus.ts), whose
// popover holds the numbers and the controls; the action that gives memory
// back shows in the bar only when memory is tight, and Start once it's all
// off. Settings' "Detailed top bar" makes it a strip instead, its memory,
// CPU and disk always in view, each opening its own breakdown.
export function MachineStatus({ agents, onSelect }: { agents: T.Agent[]; onSelect: (view: View) => void }) {
  const detailed = useTopBarDetailed();
  const free = useFreeResources(agents);
  const connection = useConnection();
  const host = useQuery({ queryKey: ['usage'], queryFn: api.usage }).data?.host;
  const status = machineStatus(free.vm, connection, host);
  const vmDisk = free.vm?.disk;
  return (
    <>
      {detailed ? (
        <Strip free={free} status={status} host={host} vmDisk={vmDisk} onSelect={onSelect} />
      ) : (
        <>
          <Bubble free={free} status={status} host={host} vmDisk={vmDisk} agents={agents} />
          {(freeing(free) || free.mode === 'start' || (free.mode === 'free' && status.kind === 'memory')) && <FreeButton free={free} style="freeMemory" />}
        </>
      )}
      {free.dialog}
    </>
  );
}

// freeing is whether a run is stopping things: the bar shows its progress.
// The VM merely starting or stopping is the status's to say, not the
// button's as well.
function freeing(free: FreeResources): boolean {
  return free.run?.phase === 'stopping' || free.run?.phase === 'vm';
}

const toneDot: Record<Status['tone'], string> = {
  ok: 'bg-emerald-400 animate-glow',
  warn: 'bg-amber-400',
  bad: 'bg-rose-400',
  busy: 'bg-brand-400 animate-pulse',
  off: 'bg-faint',
};

const toneRing: Record<Status['tone'], string> = {
  ok: 'border-line bg-surface-faint',
  warn: 'border-amber-400/40 bg-amber-400/10',
  bad: 'border-rose-400/40 bg-rose-400/10',
  busy: 'border-line bg-surface-faint',
  off: 'border-line bg-surface-faint',
};

// sentence is the bubble's words for a status.
function useSentence(free: FreeResources, status: Status): string {
  const t = useT();
  const wsl = free.vm?.driver === 'wsl';
  const vm = wsl ? 'wsl' : free.vm ? 'yes' : 'no';
  const what = wsl ? 'WSL' : 'VM';
  switch (status.kind) {
    case 'running':
      return t('shell.machine.running', { vm, count: free.now.filter((a) => a.state === 'running').length });
    case 'memory':
      return t('shell.machine.memory', { vm });
    case 'connecting':
      return t('shell.machine.connecting');
    case 'offline':
      return t('shell.machine.offline');
    case 'paused':
      return t('shell.machine.paused');
    case 'off':
      return t('shell.machine.off', { what });
    case 'busy':
      return t('shell.machine.busy', { state: free.vm?.state ?? 'starting', what });
  }
}

function StatusIcon({ status }: { status: Status }) {
  if (status.tone === 'bad' && status.kind === 'offline') return <Unplug className="size-3.5 shrink-0 text-rose-300" />;
  if (status.tone === 'busy') return <LoaderCircle className="size-3.5 shrink-0 animate-spin text-brand-300" />;
  if (status.tone === 'warn') return <TriangleAlert className="size-3.5 shrink-0 text-amber-300" />;
  return <span className={cn('size-1.5 shrink-0 rounded-full', toneDot[status.tone])} />;
}

function Bubble({
  free,
  status,
  host,
  vmDisk,
  agents,
}: {
  free: FreeResources;
  status: Status;
  host?: T.HostUsage;
  vmDisk?: T.VMDisk;
  agents: T.Agent[];
}) {
  const t = useT();
  const connection = useConnection();
  const sentence = useSentence(free, status);
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn('flex min-w-0 items-center gap-2 rounded-full border py-1 pl-2.5 pr-3 transition hover:bg-surface-raised', toneRing[status.tone])}
          aria-label={sentence}
          data-machine={status.kind}
          data-connection={connection.state}
          data-vm-state={free.vm?.state}
          data-vm-driver={free.vm?.driver}
        >
          <StatusIcon status={status} />
          <span
            className={cn(
              'hidden truncate whitespace-nowrap text-[12px] sm:inline',
              status.tone === 'bad' ? 'text-rose-200' : status.tone === 'warn' ? 'text-amber-200' : 'text-secondary',
            )}
          >
            {sentence}
          </span>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <div className="grid gap-3" data-machine-popover>
          <span className="text-[13px] font-medium text-primary">{status.kind === 'running' ? t('shell.machine.allGood') : sentence}</span>
          <div className="grid gap-1.5 text-[12px]">
            {free.vm && (
              <Check tone={status.kind === 'off' ? 'off' : free.vm.state === 'running' ? 'ok' : free.vm.state === 'paused' ? 'warn' : 'busy'} label={free.vm.driver === 'wsl' ? t('shell.machine.wsl') : t('vm.agentboxVM')} value={t(vmStateText[free.vm.state])} />
            )}
            {status.kind !== 'off' && status.kind !== 'paused' && status.kind !== 'busy' && (
              <Check
                tone={connection.state === 'connected' ? 'ok' : connection.state === 'connecting' ? 'busy' : 'bad'}
                label={t('shell.machine.connection')}
                value={t('shell.machine.connectionState', { state: connection.state })}
              />
            )}
            {status.memory && (
              <Check
                tone={status.memory.capPercent > memoryFull ? 'warn' : 'ok'}
                label={t('shell.machine.memoryLabel')}
                value={t('shell.top.usedOf', { used: humanBytes(status.memory.used), size: humanBytes(status.memory.total) })}
              />
            )}
            {host && connection.state === 'connected' && status.memory && (
              <Check
                tone={host.cpu > memoryFull ? 'warn' : 'ok'}
                label={t('shell.machine.cpuLabel')}
                value={t('shell.top.cpuOf', { percent: formatNumber(host.cpu, { maximumFractionDigits: 0 }), count: host.cores })}
              />
            )}
            {status.memory && <DiskCheck host={host} vmDisk={vmDisk} />}
            {status.memory && (
              <Check
                tone="ok"
                label={t('shell.machine.agentsLabel')}
                value={t('shell.machine.agentsValue', {
                  running: agents.filter((a) => a.state === 'running').length,
                  working: agents.filter((a) => a.state === 'running' && a.chat === 'running').length,
                })}
              />
            )}
          </div>
          {connection.state === 'disconnected' && connection.error && status.kind === 'offline' && (
            <span className="text-[11.5px] leading-relaxed text-rose-300">{connection.error}</span>
          )}
          {free.vm?.error && <Notice className="text-[12px]">{free.vm.error}</Notice>}
          {status.kind === 'memory' && <span className="text-[11.5px] leading-relaxed text-muted">{t('shell.machine.memoryHint')}</span>}
          <VMActions free={free} style="freeMemory" />
          <button
            type="button"
            className="justify-self-start text-[11.5px] text-brand-400 transition hover:underline"
            onClick={() => setTopBarDetailed(true)}
            data-topbar-detailed="on"
          >
            {t('shell.machine.showDetails')}
          </button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function DiskCheck({ host, vmDisk }: { host?: T.HostUsage; vmDisk?: T.VMDisk }) {
  const t = useT();
  const [used, size] = diskOf(host, vmDisk);
  if (size <= 0) return null;
  return (
    <Check
      tone={used / size > memoryFull / 100 ? 'warn' : 'ok'}
      label={t('shell.machine.diskLabel')}
      value={t('shell.top.usedOf', { used: knownBytes(used), size: humanBytes(size) })}
    />
  );
}

function Check({ tone, label, value }: { tone: Status['tone']; label: string; value: string }) {
  return (
    <span className="flex min-w-0 items-center gap-2">
      <span className={cn('size-1.5 shrink-0 rounded-full', tone === 'ok' ? 'bg-emerald-400' : toneDot[tone])} />
      <span className="shrink-0 text-secondary">{label}</span>
      <span className={cn('ml-auto truncate font-mono text-[11px] tabular-nums', tone === 'bad' ? 'text-rose-300' : tone === 'warn' ? 'text-amber-300' : 'text-tertiary')}>
        {value}
      </span>
    </span>
  );
}

// tint is a figure's colour in the strip: amber past 65%, rose past 85%.
function tint(percent: number): string {
  return percent > 85 ? 'text-rose-300' : percent > 65 ? 'text-amber-300' : 'text-tertiary';
}

// gib is a used/total pair as the strip shows it: "5.1/16 GiB".
function gib(used: number, total: number): string {
  const g = 1024 ** 3;
  return `${formatNumber(used / g, { minimumFractionDigits: 1, maximumFractionDigits: 1 })}/${formatNumber(total / g, { maximumFractionDigits: 0 })} GiB`;
}

function Strip({
  free,
  status,
  host,
  vmDisk,
  onSelect,
}: {
  free: FreeResources;
  status: Status;
  host?: T.HostUsage;
  vmDisk?: T.VMDisk;
  onSelect: (view: View) => void;
}) {
  const t = useT();
  const connection = useConnection();
  const sentence = useSentence(free, status);
  const up = status.memory && connection.state === 'connected';
  const [diskUsed, diskSize] = diskOf(host, vmDisk);
  const diskPercent = diskSize > 0 && diskUsed > 0 ? (diskUsed / diskSize) * 100 : 0;
  return (
    <>
      <span
        className={cn('flex min-w-0 items-center divide-x divide-line overflow-hidden rounded-full border bg-surface-faint', toneRing[status.tone].split(' ')[0])}
        data-machine={status.kind}
        data-connection={connection.state}
        data-vm-state={free.vm?.state}
        data-vm-driver={free.vm?.driver}
      >
        <Segment
          label={sentence}
          trigger={
            <>
              <StatusIcon status={status} />
              <span className="text-[12px] font-medium text-secondary">{free.vm ? t(free.vm.driver === 'wsl' ? 'shell.machine.wsl' : 'shell.machine.vm') : t('shell.machine.agentbox')}</span>
              {!up && <span className={cn('hidden whitespace-nowrap text-[11.5px] sm:inline', status.tone === 'bad' ? 'text-rose-200' : 'text-muted')}>{sentence}</span>}
            </>
          }
        >
          <div className="grid gap-3">
            <ConnectionLine status={status} />
            {free.vm ? <VMPanel vm={free.vm} free={free} /> : <VMActions free={free} style="shutDown" />}
            <button type="button" className="justify-self-start text-[11.5px] text-brand-400 transition hover:underline" onClick={() => setTopBarDetailed(false)} data-topbar-detailed="off">
              {t('shell.machine.hideDetails')}
            </button>
          </div>
        </Segment>
        {up && status.memory && (
          <Segment
            label={t('shell.machine.memoryLabel')}
            className={cn('hidden sm:flex', status.memory.capPercent > memoryFull && 'bg-amber-400/10')}
            data-meter="memory"
            trigger={
              <>
                <span className="text-[11px] text-muted">{t('shell.machine.memoryLabel')}</span>
                <span className={cn('whitespace-nowrap font-mono text-[11px] tabular-nums', tint(status.memory.capPercent))}>{gib(status.memory.used, status.memory.total)}</span>
              </>
            }
          >
            <MemoryPanel vm={free.vm} status={status} onSelect={onSelect} />
          </Segment>
        )}
        {up && host && (
          <Segment
            label={t('shell.machine.cpuLabel')}
            className="hidden md:flex"
            data-meter="cpu"
            trigger={
              <>
                <span className="text-[11px] text-muted">{t('shell.machine.cpuLabel')}</span>
                <span className={cn('w-8 font-mono text-[11px] tabular-nums', tint(host.cpu))}>{formatNumber(host.cpu, { maximumFractionDigits: 0 })}%</span>
              </>
            }
          >
            <CPUPanel host={host} onSelect={onSelect} />
          </Segment>
        )}
        {up && diskSize > 0 && (
          <Segment
            label={t('shell.machine.diskLabel')}
            className="hidden lg:flex"
            data-meter="disk"
            width="w-96"
            trigger={
              <>
                <span className="text-[11px] text-muted">{t('shell.machine.diskLabel')}</span>
                <span className={cn('whitespace-nowrap font-mono text-[11px] tabular-nums', tint(diskPercent))}>{diskUsed > 0 ? gib(diskUsed, diskSize) : `—/${humanBytes(diskSize)}`}</span>
              </>
            }
          >
            <DiskPanel host={host} vmDisk={vmDisk} />
          </Segment>
        )}
      </span>
      {(free.mode !== 'busy' || freeing(free)) && <FreeButton free={free} style="shutDown" />}
    </>
  );
}

function Segment({
  label,
  trigger,
  children,
  className,
  width = 'w-80',
  ...rest
}: {
  label: string;
  trigger: ReactNode;
  children: ReactNode;
  className?: string;
  width?: string;
  'data-meter'?: string;
}) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button type="button" className={cn('flex items-center gap-1.5 px-2.5 py-1 transition hover:bg-surface-raised', className)} aria-label={label} {...rest}>
          {trigger}
        </button>
      </PopoverTrigger>
      <PopoverContent className={width}>{children}</PopoverContent>
    </Popover>
  );
}

// ConnectionLine is whether the app reaches the daemon, while the machine is
// up: the strip's VM popover leads with it when it doesn't.
function ConnectionLine({ status }: { status: Status }) {
  const t = useT();
  const connection = useConnection();
  if (status.kind === 'off' || status.kind === 'paused' || status.kind === 'busy') return null;
  const ok = connection.state === 'connected';
  return (
    <span
      className={cn(
        'flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-[11.5px]',
        ok ? 'bg-surface-faint text-muted' : connection.state === 'disconnected' ? 'bg-rose-400/10 text-rose-200' : 'bg-brand-400/10 text-brand-200',
      )}
      data-connection-line={connection.state}
    >
      {ok ? (
        <span className="size-1.5 shrink-0 rounded-full bg-emerald-400" />
      ) : connection.state === 'disconnected' ? (
        <Unplug className="size-3.5 shrink-0" />
      ) : (
        <LoaderCircle className="size-3.5 shrink-0 animate-spin" />
      )}
      <span className="min-w-0">{connection.state === 'disconnected' && connection.error ? connection.error : t('shell.machine.connectionLine', { state: connection.state })}</span>
    </span>
  );
}

// MemoryPanel is the memory segment's popover: the VM's memory bar, then
// which agents hold the most, each with Stop.
function MemoryPanel({ vm, status, onSelect }: { vm: VMPower | null; status: Status; onSelect: (view: View) => void }) {
  const t = useT();
  const query = useQuery({ queryKey: ['memoryUsage'], queryFn: api.memoryUsage, refetchOnMount: 'always' });
  const memory = status.memory!;
  return (
    <div className="grid gap-2.5">
      <div className="flex items-center justify-between">
        <span className="text-[13px] font-medium text-primary">{vm ? t(vm.driver === 'wsl' ? 'shell.machine.wslMemory' : 'shell.machine.vmMemory') : t('shell.machine.memoryLabel')}</span>
        <span className={cn('font-mono text-[11px] tabular-nums', tint(memory.capPercent))}>
          {t('shell.top.usedOf', { used: humanBytes(memory.used), size: humanBytes(memory.total) })}
        </span>
      </div>
      {vm && <VMMemory vm={vm} />}
      {vm && memory.cap > memory.total && (
        <span className="text-[11.5px] leading-relaxed text-muted">{t('shell.machine.memoryGranted', { granted: humanBytes(memory.total), cap: humanBytes(memory.cap) })}</span>
      )}
      {memory.capPercent > memoryFull && <span className="text-[11.5px] leading-relaxed text-muted">{t('shell.machine.memoryHint')}</span>}
      {query.isPending ? (
        <span className="py-1 text-[12px] text-muted">{t('shell.machine.measuringMemory')}</span>
      ) : query.isError ? (
        <span className="py-1 text-[12px] text-rose-300">{query.error instanceof Error ? query.error.message : String(query.error)}</span>
      ) : (
        <div className="grid min-w-0 max-h-72 gap-2 overflow-y-auto border-t border-line pt-2.5 pr-1">
          {[...query.data.agents]
            .sort((a, b) => b.memory - a.memory)
            .map((a) => (
              <AgentUsageRow key={a.ref} agentRef={a.ref} title={a.title} state={a.state} onSelect={onSelect} value={humanBytes(a.memory)} />
            ))}
          <div className="flex items-center justify-between gap-3 border-t border-line pt-1.5 text-[11.5px] text-muted">
            <span>{t('shell.machine.everythingElse')}</span>
            <span className="font-mono tabular-nums text-faint">{humanBytes(query.data.otherUsed)}</span>
          </div>
        </div>
      )}
    </div>
  );
}

// DiskPanel is the disk segment's popover, as used/size. In VM mode it's what
// AgentBox's VM costs the host's disk against the most it can hold: the bytes
// its two disk images (the agents' pool and the VM's system disk) really
// take, against the sizes the VM's disk settings gave them, as the host's
// side measures them (VMPower.disk). Otherwise it's the Incus storage pool
// every agent's machine and saved base share. The breakdown is only measured
// while it's open — it walks every worktree and media directory and queries
// Incus once per machine and base. Used is never shown as 0: an image can't
// take nothing, so 0 is a front end that couldn't measure it, shown as "—".
function DiskPanel({ host, vmDisk }: { host?: T.HostUsage; vmDisk?: T.VMDisk }) {
  const t = useT();
  const diskUsage = useQuery({ queryKey: ['diskUsage'], queryFn: api.diskUsage, refetchOnMount: 'always' });
  const homeDisk = useQuery({ queryKey: ['homeDisk'], queryFn: () => window.agentbox.vm.disk(), enabled: !!vmDisk, refetchOnMount: 'always' });
  const [used, size] = diskOf(host, vmDisk);
  if (vmDisk) return <VMDiskBreakdown disk={vmDisk} host={host} home={homeDisk} pool={diskUsage} />;
  return (
    <div className="grid gap-2.5">
      <div className="flex items-center justify-between">
        <span className="text-[13px] font-medium text-primary">{t('shell.top.agentsDisk')}</span>
        <span className="font-mono text-[11px] tabular-nums text-tertiary">{t('shell.top.usedOf', { used: knownBytes(used), size: humanBytes(size) })}</span>
      </div>
      <DiskUsageCategories query={diskUsage} />
    </div>
  );
}

function diskOf(host?: T.HostUsage, vmDisk?: T.VMDisk): [number, number] {
  return vmDisk ? [vmDisk.allocated, vmDisk.size] : [host?.poolUsed ?? 0, host?.poolTotal ?? 0];
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

// CPUPanel is the CPU segment's popover: the total broken down by agent, each
// agent's own CPU% sampled only while it's open.
function CPUPanel({ host, onSelect }: { host: T.HostUsage; onSelect: (view: View) => void }) {
  const t = useT();
  const query = useQuery({ queryKey: ['cpuUsage'], queryFn: api.cpuUsage, refetchOnMount: 'always' });
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

// AgentUsageRow is one agent in the CPU or memory popover: a link to the agent, its
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
      await queryClient.invalidateQueries({ queryKey: ['memoryUsage'] });
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

