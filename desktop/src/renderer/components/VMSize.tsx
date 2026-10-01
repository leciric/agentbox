import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Cpu, LoaderCircle, MemoryStick, RotateCcw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMStatus } from '../../preload';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
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

// VMSize is the CPUs and memory of AgentBox's Linux VM on a Mac, which the
// daemon, Incus and every agent share, and a way to change them: `agentbox vm
// resize`, run by the main process. Lima only changes a stopped VM, so that
// restarts the VM and stops every agent — which the dialog says before it
// happens. The disk stays the size it was made with.
export function VMSize({ vm, busy: resizing }: { vm: VMStatus; busy: boolean }) {
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
      toast(`AgentBox's VM has ${cpus} CPUs and ${memory} GiB of memory`, { description: 'Start the agents you need again from their Overview tabs.' });
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
          <div className="text-[13px] font-medium text-primary">AgentBox's VM</div>
          {vm.driver === 'vz' && <Badge variant="warning">Experimental: vz driver</Badge>}
          <div className="text-[12px] text-subtle" data-vm-size-current>
            {vm.status ?? 'Unknown'} · {vm.cpus ?? '—'} CPUs · {vm.memory ? `${gib(vm.memory)} GiB` : '—'} of memory
            {vm.disk ? ` · ${gib(vm.disk)} GiB disk` : ''}
          </div>
        </div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          On a Mac, the daemon, Incus and every agent run in one Linux VM, and share what it has. Changing it
          restarts the VM, which stops every agent. The disk keeps the size it was made with.
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
              label="CPUs"
              htmlFor="vm-cpus"
              hint={cpusOk || form.cpus === '' ? `${limits.minCpus} to ${limits.maxCpus} on this Mac.` : <Bad>{`${limits.minCpus} to ${limits.maxCpus} on this Mac, in whole CPUs.`}</Bad>}
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
              label="Memory, in GiB"
              htmlFor="vm-memory"
              hint={
                memoryOk || form.memory === '' ? (
                  `${gib(limits.minMemory)} to ${gib(limits.maxMemory)} GiB on this Mac, which keeps 2 GiB for itself.`
                ) : (
                  <Bad>{`${gib(limits.minMemory)} to ${gib(limits.maxMemory)} GiB on this Mac, which keeps 2 GiB for itself.`}</Bad>
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
              {busy ? 'Resizing…' : 'Resize the VM'}
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
                Reset
              </Button>
            )}
            <span className="text-xs text-subtle">
              {busy ? 'Stopping the VM, resizing it and starting it again. This takes a minute or two.' : 'Restarts the VM and stops every agent.'}
            </span>
          </div>
        </form>
      ) : (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">This agentbox is too old to resize the VM from here. Update the app, or run it in a terminal:</p>
          <CommandBox command="agentbox vm resize --cpus 6 --memory 12GiB" />
        </div>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label="VM resize log" />}
      {resize.error && <Notice>{errorMessage(resize.error)}</Notice>}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Restart AgentBox's VM?"
        description={
          <>
            It will have {cpus} CPUs and {memory} GiB of memory.{' '}
            {vm.driver === 'vz' ? 'More CPUs or memory need the VM restarted, so it may stop, and' : 'Lima only resizes a stopped VM, so it stops, and'}{' '}
            {running > 0 ? (
              <strong className="font-medium text-primary">
                {running === 1 ? 'the agent running now stops' : `the ${running} agents running now stop`}
              </strong>
            ) : (
              'every agent stops'
            )}{' '}
            with it: their terminals, dev servers and any turn in progress. Their worktrees and branches are on your Mac and stay. The app can't reach the
            daemon until the VM is back, in a minute or two.
          </>
        }
        confirmLabel="Restart and resize"
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
// fit restarts the VM, which the dialog says before it happens.
export function CHVSize({ vm, busy: resizing }: { vm: T.VMStatus; busy: boolean }) {
  const queryClient = useQueryClient();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const running = (agents.data ?? []).filter((a) => a.state === 'running').length;
  const current = { cpus: String(vm.cpus ?? ''), memory: vm.memory.cap ? gib(vm.memory.cap) : '' };
  const [form, setForm] = useState(current);
  const [confirming, setConfirming] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.vm.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  const [edited, setEdited] = useState(false);
  useEffect(() => {
    if (!edited) setForm({ cpus: String(vm.cpus ?? ''), memory: vm.memory.cap ? gib(vm.memory.cap) : '' });
  }, [vm.cpus, vm.memory.cap, edited]);

  const resize = useMutation({
    mutationFn: ({ cpus, memory, restart }: { cpus: number; memory: number; restart: boolean }) =>
      window.agentbox.vm.resize(cpus, `${memory}GiB`, restart),
    onMutate: () => setLines([]),
    onSuccess: async (_, { cpus, memory, restart }) => {
      setEdited(false);
      toast(`AgentBox's VM has ${cpus} CPUs and a memory cap of ${memory} GiB`, {
        description: restart
          ? 'Start the agents you need again from their Overview tabs.'
          : on
            ? 'Every agent kept running.'
            : 'It has them from when it next starts.',
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
  const changed = cpus !== vm.cpus || Math.abs(memory * GiB - vm.memory.cap) >= GiB / 20;
  // A VM that's off takes the new size when it starts; a running one takes it
  // now, when it fits, and otherwise only by restarting.
  const needsRestart = on && !(live && cpus >= live.minCpus && cpus <= live.maxCpus && memory * GiB >= live.minMemory && memory * GiB <= live.maxMemory);
  const busy = resize.isPending || resizing;

  return (
    <Panel className="mt-3 grid gap-3 p-4" data-vm-size data-chv>
      <div>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <div className="text-[13px] font-medium text-primary">AgentBox's VM</div>
          {vm.driver === 'vz' && <Badge variant="warning">Experimental: vz driver</Badge>}
          <div className="text-[12px] text-subtle" data-vm-size-current>
            {vm.state} · {vm.cpus ?? '—'} CPUs · memory up to {vm.memory.cap ? `${gib(vm.memory.cap)} GiB` : '—'}
            {on && vm.memory.granted ? `, ${gib(vm.memory.granted)} GiB now` : ''}
          </div>
        </div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          The daemon, Incus and every agent run in one VM, and share what it has. The VM starts small and takes
          memory as its agents need it, up to the cap, and gives it back as they stop.
        </p>
      </div>

      {limits ? (
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (!cpusOk || !memoryOk || !changed || busy) return;
            if (needsRestart) setConfirming(true);
            else resize.mutate({ cpus, memory, restart: false });
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label="CPUs"
              htmlFor="vm-cpus"
              hint={
                cpusOk || form.cpus === '' ? (
                  `${limits.minCpus} to ${limits.maxCpus}, this computer's cores.`
                ) : (
                  <Bad>{`${limits.minCpus} to ${limits.maxCpus}, in whole CPUs.`}</Bad>
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
              label="Memory cap, in GiB"
              htmlFor="vm-memory"
              hint={
                memoryOk || form.memory === '' ? (
                  `${gib(limits.minMemory)} to ${gib(limits.maxMemory)} GiB, all of this computer's. The VM only takes what its agents use.`
                ) : (
                  <Bad>{`${gib(limits.minMemory)} to ${gib(limits.maxMemory)} GiB.`}</Bad>
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
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" variant="primary" size="sm" disabled={busy || !changed || !cpusOk || !memoryOk} data-vm-resize>
              {busy && <LoaderCircle className="animate-spin" />}
              {busy ? 'Resizing…' : needsRestart ? 'Restart and resize' : 'Resize the VM'}
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
                Reset
              </Button>
            )}
            <span className="text-xs text-subtle" data-vm-resize-hint={needsRestart ? 'restart' : on ? 'live' : 'off'}>
              {busy
                ? needsRestart
                  ? 'Restarting the VM at its new size. This takes a minute.'
                  : 'Resizing the VM.'
                : !on
                  ? 'The VM is off: it has the new size when it next starts.'
                  : needsRestart
                    ? live
                      ? `The VM started with room for ${live.maxCpus} CPUs and ${gib(live.maxMemory)} GiB: this needs a restart, which stops every agent.`
                      : 'This VM was started by an older AgentBox: resizing it restarts it, which stops every agent.'
                    : 'Changes at once. Every agent keeps running.'}
            </span>
          </div>
        </form>
      ) : (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">This agentbox is too old to resize the VM from here. Update the app, or run it in a terminal:</p>
          <CommandBox command="agentbox vm resize --cpus 6 --memory-cap 16GiB" />
        </div>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label="VM resize log" />}
      {resize.error && <Notice>{errorMessage(resize.error)}</Notice>}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Restart AgentBox's VM?"
        description={
          <>
            It will have {cpus} CPUs and a memory cap of {memory} GiB. That's more than the running VM has room for, so it restarts, and{' '}
            {running > 0 ? (
              <strong className="font-medium text-primary">
                {running === 1 ? 'the agent running now stops' : `the ${running} agents running now stop`}
              </strong>
            ) : (
              'every agent stops'
            )}{' '}
            with it: their terminals, dev servers and any turn in progress. Their worktrees and branches are in your home folder and stay. After this
            restart, the VM can be resized again without one.
          </>
        }
        confirmLabel="Restart and resize"
        destructive
        onConfirm={async () => resize.mutate({ cpus, memory, restart: true })}
      />
    </Panel>
  );
}
