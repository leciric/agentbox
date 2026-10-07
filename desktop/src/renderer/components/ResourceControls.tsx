import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, LoaderCircle, MonitorCog, Pause, Play, Power } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type { VMPower, VMPowerAction } from '../../preload';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatList, useT, type MessageKey } from '../lib/i18n';
import {
  coresText,
  freed,
  freeMode,
  freeTargets,
  loadRestorable,
  progress,
  saveRestorable,
  vmHeld,
  vmTransitions,
  whoText,
  type FreeTarget,
} from '../lib/freeResources';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { actOnVM, ensureVMRunning, useVMPower } from '../lib/vm';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';
import { Tip } from './ui/tooltip';

// FreeRun is one Free resources, from its confirmation to what it freed. It
// lives in ResourceControls, not in the dialog, so closing the dialog while
// the agents stop only hides it: the top bar's button keeps showing the
// progress, and opening it again picks up where it is.
export interface FreeRun {
  phase: 'confirm' | 'stopping' | 'vm' | 'done' | 'error';
  targets: FreeTarget[];
  vmBefore: VMPower | null;
  result?: T.StopAgentsResult;
  vmStopped?: boolean;
  vmError?: string;
  error?: string;
}

// ResourceControls is the top bar's resource controls: in VM mode the VM's
// indicator, and the Free resources button, which turns into Start once
// everything is off.
export function ResourceControls({ agents }: { agents: T.Agent[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  const vm = useVMPower().data ?? null;
  const [restorable, setRestorable] = useState(loadRestorable);
  const [run, setRun] = useState<FreeRun | null>(null);
  const [open, setOpen] = useState(false);
  const [starting, setStarting] = useState(false);
  // While Free resources runs, the VM is on its way off, whatever it's doing
  // meanwhile: a paused one is resumed first, so its agents stop cleanly, and
  // the pill says Stopping throughout rather than Paused, then Resuming.
  const running = run?.phase === 'stopping' || run?.phase === 'vm';
  // While agents stop, the list is what the progress counts: events move it,
  // and this covers a stream that's reconnecting.
  const live = useQuery({
    queryKey: ['agents'],
    queryFn: api.agents,
    refetchInterval: run?.phase === 'stopping' ? 1_500 : false,
    enabled: running,
  });
  const now = live.data ?? agents;
  const mode = running ? 'busy' : freeMode(now, vm, restorable);

  const begin = () => {
    if (!running) setRun({ phase: 'confirm', targets: freeTargets(now), vmBefore: vm });
    setOpen(true);
  };

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['agents'] });
    await queryClient.invalidateQueries({ queryKey: ['usage'] });
  };

  const confirm = async () => {
    if (!run) return;
    const { targets, vmBefore } = run;
    setRun({ ...run, phase: 'stopping' });
    let result: T.StopAgentsResult | undefined;
    try {
      if (targets.length > 0) {
        // A paused VM's daemon can't answer: wake it, so its agents are
        // stopped cleanly rather than cut off with it.
        if (vmBefore) await ensureVMRunning();
        const job = await api.stopAgents(targets.map((target) => target.ref));
        const done = await finished(job.id);
        if (done.status !== 'succeeded') throw new Error(done.error || t('vm.free.stopFailed'));
        result = done.result as T.StopAgentsResult;
        const refs = result.stopped.map((a) => a.ref);
        saveRestorable(refs);
        setRestorable(refs);
      }
    } catch (err) {
      setRun((r) => r && { ...r, phase: 'error', error: errorMessage(err) });
      await refresh();
      return;
    }
    let vmStopped = false;
    let vmError: string | undefined;
    if (vmBefore) {
      setRun((r) => r && { ...r, phase: 'vm', result });
      try {
        await actOnVM('stop');
        vmStopped = true;
      } catch (err) {
        vmError = errorMessage(err);
      }
    }
    setRun((r) => r && { ...r, phase: 'done', result, vmStopped, vmError });
    setOpen(true);
    await refresh();
  };

  // start is the one click back: the VM, then the agents Free resources
  // stopped. Any one of them that won't start is said, not fatal.
  const start = async (refs: string[]) => {
    setStarting(true);
    try {
      if (vm && vm.state !== 'running') await actOnVM(vm.state === 'paused' ? 'resume' : 'start');
      const results = await Promise.allSettled(refs.map((ref) => api.agentAction(ref, 'start')));
      const failed = results.filter((r) => r.status === 'rejected').length;
      saveRestorable([]);
      setRestorable([]);
      const what = formatList([vm ? t('vm.free.whatVM') : null, refs.length ? t('vm.free.whatAgents', { count: refs.length - failed }) : null].filter((x) => x !== null));
      if (failed) toast.error(t('vm.free.startedWithFailures', { what, failed }));
      else if (what) toast.success(t('vm.free.started', { what }));
      setOpen(false);
      setRun(null);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setStarting(false);
      await refresh();
    }
  };

  return (
    <>
      {vm && <VMIndicator vm={running && vm.state !== 'off' ? { ...vm, state: 'stopping' } : vm} onStop={begin} />}
      <FreeButton
        mode={starting ? 'busy' : mode}
        vm={vm}
        run={run}
        now={now}
        restorable={restorable}
        onFree={begin}
        onStart={() => void start(vm ? restorable : restorable.filter((ref) => now.some((a) => a.ref === ref && a.state === 'stopped')))}
        onShow={() => setOpen(true)}
      />
      {run && (
        <FreeResourcesDialog
          open={open}
          onOpenChange={(next) => {
            setOpen(next);
            // A run that's over is forgotten once its result is closed.
            if (!next && (run.phase === 'confirm' || run.phase === 'done' || run.phase === 'error')) setRun(null);
          }}
          run={run}
          agents={now}
          onConfirm={() => void confirm()}
          onStartAgain={() => void start(run.result?.stopped.map((a) => a.ref) ?? [])}
          starting={starting}
        />
      )}
    </>
  );
}

// finished waits for a job to end, the way JobProgress follows one.
async function finished(id: string): Promise<T.Job> {
  for (;;) {
    const job = await api.job(id);
    if (job.status !== 'running') return job;
    await new Promise((resolve) => setTimeout(resolve, 800));
  }
}

function FreeButton({
  mode,
  vm,
  run,
  now,
  restorable,
  onFree,
  onStart,
  onShow,
}: {
  mode: ReturnType<typeof freeMode>;
  vm: VMPower | null;
  run: FreeRun | null;
  now: T.Agent[];
  restorable: string[];
  onFree: () => void;
  onStart: () => void;
  onShow: () => void;
}) {
  const t = useT();
  if (mode === 'idle') return null;
  if (mode === 'busy') {
    const stopping = run?.phase === 'stopping' || run?.phase === 'vm';
    const { done, total } = run ? progress(run.targets, now) : { done: 0, total: 0 };
    const label = stopping
      ? run.phase === 'vm'
        ? t('vm.free.busyVM')
        : t('vm.free.busyFreeing', { done, total })
      : vm?.state === 'stopping'
        ? t('vm.free.busyStopping')
        : vm?.state === 'pausing'
          ? t('vm.free.busyPausing')
          : t('vm.free.busyStarting');
    return (
      <button
        type="button"
        className="flex items-center gap-1.5 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs text-muted transition hover:bg-surface-raised"
        onClick={stopping ? onShow : undefined}
        disabled={!stopping}
        aria-label={label}
        data-free-mode="busy"
      >
        <LoaderCircle className="size-3.5 animate-spin" />
        <span className="font-medium tabular-nums">{label}</span>
      </button>
    );
  }
  if (mode === 'start') {
    const count = vm ? restorable.length : restorable.filter((ref) => now.some((a) => a.ref === ref && a.state === 'stopped')).length;
    const label = vm ? (count ? t('vm.free.startVMAgents', { count }) : t('vm.free.startVM')) : count ? t('vm.free.startAgents', { count }) : t('common.start');
    return (
      <Tip label={label}>
        <button
          type="button"
          className="flex items-center gap-1.5 rounded-full bg-emerald-400/10 px-2.5 py-1 text-xs font-medium text-emerald-200 ring-1 ring-inset ring-emerald-400/30 transition hover:bg-emerald-400/15"
          onClick={onStart}
          aria-label={label}
          data-free-mode="start"
        >
          <Play className="size-3.5" />
          <span>{t('common.start')}</span>
        </button>
      </Tip>
    );
  }
  const count = freeTargets(now).length;
  const what = count ? (vm ? 'both' : 'agents') : 'vm';
  return (
    <Tip label={t('vm.free.tip', { what, count })}>
      <button
        type="button"
        className="flex items-center gap-1.5 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs font-medium text-secondary transition hover:border-rose-400/40 hover:bg-rose-400/10 hover:text-rose-200"
        onClick={onFree}
        aria-label={t('vm.free.button')}
        data-free-mode="free"
      >
        <Power className="size-3.5" />
        <span className="hidden xl:inline">{t('vm.free.button')}</span>
        {count > 0 && <span className="font-mono text-[11px] tabular-nums text-subtle">{count}</span>}
      </button>
    </Tip>
  );
}

const doingText: Record<FreeTarget['doing'], MessageKey> = {
  working: 'vm.free.doing.working',
  asking: 'vm.free.doing.asking',
  idle: 'vm.free.doing.idle',
  paused: 'vm.free.doing.paused',
};
const doingTone: Record<FreeTarget['doing'], string> = {
  working: 'bg-brand-400/15 text-brand-300 ring-brand-400/30',
  asking: 'bg-amber-400/10 text-amber-200 ring-amber-400/30',
  idle: 'bg-surface-raised text-muted ring-line',
  paused: 'bg-surface-raised text-muted ring-line',
};

export function FreeResourcesDialog({
  open,
  onOpenChange,
  run,
  agents,
  onConfirm,
  onStartAgain,
  starting,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  run: FreeRun;
  agents: T.Agent[];
  onConfirm: () => void;
  onStartAgain: () => void;
  starting: boolean;
}) {
  const t = useT();
  const { targets, vmBefore } = run;
  const count = targets.length;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md" data-free-phase={run.phase}>
        {run.phase === 'confirm' && (
          <>
            <DialogHeader>
              <DialogTitle>{t('vm.free.confirmTitle')}</DialogTitle>
              <DialogDescription>
                {count > 0
                  ? vmBefore
                    ? t('vm.free.confirmBoth', { count, memory: humanBytes(vmHeld(vmBefore)) })
                    : t('vm.free.confirmAgents', { count })
                  : vmBefore
                    ? t('vm.free.confirmVM', { memory: humanBytes(vmHeld(vmBefore)) })
                    : t('vm.free.confirmNone')}
              </DialogDescription>
            </DialogHeader>
            {whoText(targets) && (
              <Notice tone="warning">
                {t.rich('vm.free.cutOff', { who: whoText(targets), count, b: (c) => <span className="font-medium">{c}</span> })}
              </Notice>
            )}
            {count > 0 && (
              <ul className="grid max-h-80 gap-1 overflow-y-auto rounded-xl border border-line bg-surface-faint p-1.5" aria-label={t('vm.free.agentsToStop')}>
                {targets.map((target) => (
                  <li key={target.ref} className="flex min-w-0 items-center justify-between gap-3 rounded-lg px-2 py-1.5 text-[13px]">
                    <span className="min-w-0 truncate text-secondary">{target.label}</span>
                    <span className={cn('shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset', doingTone[target.doing])}>
                      {t(doingText[target.doing])}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            <DialogFooter>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                {t('common.cancel')}
              </Button>
              <Button variant="destructive" onClick={onConfirm} data-free-confirm>
                <Power />
                {count > 0 ? t(vmBefore ? 'vm.free.stopAgentsVM' : 'vm.free.stopAgents', { count }) : t('vm.free.turnVMOff')}
              </Button>
            </DialogFooter>
          </>
        )}

        {(run.phase === 'stopping' || run.phase === 'vm') && <FreeProgress run={run} agents={agents} onHide={() => onOpenChange(false)} />}

        {run.phase === 'done' && <FreeResult run={run} onClose={() => onOpenChange(false)} onStartAgain={onStartAgain} starting={starting} />}

        {run.phase === 'error' && (
          <>
            <DialogHeader>
              <DialogTitle>{t('vm.free.errorTitle')}</DialogTitle>
            </DialogHeader>
            <Notice>{run.error}</Notice>
            <DialogFooter>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                {t('common.close')}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function FreeProgress({ run, agents, onHide }: { run: FreeRun; agents: T.Agent[]; onHide: () => void }) {
  const t = useT();
  const { done, total } = progress(run.targets, agents);
  const vmSteps = run.vmBefore ? 1 : 0;
  const steps = total + vmSteps;
  const complete = run.phase === 'vm' ? total : done;
  const percent = steps ? (complete / steps) * 100 : 0;
  return (
    <>
      <DialogHeader>
        <DialogTitle>{t('vm.free.progressTitle')}</DialogTitle>
        <DialogDescription>
          {run.phase === 'vm'
            ? t('vm.free.progressVM')
            : t('vm.free.progressAgents', { done, total, vm: run.vmBefore ? 'yes' : 'no' })}
        </DialogDescription>
      </DialogHeader>
      <div className="h-1.5 overflow-hidden rounded-full bg-surface-strong" role="progressbar" aria-valuenow={complete} aria-valuemax={steps}>
        <div
          className="h-full rounded-full bg-gradient-to-r from-brand-400 to-sky-400 transition-[width] duration-500"
          style={{ width: `${Math.max(percent, 3)}%` }}
        />
      </div>
      <ul className="grid max-h-72 gap-0.5 overflow-y-auto" aria-label={t('vm.free.progress')}>
        {run.targets.map((target) => {
          const a = agents.find((x) => x.ref === target.ref);
          const stopped = run.phase === 'vm' || !a || a.state === 'stopped';
          return <Step key={target.ref} done={stopped} label={target.label} detail={stopped ? t('vm.free.stopped') : t('vm.free.stoppingStep')} />;
        })}
      </ul>
      {/* The VM's step stays in view below the list, however long it is. */}
      {run.vmBefore && (
        <ul className="-mt-3 border-t border-line pt-2">
          <Step done={false} pending={run.phase !== 'vm'} label={t('vm.agentboxVM')} detail={run.phase === 'vm' ? t('vm.free.turningOff') : t('vm.free.afterAgents')} />
        </ul>
      )}
      <DialogFooter>
        <Button variant="ghost" onClick={onHide}>
          {t('vm.free.hide')}
        </Button>
      </DialogFooter>
    </>
  );
}

function Step({ done, pending, label, detail }: { done: boolean; pending?: boolean; label: string; detail: string }) {
  return (
    <li className="flex min-w-0 items-center gap-2.5 rounded-lg px-1.5 py-1 text-[13px]" data-step={done ? 'done' : pending ? 'pending' : 'active'}>
      <span className="flex size-4 shrink-0 items-center justify-center">
        {done ? (
          <Check className="size-4 text-emerald-300" />
        ) : pending ? (
          <span className="size-1.5 rounded-full bg-surface-strong" />
        ) : (
          <LoaderCircle className="size-4 animate-spin text-brand-300" />
        )}
      </span>
      <span className={cn('min-w-0 flex-1 truncate', done ? 'text-muted' : 'text-secondary')}>{label}</span>
      <span className="shrink-0 text-[11.5px] text-faint">{detail}</span>
    </li>
  );
}

function FreeResult({ run, onClose, onStartAgain, starting }: { run: FreeRun; onClose: () => void; onStartAgain: () => void; starting: boolean }) {
  const t = useT();
  const got = freed(run.result, run.vmBefore, !!run.vmStopped);
  const stopped = run.result?.stopped ?? [];
  const failed = run.result?.failed ?? [];
  return (
    <>
      <DialogHeader>
        <DialogTitle>{t('vm.free.resultTitle')}</DialogTitle>
        <DialogDescription>{t('vm.free.resultDescription', { stopped: stopped.length, vm: run.vmStopped ? 'yes' : 'no' })}</DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-2 gap-2.5">
        <Stat id="memory" label={t('vm.free.memoryReturned')} value={humanBytes(got.memory)} detail={got.vm ? t('vm.free.allTheVM') : t('vm.free.ramSwap')} />
        <Stat id="cpu" label={t('vm.free.cpuReturned')} value={coresText(got.cpu)} detail={t('vm.free.agentsWereUsing')} />
      </div>
      {got.hostBefore !== undefined && got.hostAfter !== undefined && (
        <p className="text-[12.5px] text-muted">
          {t.rich('vm.free.hostMemory', {
            before: humanBytes(got.hostBefore),
            after: humanBytes(got.hostAfter),
            mono: (c) => <span className="font-mono tabular-nums text-secondary">{c}</span>,
          })}
        </p>
      )}
      {stopped.length > 0 && (
        <ul className="grid max-h-64 gap-0.5 overflow-y-auto rounded-xl border border-line bg-surface-faint p-1.5" aria-label={t('vm.free.stoppedAgents')}>
          {stopped.map((a) => (
            <li key={a.ref} className="flex min-w-0 items-center justify-between gap-3 rounded-lg px-2 py-1 text-[12.5px]">
              <span className="min-w-0 truncate text-secondary">
                {a.title || a.ref.split('/')[1]}
                {a.working && <span className="ml-1.5 text-[11px] text-faint">{t('vm.free.wasWorking')}</span>}
              </span>
              <span className="shrink-0 font-mono text-[11px] tabular-nums text-faint">
                {humanBytes(a.memory)} · {coresText(a.cpu)}
              </span>
            </li>
          ))}
        </ul>
      )}
      {failed.length > 0 && (
        <Notice>
          {t('vm.free.failed', { count: failed.length, errors: failed.map((f) => `${f.title || f.ref}: ${f.error}`).join('; ') })}
        </Notice>
      )}
      {run.vmError && <Notice>{t('vm.free.vmFailed', { error: run.vmError })}</Notice>}
      <DialogFooter>
        {stopped.length > 0 && (
          <Button variant="ghost" onClick={onStartAgain} disabled={starting}>
            {starting ? <LoaderCircle className="animate-spin" /> : <Play />}
            {t('vm.free.startAgain')}
          </Button>
        )}
        <Button variant="primary" onClick={onClose}>
          {t('common.done')}
        </Button>
      </DialogFooter>
    </>
  );
}

function Stat({ id, label, value, detail }: { id: string; label: string; value: string; detail: string }) {
  return (
    <div className="grid gap-0.5 rounded-xl border border-emerald-400/20 bg-emerald-400/[0.06] px-3.5 py-3" data-stat={id}>
      <span className="text-[11px] font-medium uppercase tracking-wider text-emerald-300">{label}</span>
      <span className="font-mono text-xl font-semibold tabular-nums text-emerald-200">{value}</span>
      <span className="text-[11px] text-muted">{detail}</span>
    </div>
  );
}

const vmStateText: Record<VMPower['state'], MessageKey> = {
  off: 'vm.state.off',
  starting: 'vm.state.starting',
  running: 'vm.state.running',
  pausing: 'vm.state.pausing',
  paused: 'vm.state.paused',
  resuming: 'vm.state.resuming',
  stopping: 'vm.state.stopping',
};

// VMIndicator is VM mode's pill: the VM's state and how much of its memory
// is in use, opening a popover with the details and its controls. Stopping
// goes through Free resources (onStop), so the agents inside are stopped
// cleanly and what that freed is shown, rather than cut off with the VM. On
// Windows it's AgentBox's WSL distro, which WSL can't pause.
export function VMIndicator({ vm, onStop }: { vm: VMPower; onStop: () => void }) {
  const t = useT();
  const wsl = vm.driver === 'wsl';
  const [busy, setBusy] = useState(false);
  const transitioning = busy || vmTransitions.includes(vm.state);
  const up = vm.state !== 'off' && vm.state !== 'starting';
  const act = async (action: VMPowerAction) => {
    setBusy(true);
    try {
      await actOnVM(action);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  const percent = vm.memoryGranted > 0 ? Math.min(1, vm.memoryUsed / vm.memoryGranted) * 100 : 0;
  const dot =
    vm.error && vm.state === 'off'
      ? 'bg-rose-400'
      : vm.state === 'running'
        ? 'bg-emerald-400 animate-glow'
        : vm.state === 'paused'
          ? 'bg-amber-400'
          : vm.state === 'off'
            ? 'bg-faint'
            : 'bg-brand-400 animate-pulse';
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="flex items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 transition hover:bg-surface-raised"
          aria-label={t(wsl ? 'vm.wslPower.aria' : 'vm.indicator.aria', {
            state: t(vmStateText[vm.state]),
            up: up ? 'yes' : 'no',
            used: humanBytes(vm.memoryUsed),
            total: humanBytes(vm.memoryGranted),
          })}
          data-vm-state={vm.state}
          data-vm-driver={vm.driver}
        >
          <span className={cn('size-1.5 rounded-full', dot)} />
          <span className="text-[11px] font-medium text-tertiary">{wsl ? 'WSL' : 'VM'}</span>
          <span className={cn('text-[11px] text-muted', up && 'hidden sm:inline')}>{t(vmStateText[vm.state])}</span>
          {up && (
            <>
              <span className="hidden w-36 whitespace-nowrap font-mono text-[11px] tabular-nums text-tertiary md:inline">
                {humanBytes(vm.memoryUsed)}/{humanBytes(vm.memoryGranted)}
              </span>
              <span className="hidden h-1 w-8 overflow-hidden rounded-full bg-surface-strong md:block">
                <span
                  className={cn(
                    'block h-full rounded-full',
                    percent > 85 ? 'bg-rose-400' : percent > 65 ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400',
                  )}
                  style={{ width: `${Math.max(percent, 4)}%` }}
                />
              </span>
            </>
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80">
        <div className="grid gap-3" data-vm-popover>
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-2 text-[13px] font-medium text-primary">
              <MonitorCog className="size-4 text-subtle" />
              {wsl ? t('vm.wslPower.title') : t('vm.agentboxVM')}
            </span>
            <span className="flex items-center gap-1.5 text-[12px] text-muted">
              <span className={cn('size-1.5 rounded-full', dot)} />
              {t(vmStateText[vm.state])}
            </span>
          </div>
          {wsl ? <WSLMemory vm={vm} /> : <VMMemory vm={vm} />}
          <p className="text-[11.5px] leading-relaxed text-muted">
            {wsl ? t('vm.wslPower.cpus', { cpus: vm.cpus }) : t('vm.indicator.cpus', { cpus: vm.cpus })}
            {vm.state === 'paused' && ` ${t('vm.indicator.pausedNote')}`}
          </p>
          {vm.error && <Notice className="text-[12px]">{vm.error}</Notice>}
          <div className="flex flex-wrap items-center gap-2 border-t border-line pt-3">
            {transitioning ? (
              <Button size="sm" disabled>
                <LoaderCircle className="animate-spin" />
                {t(vmStateText[vm.state])}…
              </Button>
            ) : vm.state === 'off' ? (
              <Button size="sm" variant="primary" onClick={() => void act('start')}>
                <Play />
                {t('common.start')}
              </Button>
            ) : (
              <>
                {wsl ? null : vm.state === 'paused' ? (
                  <Button size="sm" onClick={() => void act('resume')}>
                    <Play />
                    {t('vm.indicator.resume')}
                  </Button>
                ) : (
                  <Button size="sm" onClick={() => void act('pause')}>
                    <Pause />
                    {t('vm.indicator.pause')}
                  </Button>
                )}
                <Button size="sm" variant="danger" onClick={onStop}>
                  <Power />
                  {t('common.stop')}…
                </Button>
              </>
            )}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}

// VMMemory is the VM's memory as one bar: the cap is the whole width, what
// it's been granted a lighter fill, what's in use inside a solid one.
function VMMemory({ vm }: { vm: VMPower }) {
  const t = useT();
  const cap = Math.max(vm.memoryCap, vm.memoryGranted, 1);
  const granted = (vm.memoryGranted / cap) * 100;
  const used = (Math.min(vm.memoryUsed, vm.memoryGranted) / cap) * 100;
  return (
    <div className="grid gap-2">
      <div className="relative h-2 overflow-hidden rounded-full bg-surface-strong">
        <div className="absolute inset-y-0 left-0 rounded-full bg-brand-400/30" style={{ width: `${granted}%` }} />
        <div className="absolute inset-y-0 left-0 rounded-full bg-gradient-to-r from-brand-400 to-sky-400" style={{ width: `${used}%` }} />
      </div>
      <div className="grid grid-cols-3 gap-2 text-[11px]">
        <Legend swatch="bg-gradient-to-r from-brand-400 to-sky-400" label={t('vm.indicator.used')} value={humanBytes(vm.memoryUsed)} />
        <Legend swatch="bg-brand-400/30" label={t('vm.indicator.granted')} value={humanBytes(vm.memoryGranted)} />
        <Legend swatch="bg-surface-strong" label={t('vm.indicator.cap')} value={humanBytes(vm.memoryCap)} />
      </div>
    </div>
  );
}

// WSLMemory is the WSL distro's memory: what's in use of all WSL lets it have,
// which it isn't given up front, so there's no granted share to show.
function WSLMemory({ vm }: { vm: VMPower }) {
  const t = useT();
  const used = vm.memoryCap > 0 ? (Math.min(vm.memoryUsed, vm.memoryCap) / vm.memoryCap) * 100 : 0;
  return (
    <div className="grid gap-2">
      <div className="relative h-2 overflow-hidden rounded-full bg-surface-strong">
        <div className="absolute inset-y-0 left-0 rounded-full bg-gradient-to-r from-brand-400 to-sky-400" style={{ width: `${used}%` }} />
      </div>
      <div className="grid grid-cols-3 gap-2 text-[11px]">
        <Legend swatch="bg-gradient-to-r from-brand-400 to-sky-400" label={t('vm.indicator.used')} value={humanBytes(vm.memoryUsed)} />
        <Legend swatch="bg-surface-strong" label={t('vm.wslPower.limit')} value={humanBytes(vm.memoryCap)} />
      </div>
    </div>
  );
}

function Legend({ swatch, label, value }: { swatch: string; label: string; value: ReactNode }) {
  return (
    <span className="grid gap-0.5">
      <span className="flex items-center gap-1.5 text-muted">
        <span className={cn('size-2 rounded-sm ring-1 ring-inset ring-line-strong', swatch)} />
        {label}
      </span>
      <span className="font-mono tabular-nums text-secondary">{value}</span>
    </span>
  );
}
