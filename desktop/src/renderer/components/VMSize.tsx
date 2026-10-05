import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Cpu, HardDrive, LoaderCircle, MemoryStick, RotateCcw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMStatus } from '../../preload';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatNumber, useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Field, Input } from './ui/input';

const GiB = 1024 ** 3;

// gib is a size in GiB the way a person says it: 8, 6.5.
function gib(bytes: number): string {
  return String(Math.round((bytes / GiB) * 10) / 10);
}

// gibText is the same size for the screen, with the language's decimal mark.
function gibText(bytes: number): string {
  return formatNumber(Math.round((bytes / GiB) * 10) / 10);
}

// VMSize is the CPUs and memory of AgentBox's Linux VM on a Mac, which the
// daemon, Incus and every agent share, and a way to change them: `agentbox vm
// resize`, run by the main process. Lima only changes a stopped VM, so that
// restarts the VM and stops every agent — which the dialog says before it
// happens. The disk stays the size it was made with.
export function VMSize({ vm, busy: resizing }: { vm: VMStatus; busy: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const running = (agents.data ?? []).filter((a) => a.state === 'running').length;
  const [form, setForm] = useState({ cpus: String(vm.cpus ?? ''), memory: vm.memory ? gib(vm.memory) : '' });
  const [confirming, setConfirming] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.vm.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  // The form follows the VM until you change something, so a resize made in a
  // terminal shows up here too.
  const [edited, setEdited] = useState(false);
  useEffect(() => {
    if (!edited) setForm({ cpus: String(vm.cpus ?? ''), memory: vm.memory ? gib(vm.memory) : '' });
  }, [vm.cpus, vm.memory, edited]);

  const resize = useMutation({
    mutationFn: ({ cpus, memory }: { cpus: number; memory: number }) => window.agentbox.vm.resize(cpus, `${memory}GiB`),
    onMutate: () => setLines([]),
    onSuccess: async (_, { cpus, memory }) => {
      setEdited(false);
      toast(t('vm.size.toastMac', { cpus, memory: formatNumber(memory) }), { description: t('vm.size.toastRestart') });
      // The rest refetches when the app reconnects to the restarted daemon.
      await queryClient.invalidateQueries({ queryKey: ['host-setup'] });
    },
  });

  const limits = vm.limits;
  const cpus = Number(form.cpus);
  const memory = Number(form.memory);
  const cpusOk = Number.isInteger(cpus) && limits !== undefined && cpus >= limits.minCpus && cpus <= limits.maxCpus;
  const memoryOk = form.memory.trim() !== '' && Number.isFinite(memory) && limits !== undefined && memory * GiB >= limits.minMemory && memory * GiB <= limits.maxMemory;
  const changed = cpus !== vm.cpus || Math.abs(memory * GiB - (vm.memory ?? 0)) >= GiB / 20;
  const busy = resize.isPending || resizing;

  return (
    <Panel className="mt-3 grid gap-3 p-4" data-vm-size>
      <div>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <div className="text-[13px] font-medium text-primary">{t('vm.agentboxVM')}</div>
          {vm.driver === 'vz' && <Badge variant="warning">{t('vm.size.experimental')}</Badge>}
          <div className="text-[12px] text-subtle" data-vm-size-current>
            {t('vm.size.currentMac', {
              status: vm.status ?? t('common.unknown'),
              cpus: vm.cpus ?? '—',
              memory: vm.memory ? `${gibText(vm.memory)} GiB` : '—',
              hasDisk: vm.disk ? 'yes' : 'no',
              disk: vm.disk ? gibText(vm.disk) : '',
            })}
          </div>
        </div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          {t('vm.size.descriptionMac')}
        </p>
      </div>

      {limits ? (
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (cpusOk && memoryOk && changed && !busy) setConfirming(true);
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label={t('vm.size.cpus')}
              htmlFor="vm-cpus"
              hint={
                cpusOk || form.cpus === '' ? (
                  t('vm.size.cpusHintMac', { min: limits.minCpus, max: limits.maxCpus })
                ) : (
                  <Bad>{t('vm.size.cpusHintMacBad', { min: limits.minCpus, max: limits.maxCpus })}</Bad>
                )
              }
            >
              <div className="relative">
                <Cpu className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                <Input
                  id="vm-cpus"
                  type="number"
                  inputMode="numeric"
                  min={limits.minCpus}
                  max={limits.maxCpus}
                  step={1}
                  className="h-8 pl-8 font-mono text-[12.5px]"
                  disabled={busy}
                  value={form.cpus}
                  onChange={(e) => {
                    setEdited(true);
                    setForm({ ...form, cpus: e.target.value });
                  }}
                />
              </div>
            </Field>
            <Field
              label={t('vm.size.memoryLabel')}
              htmlFor="vm-memory"
              hint={
                memoryOk || form.memory === '' ? (
                  t('vm.size.memoryHintMac', { min: gibText(limits.minMemory), max: gibText(limits.maxMemory) })
                ) : (
                  <Bad>{t('vm.size.memoryHintMac', { min: gibText(limits.minMemory), max: gibText(limits.maxMemory) })}</Bad>
                )
              }
            >
              <div className="relative">
                <MemoryStick className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                <Input
                  id="vm-memory"
                  type="number"
                  inputMode="decimal"
                  min={gib(limits.minMemory)}
                  max={gib(limits.maxMemory)}
                  step={0.5}
                  className="h-8 pl-8 font-mono text-[12.5px]"
                  disabled={busy}
                  value={form.memory}
                  onChange={(e) => {
                    setEdited(true);
                    setForm({ ...form, memory: e.target.value });
                  }}
                />
              </div>
            </Field>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" variant="primary" size="sm" disabled={busy || !changed || !cpusOk || !memoryOk} data-vm-resize>
              {busy && <LoaderCircle className="animate-spin" />}
              {busy ? t('vm.size.resizing') : t('vm.size.resize')}
            </Button>
            {edited && !busy && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEdited(false);
                  resize.reset();
                }}
              >
                <RotateCcw />
                {t('common.reset')}
              </Button>
            )}
            <span className="text-xs text-subtle">
              {busy ? t('vm.size.busyMac') : t('vm.size.restartsVM')}
            </span>
          </div>
        </form>
      ) : (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">{t('vm.size.tooOld')}</p>
          <CommandBox command="agentbox vm resize --cpus 6 --memory 12GiB" />
        </div>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label={t('vm.size.log')} />}
      {resize.error && <Notice>{errorMessage(resize.error)}</Notice>}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('vm.size.confirmTitle')}
        description={t.rich('vm.size.confirmMac', {
          cpus,
          memory: formatNumber(memory),
          driver: vm.driver,
          running,
          b: (c) => <strong className="font-medium text-primary">{c}</strong>,
        })}
        confirmLabel={t('vm.size.restartResize')}
        destructive
        // The resize outlives the dialog: its log is on this page.
        onConfirm={async () => resize.mutate({ cpus, memory })}
      />
    </Panel>
  );
}

function Bad({ children }: { children: string }) {
  return <span className="text-rose-300">{children}</span>;
}

// CHVSize is the size of AgentBox's Cloud Hypervisor VM on Linux: its CPUs and
// its memory cap, the most memory it takes as its agents need it (it boots
// with less, and gives back what they stop using). `agentbox vm resize`
// changes a running VM at once when the new size fits in what it booted with
// room for (vm.live: every core and all of the host's memory, for a VM started
// by this version), and every agent keeps running; only a size that doesn't
// fit restarts the VM, which the dialog says before it happens. Its disk for
// agents (Incus's pool) only grows: while it runs on Cloud Hypervisor, and
// with a restart on the vz driver. It's sparse, so its size is only what it
// may grow to on this computer's disk.
export function CHVSize({ vm, busy: resizing }: { vm: T.VMStatus; busy: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const running = (agents.data ?? []).filter((a) => a.state === 'running').length;
  // The disk it has, or will have when it next starts. A front end older than
  // disk resizing says neither, and the disk field isn't shown.
  const hasDisk = Math.max(vm.disk.pool.size, vm.limits?.minDisk ?? 0);
  const current = () => ({ cpus: String(vm.cpus ?? ''), memory: vm.memory.cap ? gib(vm.memory.cap) : '', disk: hasDisk ? gib(hasDisk) : '' });
  const [form, setForm] = useState(current);
  const [confirming, setConfirming] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.vm.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  const [edited, setEdited] = useState(false);
  useEffect(() => {
    if (!edited) setForm({ cpus: String(vm.cpus ?? ''), memory: vm.memory.cap ? gib(vm.memory.cap) : '', disk: hasDisk ? gib(hasDisk) : '' });
  }, [vm.cpus, vm.memory.cap, hasDisk, edited]);

  const resize = useMutation({
    mutationFn: ({ cpus, memory, disk, restart }: { cpus: number; memory: number; disk?: number; restart: boolean }) =>
      window.agentbox.vm.resize(cpus, `${memory}GiB`, restart, disk ? `${disk}GiB` : undefined),
    onMutate: () => setLines([]),
    onSuccess: async (_, { cpus, memory, disk, restart }) => {
      setEdited(false);
      toast(t('vm.size.toastCHV', { cpus, memory: formatNumber(memory), hasDisk: disk ? 'yes' : 'no', disk: formatNumber(disk ?? 0) }), {
        description: restart ? t('vm.size.toastRestart') : on ? t('vm.size.toastLive') : t('vm.size.toastLater'),
      });
      await queryClient.invalidateQueries({ queryKey: ['host-setup'] });
    },
  });

  const limits = vm.limits;
  const live = vm.live;
  const on = vm.state === 'running' || vm.state === 'starting';
  const cpus = Number(form.cpus);
  const memory = Number(form.memory);
  const cpusOk = Number.isInteger(cpus) && limits !== undefined && cpus >= limits.minCpus && cpus <= limits.maxCpus;
  const memoryOk =
    form.memory.trim() !== '' && Number.isFinite(memory) && limits !== undefined && memory * GiB >= limits.minMemory && memory * GiB <= limits.maxMemory;
  const disk = Number(form.disk);
  const minDisk = limits?.minDisk ?? 0;
  const maxDisk = limits?.maxDisk ?? 0;
  const diskOk = !minDisk || (form.disk.trim() !== '' && Number.isFinite(disk) && disk * GiB >= minDisk - GiB / 20 && disk * GiB <= maxDisk);
  // Only a bigger disk is sent: one that stays as it is goes unsaid.
  const diskGrows = minDisk > 0 && diskOk && disk * GiB - hasDisk >= GiB / 20;
  const changed = cpus !== vm.cpus || Math.abs(memory * GiB - vm.memory.cap) >= GiB / 20 || diskGrows;
  // A VM that's off takes the new size when it starts; a running one takes it
  // now, when it fits, and otherwise only by restarting.
  const needsRestart =
    on &&
    !(
      live &&
      cpus >= live.minCpus &&
      cpus <= live.maxCpus &&
      memory * GiB >= live.minMemory &&
      memory * GiB <= live.maxMemory &&
      (!diskGrows || disk * GiB <= (live.maxDisk ?? 0))
    );
  const request = () => ({ cpus, memory, disk: diskGrows ? disk : undefined });
  const busy = resize.isPending || resizing;

  return (
    <Panel className="mt-3 grid gap-3 p-4" data-vm-size data-chv>
      <div>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <div className="text-[13px] font-medium text-primary">{t('vm.agentboxVM')}</div>
          {vm.driver === 'vz' && <Badge variant="warning">{t('vm.size.experimental')}</Badge>}
          <div className="text-[12px] text-subtle" data-vm-size-current>
            {t('vm.size.currentCHV', {
              state: vm.state,
              cpus: vm.cpus ?? '—',
              memory: vm.memory.cap ? `${gibText(vm.memory.cap)} GiB` : '—',
              granted: on && vm.memory.granted ? 'yes' : 'no',
              grantedGib: gibText(vm.memory.granted),
              hasDisk: hasDisk ? 'yes' : 'no',
              disk: gibText(hasDisk),
            })}
          </div>
        </div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          {t('vm.size.descriptionCHV')}
        </p>
      </div>

      {limits ? (
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (!cpusOk || !memoryOk || !diskOk || !changed || busy) return;
            if (needsRestart) setConfirming(true);
            else resize.mutate({ ...request(), restart: false });
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label={t('vm.size.cpus')}
              htmlFor="vm-cpus"
              hint={
                cpusOk || form.cpus === '' ? (
                  t('vm.size.cpusHintCHV', { min: limits.minCpus, max: limits.maxCpus })
                ) : (
                  <Bad>{t('vm.size.cpusHintCHVBad', { min: limits.minCpus, max: limits.maxCpus })}</Bad>
                )
              }
            >
              <div className="relative">
                <Cpu className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                <Input
                  id="vm-cpus"
                  type="number"
                  inputMode="numeric"
                  min={limits.minCpus}
                  max={limits.maxCpus}
                  step={1}
                  className="h-8 pl-8 font-mono text-[12.5px]"
                  disabled={busy}
                  value={form.cpus}
                  onChange={(e) => {
                    setEdited(true);
                    setForm({ ...form, cpus: e.target.value });
                  }}
                />
              </div>
            </Field>
            <Field
              label={t('vm.size.memoryCapLabel')}
              htmlFor="vm-memory"
              hint={
                memoryOk || form.memory === '' ? (
                  t('vm.size.memoryHintCHV', { min: gibText(limits.minMemory), max: gibText(limits.maxMemory) })
                ) : (
                  <Bad>{t('vm.size.memoryHintCHVBad', { min: gibText(limits.minMemory), max: gibText(limits.maxMemory) })}</Bad>
                )
              }
            >
              <div className="relative">
                <MemoryStick className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                <Input
                  id="vm-memory"
                  type="number"
                  inputMode="decimal"
                  min={gib(limits.minMemory)}
                  max={gib(limits.maxMemory)}
                  step={1}
                  className="h-8 pl-8 font-mono text-[12.5px]"
                  disabled={busy}
                  value={form.memory}
                  onChange={(e) => {
                    setEdited(true);
                    setForm({ ...form, memory: e.target.value });
                  }}
                />
              </div>
            </Field>
            {minDisk > 0 && (
              <Field
                label={t('vm.size.diskLabel')}
                htmlFor="vm-disk"
                hint={
                  diskOk || form.disk === '' ? (
                    t('vm.size.diskHint', { now: gibText(hasDisk), max: gibText(maxDisk) })
                  ) : (
                    <Bad>{t('vm.size.diskHintBad', { now: gibText(hasDisk), max: gibText(maxDisk) })}</Bad>
                  )
                }
              >
                <div className="relative">
                  <HardDrive className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                  <Input
                    id="vm-disk"
                    type="number"
                    inputMode="numeric"
                    min={gib(hasDisk)}
                    max={gib(maxDisk)}
                    step={10}
                    className="h-8 pl-8 font-mono text-[12.5px]"
                    disabled={busy}
                    value={form.disk}
                    onChange={(e) => {
                      setEdited(true);
                      setForm({ ...form, disk: e.target.value });
                    }}
                    data-vm-disk
                  />
                </div>
              </Field>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" variant="primary" size="sm" disabled={busy || !changed || !cpusOk || !memoryOk || !diskOk} data-vm-resize>
              {busy && <LoaderCircle className="animate-spin" />}
              {busy ? t('vm.size.resizing') : needsRestart ? t('vm.size.restartResize') : t('vm.size.resize')}
            </Button>
            {edited && !busy && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEdited(false);
                  resize.reset();
                }}
              >
                <RotateCcw />
                {t('common.reset')}
              </Button>
            )}
            <span className="text-xs text-subtle" data-vm-resize-hint={needsRestart ? 'restart' : on ? 'live' : 'off'}>
              {busy
                ? needsRestart
                  ? t('vm.size.busyRestart')
                  : t('vm.size.busyLive')
                : !on
                  ? t('vm.size.hintOff')
                  : needsRestart
                    ? live
                      ? diskGrows && !live.maxDisk
                        ? t('vm.size.hintDisk')
                        : t('vm.size.hintRoom', { cpus: live.maxCpus, memory: gibText(live.maxMemory) })
                      : t('vm.size.hintOlder')
                    : t('vm.size.hintLive')}
            </span>
          </div>
        </form>
      ) : (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">{t('vm.size.tooOld')}</p>
          <CommandBox command="agentbox vm resize --cpus 6 --memory-cap 16GiB" />
        </div>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label={t('vm.size.log')} />}
      {resize.error && <Notice>{errorMessage(resize.error)}</Notice>}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('vm.size.confirmTitle')}
        description={t.rich('vm.size.confirmCHV', {
          cpus,
          memory: formatNumber(memory),
          diskGrows: diskGrows ? 'yes' : 'no',
          disk: formatNumber(disk),
          running,
          b: (c) => <strong className="font-medium text-primary">{c}</strong>,
        })}
        confirmLabel={t('vm.size.restartResize')}
        destructive
        onConfirm={async () => resize.mutate({ ...request(), restart: true })}
      />
    </Panel>
  );
}
