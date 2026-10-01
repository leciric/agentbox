import { useQuery } from '@tanstack/react-query';
import { HardDrive, TriangleAlert } from 'lucide-react';
import type { View } from '../App';
import { api } from '../lib/api';
import { diskGuardView } from '../lib/diskGuard';
import { useVMPower } from '../lib/vm';
import type * as T from '../../shared/api';
import { cn, humanBytes } from '../lib/utils';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// DiskGuardPill is the disk guard in the top bar: nothing while every disk
// AgentBox writes to has room, an amber pill nearing the floor of free space
// it keeps, a rose one at it, and a rose one too when the VM's supervisor
// paused the whole VM because the host's disk was nearly full. It has a fixed
// label rather than a changing number, so it doesn't shift the bar around.
export function DiskGuardPill({ onSelect }: { onSelect: (view: View) => void }) {
  const vm = useVMPower().data;
  // The guard pushes every change as an event (lib/events.ts); the poll is
  // only for the free space shown in the popover.
  const disk = useQuery({ queryKey: ['disk'], queryFn: api.diskGuard, refetchInterval: 30_000, retry: false });
  const view = diskGuardView(disk.data, vm);
  if (!view) return null;
  const full = view.tone === 'full';
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset transition',
            full
              ? 'bg-rose-400/10 text-rose-200 ring-rose-400/30 hover:bg-rose-400/15'
              : 'bg-amber-400/10 text-amber-200 ring-amber-400/25 hover:bg-amber-400/15',
          )}
          aria-label={view.title}
          data-disk-level={view.tone}
        >
          <TriangleAlert className="size-3.5" />
          <span className="hidden sm:inline">{view.label}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-96">
        <div className="grid gap-3">
          <div className="grid gap-1">
            <span className={cn('text-[13px] font-medium', full ? 'text-rose-200' : 'text-amber-200')}>{view.title}</span>
            <span className="text-[12px] leading-relaxed text-secondary">{view.body}</span>
          </div>
          {!vm?.pausedForDisk && disk.data && disk.data.disks.length > 0 && <DiskList disks={disk.data.disks} />}
          {!vm?.pausedForDisk && disk.data && disk.data.paused.length > 0 && (
            <div className="grid gap-1">
              <span className="text-[12px] font-medium text-secondary">Paused for it</span>
              <span className="font-mono text-[11.5px] text-muted">{disk.data.paused.join(', ')}</span>
            </div>
          )}
          <div className="flex items-center justify-between gap-3 border-t border-line pt-2.5">
            <span className="text-[11.5px] text-faint">Nothing is stopped or deleted.</span>
            <button
              type="button"
              className="rounded-md px-2 py-1 text-[12px] font-medium text-brand-300 transition hover:bg-surface-raised"
              onClick={() => onSelect({ kind: 'settings' })}
            >
              Disk floor settings
            </button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function DiskList({ disks }: { disks: T.DiskGuardDisk[] }) {
  return (
    <div className="grid gap-2">
      {disks.map((d) => {
        const used = d.total > 0 ? Math.min(1, Math.max(0, 1 - d.free / d.total)) * 100 : 0;
        const floorAt = d.total > 0 ? Math.min(100, (1 - d.floor / d.total) * 100) : 100;
        return (
          <div key={d.label + (d.path ?? '')} className="grid gap-1">
            <div className="flex items-center justify-between gap-3 text-[12px]">
              <span className="flex min-w-0 items-center gap-1.5 text-secondary">
                <HardDrive className="size-3.5 shrink-0 text-subtle" />
                <span className="truncate" title={d.path}>
                  {d.label}
                </span>
              </span>
              <span
                className={cn(
                  'shrink-0 font-mono text-[11px] tabular-nums',
                  d.level === 'full' ? 'text-rose-300' : d.level === 'low' ? 'text-amber-300' : 'text-tertiary',
                )}
              >
                {humanBytes(d.free)} free
              </span>
            </div>
            <span className="relative h-1.5 overflow-hidden rounded-full bg-surface-strong">
              <span
                className={cn('block h-full rounded-full', d.level === 'full' ? 'bg-rose-400' : d.level === 'low' ? 'bg-amber-400' : 'bg-sky-400')}
                style={{ width: `${Math.max(used, 2)}%` }}
              />
              {/* Where the floor starts: AgentBox keeps everything right of it free. */}
              <span className="absolute inset-y-0 w-px bg-rose-300/80" style={{ left: `${floorAt}%` }} />
            </span>
            <span className="text-[11px] text-faint">
              keeps {humanBytes(d.floor)} free of {humanBytes(d.total)}
            </span>
          </div>
        );
      })}
    </div>
  );
}
