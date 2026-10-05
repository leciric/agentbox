// What the top bar says about the disk guard (DiskGuardPill): the daemon's
// floor of free space on every disk AgentBox writes to, and, in VM mode, the
// supervisor's last line under it, which pauses the whole VM when the host's
// disk is nearly full.
import type { VMPower } from '../../preload';
import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';
import { humanBytes } from './utils.ts';

export interface DiskGuardView {
  tone: 'low' | 'full';
  label: string; // the pill's few words
  title: string; // the popover's heading, and the pill's accessible name
  body: string; // what's happening and what to do
}

// diskGuardView is null while every disk has room and the VM, if any, wasn't
// paused for the host's disk.
export function diskGuardView(guard: T.DiskGuard | undefined, vm: VMPower | null | undefined): DiskGuardView | null {
  if (vm?.pausedForDisk) {
    const free = vm.hostFree ? t('shell.diskGuard.onlyFree', { size: humanBytes(vm.hostFree) }) : t('shell.diskGuard.almostNone');
    return {
      tone: 'full',
      label: t('shell.diskGuard.vmPausedLabel'),
      title: t('shell.diskGuard.vmPausedTitle'),
      body: t('shell.diskGuard.vmPausedBody', { free }),
    };
  }
  if (!guard || guard.level === 'ok' || guard.disks.length === 0) return null;
  const worst = [...guard.disks].filter((d) => d.level !== 'ok').sort((a, b) => a.free / Math.max(a.floor, 1) - b.free / Math.max(b.floor, 1))[0];
  if (!worst) return null;
  const where = t('shell.diskGuard.where', { label: worst.label, free: humanBytes(worst.free) });
  if (guard.level === 'full') {
    const paused = guard.paused.length > 0 ? ` ${t('shell.diskGuard.pausedAgents', { count: guard.paused.length })}` : '';
    return {
      tone: 'full',
      label: t('shell.diskGuard.fullLabel'),
      title: t('shell.diskGuard.fullTitle'),
      body: t('shell.diskGuard.fullBody', { where, floor: humanBytes(worst.floor), paused }),
    };
  }
  return {
    tone: 'low',
    label: t('shell.diskGuard.lowLabel'),
    title: t('shell.diskGuard.lowTitle'),
    body: t('shell.diskGuard.lowBody', { where, floor: humanBytes(worst.floor) }),
  };
}
