import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, Cpu, LoaderCircle, MemoryStick, Monitor } from 'lucide-react';
import type { LinuxSetup } from '../../preload';
import { useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { t, useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { laterKey, movePrompt } from '../lib/vmMove';
import { rememberSection } from './SettingsPage';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';

// On Linux, AgentBox runs in a VM of its own, made with Cloud Hypervisor by
// `agentbox vm init`: the daemon, Incus and every agent are in there, and
// agents get the VM's CPUs and memory, up to a cap, rather than the whole
// machine's, so heavy agent work doesn't take the desktop down with it. Setup
// offers nothing else: running agents on the machine's own Incus (host mode)
// was far worse. A machine set up that way before keeps working until it
// moves into the VM (`agentbox vm migrate`): MovePrompt says so once after
// each update, and Settings' MoveToVM is where the move is. The VM isn't sold
// as a security boundary: the home folder is shared with it.
//
// Mac and Windows have nothing of this (Lima and WSL): the main process only
// reports `linux` on Linux.

// The words for the VM, kept here so Setup, the prompt and Settings say the
// same thing.
const vmSummary = () => t('vm.run.summary');
const noKVM = () => t('vm.run.noKVM');

// LinuxVMSetup is a Linux machine's first screen, before AgentBox's VM is
// made: there's no daemon until then, so the setup is the page, as a Mac's
// VMSetup is. It makes the VM with `agentbox vm init`, at the size chosen
// here, and streams what it prints; the daemon it starts is the app's to
// connect to when it finishes.
export function LinuxVMSetup({ linux }: { linux: LinuxSetup }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  const [form, setForm] = useState<VMSizeForm>(() => vmSizeDefaults(linux));
  useEffect(() => window.agentbox.hostSetup.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);
  const sized = vmSize(form, linux);
  const run = useMutation({
    mutationFn: () => window.agentbox.hostSetup.run({ vm: true, ...sized }),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast(t('vm.setup.toast'), { description: t('vm.setup.toastDescription') });
      await queryClient.invalidateQueries();
    },
  });
  return (
    <Panel className="grid gap-3 p-4" data-linux-vm-setup={linux.kvm ? 'create' : 'nokvm'}>
      <div className="flex items-start gap-3">
        <Monitor className="mt-0.5 size-5 shrink-0 text-brand-400" />
        <div className="grid min-w-0 gap-1">
          <h2 className="text-[15px] font-medium text-primary">{t('vm.run.setupTitle')}</h2>
          <p className="text-[13px] text-muted">
            {t('vm.run.setupIntro', { summary: vmSummary() })}
          </p>
        </div>
      </div>
      {linux.kvm ? (
        <>
          <VMSizeFields linux={linux} form={form} disabled={run.isPending} onChange={setForm} />
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" disabled={run.isPending || !sized} onClick={() => run.mutate()} data-linux-vm-setup-run>
              {run.isPending ? <LoaderCircle className="animate-spin" /> : <Monitor />}
              {t('vm.run.setUp')}
            </Button>
            <span className="text-xs text-subtle">{run.isPending ? t('vm.run.making') : t('vm.run.noPassword')}</span>
          </div>
        </>
      ) : (
        <Notice tone="warning">{noKVM()}</Notice>
      )}
      {(lines.length > 0 || run.isPending) && <SetupLog lines={lines} label={t('vm.run.log')} />}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      <div className="grid gap-2">
        <p className="text-[13px] text-muted">{t('vm.run.orTerminal')}</p>
        <CommandBox command={sized ? `agentbox vm init --cpus ${sized.cpus} --memory-cap ${sized.memoryCap}` : 'agentbox vm init'} />
      </div>
    </Panel>
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
  const t = useT();
  const cpus = Number(form.cpus);
  const cpusOk = Number.isInteger(cpus) && cpus >= 1 && cpus <= linux.cores;
  const memory = Number(form.memory);
  const memoryOk = form.memory.trim() !== '' && Number.isFinite(memory) && memory >= 4 && memory * GiB <= linux.memory;
  const most = Math.floor(linux.memory / GiB);
  return (
    <div className="grid gap-3 rounded-xl border border-line bg-surface-faint px-3.5 py-3 sm:grid-cols-2" data-vm-size-fields>
      <Field
        label={t('vm.run.cpusLabel')}
        htmlFor="setup-vm-cpus"
        hint={<span className={cn(!cpusOk && 'text-rose-300')}>{t('vm.run.cpusHint', { max: linux.cores, default: linux.defaultCpus })}</span>}
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
        label={t('vm.run.memoryLabel')}
        htmlFor="setup-vm-memory"
        hint={
          <span className={cn(!memoryOk && 'text-rose-300')}>
            {t('vm.run.memoryHint', { most, default: Math.round(linux.defaultMemoryCap / GiB) })}
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
      <p className="text-xs text-subtle sm:col-span-2">{t('vm.run.changeLater')}</p>
    </div>
  );
}

// MoveToVM is Settings' way out of host mode, on a Linux machine set up to
// run agents itself before AgentBox ran in a VM on Linux. The move is
// `agentbox vm migrate`, which takes this installation into AgentBox's VM;
// this is where the app runs it. action is the move itself, in the app
// (VMMigrate); without it, command is the command, in a box to copy.
export function MoveToVM({ kvm, command, action }: { kvm: boolean; command: ReactNode; action?: ReactNode }) {
  const t = useT();
  return (
    <Panel className="grid gap-3 p-4" data-move-to-vm>
      <div className="flex items-center gap-2">
        <Monitor className="size-4 text-brand-300" />
        <h3 className="text-[14px] font-semibold text-primary">{t('vm.run.moveTitle')}</h3>
      </div>
      <p className="text-[13px] leading-relaxed text-muted">
        {t('vm.run.moveDescription')}
      </p>
      {action ? (
        action
      ) : kvm ? (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">
            {t('vm.run.moveCommand')}
          </p>
          {command}
        </div>
      ) : (
        <Notice tone="warning">{noKVM()}</Notice>
      )}
    </Panel>
  );
}

// MovePrompt tells a host-mode installation, once after each update, that
// AgentBox runs in a VM on Linux now, and takes it to the move: Settings →
// Setup → Move to a VM, which runs `agentbox vm migrate`. Later puts it off
// until the next update; Settings keeps the way to the move meanwhile.
export function MovePrompt({ version, onOpen }: { version: string | undefined; onOpen: () => void }) {
  const t = useT();
  const [later, setLater] = useState(() => localStorage.getItem(laterKey));
  const status = useQuery({
    queryKey: ['host-setup'],
    queryFn: () => window.agentbox.hostSetup.status(),
    refetchInterval: 30_000,
  });
  const host = status.data?.linux?.mode === 'host';
  const migration = useQuery({
    queryKey: ['vm-migration'],
    queryFn: () => window.agentbox.vmMigrate.status(),
    refetchInterval: 30_000,
    enabled: host,
  });
  const open = movePrompt(status.data, migration.data, version, later);
  const putOff = () => {
    if (!version) return;
    localStorage.setItem(laterKey, version);
    setLater(version);
  };
  const kvm = status.data?.linux?.kvm ?? false;
  const projects = migration.data?.projects?.length ?? 0;
  const agents = migration.data?.agents?.length ?? 0;
  const started = migration.data?.state === 'started';
  return (
    <Dialog open={open} onOpenChange={(next) => !next && putOff()}>
      <DialogContent data-move-prompt={started ? 'started' : 'available'}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Monitor className="size-4 text-brand-300" />
            {t('vm.run.promptTitle')}
          </DialogTitle>
          <DialogDescription>
            {started
              ? t('vm.run.promptStarted')
              : t('vm.run.promptAvailable')}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 text-[13px] leading-relaxed text-muted">
          <p>
            {t('vm.run.promptStill')}
          </p>
          <p>
            {t('vm.run.promptTakes', { projects, agents })}
          </p>
          {!kvm && <Notice tone="warning">{noKVM()}</Notice>}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={putOff}>
            {t('vm.run.later')}
          </Button>
          <Button
            variant="primary"
            data-move-prompt-open
            onClick={() => {
              putOff();
              rememberSection('setup');
              onOpen();
            }}
          >
            {started ? t('vm.run.carryOn') : t('vm.run.moveToVM')}
            <ArrowRight />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// moveToVMKeywords is the words a search in Settings finds the move by, in the
// language of the moment.
export const moveToVMKeywords = (): string => t('vm.run.keywords', { summary: vmSummary() });
