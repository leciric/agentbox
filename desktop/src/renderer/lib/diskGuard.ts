// What the top bar says about the disk guard (DiskGuardPill): the daemon's
// floor of free space on every disk AgentBox writes to, and, in VM mode, the
// supervisor's last line under it, which pauses the whole VM when the host's
// disk is nearly full.
import type { VMPower } from '../../preload';
import type * as T from '../../shared/api';
import { humanBytes } from './utils.ts';

export interface DiskGuardView {
  tone: 'low' | 'full';
  label: string; // the pill's few words
  title: string; // the popover's heading, and the pill's accessible name
  body: string; // what's happening
  // What frees the disk or gives it room, from the daemon: commands in
  // `backticks`. Empty when there's nothing to add.
  advice: string;
}

// diskGuardView is null while every disk has room and the VM, if any, wasn't
// paused for the host's disk.
export function diskGuardView(guard: T.DiskGuard | undefined, vm: VMPower | null | undefined): DiskGuardView | null {
  if (vm?.pausedForDisk) {
    const free = vm.hostFree ? `only ${humanBytes(vm.hostFree)} free` : 'almost no space left';
    return {
      tone: 'full',
      label: 'Disk full: VM paused',
      title: "AgentBox's VM is paused: your disk is nearly full",
      body: `The disk that holds the VM's disk images has ${free}. The VM is paused so none of its writes fails half done, and resumes by itself once there's room. Free some space on your computer.`,
      advice: '',
    };
  }
  if (!guard || guard.level === 'ok' || guard.disks.length === 0) return null;
  const worst = [...guard.disks].filter((d) => d.level !== 'ok').sort((a, b) => a.free / Math.max(a.floor, 1) - b.free / Math.max(b.floor, 1))[0];
  if (!worst) return null;
  // The label names everything on the disk, which the popover's list shows:
  // the title and the body name only the disk.
  const disk = worst.label.split(', ')[0];
  const where = `${disk} has ${humanBytes(worst.free)} free`;
  const advice = worst.advice ?? '';
  if (guard.level === 'full') {
    const paused = guard.paused.length > 0 ? ` ${guard.paused.length === 1 ? 'The agent writing the most is' : `${guard.paused.length} agents writing the most are`} paused until there's room.` : '';
    return {
      tone: 'full',
      label: 'Disk full',
      title: `${disk} is at its floor: new agents are refused`,
      body: `${where}, under the ${humanBytes(worst.floor)} AgentBox keeps free so your disk never fills. New agents, forks, image builds and saved bases wait until there's room.${paused}${advice ? '' : " Destroy agents you're done with, or delete files."}`,
      advice,
    };
  }
  return {
    tone: 'low',
    label: 'Disk low',
    title: `${disk} is running low`,
    body: `${where}. At ${humanBytes(worst.floor)} AgentBox stops making new agents and pauses the ones writing the most.`,
    advice,
  };
}
