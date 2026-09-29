import { useQuery } from '@tanstack/react-query';
import { ArrowRight, Cpu, Laptop, MemoryStick, Monitor, X } from 'lucide-react';
import type { LinuxSetup } from '../../preload';
import { useState, type ReactNode } from 'react';
import { cn } from '../lib/utils';
import { rememberSection } from './SettingsPage';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Field, Input } from './ui/input';

// On Linux, AgentBox runs its agents either in a VM of its own (Cloud
// Hypervisor, `agentbox vm init`) or on the machine's own Incus (`agentbox
// host setup`). The VM is the recommended way: agents there get the VM's
// CPUs and memory, up to a cap, rather than the whole machine's, so heavy
// agent work doesn't take the desktop down with it. Host mode stays, as the
// second choice, with what it costs said plainly. Neither is sold as a
// security boundary: the home folder is shared with the VM, and host-mode
// agents are containers.
//
// Mac and Windows have no choice to make (Lima and WSL), so none of this shows
// there: the main process only reports `linux` on Linux.

export type RunMode = 'vm' | 'host';

// The words for each way, kept here so Setup's choice, Settings' suggestion
// and Home's say the same thing.
const vmSummary =
  "The daemon, Incus and every agent run in one Cloud Hypervisor VM. Agents get the VM's CPUs and memory, up to a cap, not all of your computer's, and the VM gives memory back when they stop. Your home folder is shared with it at the same path. No password, and nothing installed on your system.";
const hostSummary =
  "Agents run in containers on your computer's own Incus. They share its kernel, memory and disk directly, so heavy agent work can freeze your desktop. Setup asks for your password and changes your system: it installs Incus and adds a network bridge.";
const noKVM = "This machine has no /dev/kvm you can use. Turn on virtualization in your firmware settings, or add your user to the kvm group, then log in again.";

// ModeChoice is Setup's choice on a Linux machine not set up yet: the VM
// first, marked Recommended and picked unless the machine can't run it.
export function ModeChoice({ picked, kvm, disabled, onPick }: { picked: RunMode; kvm: boolean; disabled?: boolean; onPick: (mode: RunMode) => void }) {
  return (
    <div role="radiogroup" aria-label="Where AgentBox runs agents" className="grid gap-2" data-run-mode={picked}>
      <ModeOption mode="vm" picked={picked} disabled={disabled || !kvm} onPick={onPick} icon={Monitor} title="In a VM" badge={<Badge variant="brand">Recommended</Badge>}>
        {vmSummary} It needs /dev/kvm and about 4 GiB of memory to start.
        {!kvm && <span className="mt-1.5 block text-amber-200/90">{noKVM}</span>}
      </ModeOption>
      <ModeOption mode="host" picked={picked} disabled={disabled} onPick={onPick} icon={Laptop} title="Directly on this computer">
        {hostSummary}
      </ModeOption>
    </div>
  );
}

function ModeOption({
  mode,
  picked,
  disabled,
  onPick,
  icon: Icon,
  title,
  badge,
  children,
}: {
  mode: RunMode;
  picked: RunMode;
  disabled?: boolean;
  onPick: (mode: RunMode) => void;
  icon: typeof Monitor;
  title: string;
  badge?: ReactNode;
  children: ReactNode;
}) {
  const on = picked === mode;
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      disabled={disabled}
      data-mode={mode}
      onClick={() => onPick(mode)}
      className={cn(
        'flex items-start gap-3 rounded-xl border border-line bg-surface-faint px-3.5 py-3 text-left transition hover:border-line-vivid hover:bg-surface disabled:pointer-events-none disabled:opacity-60',
        on && 'border-brand-400/50 bg-brand-500/10 shadow-[0_0_0_3px_rgb(139_92_246/0.12)]',
      )}
    >
      <span className={cn('mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border border-line-strong', on && 'border-brand-400')}>
        {on && <span className="size-2 rounded-full bg-brand-400" />}
      </span>
      <span className="grid min-w-0 gap-1">
        <span className="flex items-center gap-2">
          <Icon className={cn('size-4 text-subtle', on && 'text-brand-300')} />
          <span className="text-[13px] font-medium text-primary">{title}</span>
          {badge}
        </span>
        <span className="text-[12.5px] leading-relaxed text-muted">{children}</span>
      </span>
    </button>
  );
}

const GiB = 1024 ** 3;

// VMSizeForm is what Setup's VM size fields hold, as typed.
export interface VMSizeForm {
  cpus: string;
  memory: string; // GiB
}

// vmSizeDefaults are `agentbox vm init`'s own: half the cores, from 2 to 8,
// and a memory cap of three quarters of the memory.
export function vmSizeDefaults(linux: LinuxSetup): VMSizeForm {
  return { cpus: String(linux.defaultCpus), memory: String(Math.round(linux.defaultMemoryCap / GiB)) };
}

// vmSize reads the form: the VM's CPUs and memory cap (like 12GiB), or null
// while either is out of range. The cap can't go below the 4 GiB the VM boots
// with, and may be all of the machine's: the VM only takes what agents use.
export function vmSize(form: VMSizeForm, linux: LinuxSetup): { cpus: number; memoryCap: string } | null {
  const cpus = Number(form.cpus);
  const memory = Number(form.memory);
  if (!Number.isInteger(cpus) || cpus < 1 || cpus > linux.cores) return null;
  if (form.memory.trim() === '' || !Number.isFinite(memory) || memory < 4 || memory * GiB > linux.memory) return null;
  return { cpus, memoryCap: `${memory}GiB` };
}

// VMSizeFields are the VM's CPUs and memory cap, under Setup's choice of the
// VM, filled in with vm init's defaults. Settings changes them afterwards.
export function VMSizeFields({ linux, form, disabled, onChange }: { linux: LinuxSetup; form: VMSizeForm; disabled?: boolean; onChange: (form: VMSizeForm) => void }) {
  const cpus = Number(form.cpus);
  const cpusOk = Number.isInteger(cpus) && cpus >= 1 && cpus <= linux.cores;
  const memory = Number(form.memory);
  const memoryOk = form.memory.trim() !== '' && Number.isFinite(memory) && memory >= 4 && memory * GiB <= linux.memory;
  const most = Math.floor(linux.memory / GiB);
  return (
    <div className="grid gap-3 rounded-xl border border-line bg-surface-faint px-3.5 py-3 sm:grid-cols-2" data-vm-size-fields>
      <Field
        label="CPUs"
        htmlFor="setup-vm-cpus"
        hint={<span className={cn(!cpusOk && 'text-rose-300')}>{`1 to ${linux.cores}, this computer's cores. ${linux.defaultCpus} by default.`}</span>}
      >
        <div className="relative">
          <Cpu className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
          <Input
            id="setup-vm-cpus"
            type="number"
            inputMode="numeric"
            min={1}
            max={linux.cores}
            step={1}
            className="h-8 pl-8 font-mono text-[12.5px]"
            disabled={disabled}
            value={form.cpus}
            onChange={(e) => onChange({ ...form, cpus: e.target.value })}
          />
        </div>
      </Field>
      <Field
        label="Memory cap, in GiB"
        htmlFor="setup-vm-memory"
        hint={
          <span className={cn(!memoryOk && 'text-rose-300')}>
            {`The most the VM may take, 4 to ${most}. It starts with 4 and takes more as agents need it. ${Math.round(linux.defaultMemoryCap / GiB)} by default.`}
          </span>
        }
      >
        <div className="relative">
          <MemoryStick className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
          <Input
            id="setup-vm-memory"
            type="number"
            inputMode="decimal"
            min={4}
            max={most}
            step={1}
            className="h-8 pl-8 font-mono text-[12.5px]"
            disabled={disabled}
            value={form.memory}
            onChange={(e) => onChange({ ...form, memory: e.target.value })}
          />
        </div>
      </Field>
      <p className="text-xs text-subtle sm:col-span-2">You can change both later in Settings, while the VM runs.</p>
    </div>
  );
}

// MoveToVM is Settings' way out of host mode, on a Linux machine already set
// up to run agents itself. The move is `agentbox vm migrate`
// (feat-migrate-host-to-vm), which takes this installation into AgentBox's VM;
// this is where the app opens it. command is the command, in a box to copy.
export function MoveToVM({ kvm, command }: { kvm: boolean; command: ReactNode }) {
  return (
    <Panel className="grid gap-3 p-4" data-move-to-vm>
      <div className="flex items-center gap-2">
        <Monitor className="size-4 text-brand-300" />
        <h3 className="text-[14px] font-semibold text-primary">Move to a VM</h3>
        <Badge variant="brand">Recommended</Badge>
      </div>
      <p className="text-[13px] leading-relaxed text-muted">
        AgentBox runs agents directly on this computer now. They share its kernel, memory and disk, so heavy agent work can freeze your desktop. In
        AgentBox's VM, they get the VM's CPUs and memory, up to a cap, instead. Your projects stay where they are: your home folder is shared with
        the VM at the same path.
      </p>
      {kvm ? (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">
            Run this in a terminal to move this installation into the VM. It needs no password.
          </p>
          {command}
        </div>
      ) : (
        <Notice tone="warning">{noKVM}</Notice>
      )}
    </Panel>
  );
}

// The suggestion on Home, until it's dismissed: once per machine, since the
// choice is the user's and Settings keeps the way to it.
const dismissedKey = 'agentbox.suggest-vm.dismissed';

// VMSuggestion is Home's nudge towards the VM on a Linux machine that runs its
// agents itself: only once setup is done (a machine still setting up gets
// Setup's own choice) and only when it could run the VM.
export function VMSuggestion({ ready, onOpen, className }: { ready: boolean | undefined; onOpen: () => void; className?: string }) {
  const [dismissed, setDismissed] = useState(() => localStorage.getItem(dismissedKey) === '1');
  const status = useQuery({
    queryKey: ['host-setup'],
    queryFn: () => window.agentbox.hostSetup.status(),
    refetchInterval: 30_000,
    enabled: !dismissed,
  });
  const linux = status.data?.linux;
  if (dismissed || !ready || linux?.mode !== 'host' || !linux.kvm) return null;
  return (
    <div
      className={cn('flex items-center gap-3 rounded-2xl border border-brand-400/25 bg-brand-500/[0.07] py-2.5 pl-4 pr-2', className)}
      data-vm-suggestion
    >
      <Monitor className="size-4 shrink-0 text-brand-300" />
      <span className="min-w-0 text-[13px] text-secondary">
        Agents run directly on this computer, so heavy work can freeze your desktop. Running them in AgentBox's VM keeps them within its limits.
      </span>
      <Button
        size="sm"
        variant="primary"
        className="ml-auto shrink-0"
        onClick={() => {
          rememberSection('setup');
          onOpen();
        }}
      >
        Move to a VM
        <ArrowRight />
      </Button>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label="Dismiss"
        title="Don't suggest this again"
        className="shrink-0"
        onClick={() => {
          localStorage.setItem(dismissedKey, '1');
          setDismissed(true);
        }}
      >
        <X />
      </Button>
    </div>
  );
}

// For the words a search in Settings finds the move by.
export const moveToVMKeywords = `vm virtual machine cloud hypervisor kvm host migrate move freeze ${vmSummary}`;
