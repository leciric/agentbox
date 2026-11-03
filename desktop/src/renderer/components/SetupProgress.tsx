import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CircleCheck, CircleDashed, Clock, Layers, LoaderCircle, MonitorCog, RefreshCw, TriangleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import { api } from '../lib/api';
import { imageState, type ImageState, type MachineState } from '../lib/firstRun';
import { useT } from '../lib/i18n';
import { retryMachine } from '../lib/machineRun';
import { elapsed } from '../lib/machineStatus';
import { useNow } from '../lib/useNow';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// The work Setup leaves running in the background, shown small: the machine
// being made (lib/machineRun) and the base image downloading in it (the
// daemon's fetchFirstImage). The first run keeps both under its rail through
// every step; after it, the machine has the top bar's MachineChip and the
// image its ImageBuildChip.

// BackgroundWork is the first run's: a row for each, with a retry on the one
// that failed. onMachineDetails opens the machine's step, where its card says
// what to do about a failure retrying won't fix.
export function BackgroundWork({ machine, onMachineDetails }: { machine: MachineState; onMachineDetails?: () => void }) {
  const t = useT();
  return (
    <section aria-label={t('firstRun.bg.title')} className="mt-4 grid gap-2.5 border-t border-line pt-3" data-first-run-background>
      <h3 className="px-2 text-caption font-medium uppercase tracking-wide text-faint">{t('firstRun.bg.title')}</h3>
      <MachineRow machine={machine} onDetails={onMachineDetails} />
      <ImageRow connected={machine.kind === 'ready'} />
    </section>
  );
}

// Row is one of them: its name and a short status on the first line, then
// what it's doing, or why it failed, and the retry.
function Row({ icon, title, status, detail, tone, action, data }: { icon: ReactNode; title: string; status: string; detail?: string; tone?: 'bad'; action?: ReactNode; data: Record<string, string> }) {
  return (
    <div className="grid min-w-0 gap-1 px-2" {...data}>
      <div className="flex min-w-0 items-center gap-2 text-ui">
        <span className="shrink-0">{icon}</span>
        <span className="shrink-0 whitespace-nowrap font-medium text-secondary">{title}</span>
        <span className={cn('ml-auto min-w-0 truncate text-caption tabular-nums', tone === 'bad' ? 'text-rose-300' : 'text-subtle')} title={status}>
          {status}
        </span>
      </div>
      {detail && (
        <p className={cn('line-clamp-3 break-words pl-6 text-caption', tone === 'bad' ? 'text-rose-200/80' : 'text-subtle')} title={detail}>
          {detail}
        </p>
      )}
      {action && <div className="flex flex-wrap gap-1.5 pl-6">{action}</div>}
    </div>
  );
}

function MachineRow({ machine, onDetails }: { machine: MachineState; onDetails?: () => void }) {
  const t = useT();
  const now = useNow(1_000);
  const title = t('firstRun.bg.machine');
  const data = { 'data-background-machine': machine.kind };
  if (machine.kind === 'ready')
    return <Row data={data} icon={<CircleCheck className="size-4 text-emerald-400" />} title={title} status={t('firstRun.bg.ready')} />;
  if (machine.kind === 'idle')
    return <Row data={data} icon={<CircleDashed className="size-4 text-subtle" />} title={title} status={t('firstRun.bg.notStarted')} />;
  if (machine.kind === 'failed')
    return (
      <Row
        data={data}
        icon={<TriangleAlert className="size-4 text-rose-300" />}
        title={title}
        status={t('firstRun.bg.failed')}
        tone="bad"
        detail={machine.error}
        action={<MachineActions onDetails={onDetails} />}
      />
    );
  return (
    <Row
      data={data}
      icon={<LoaderCircle className="size-4 animate-spin text-sky-300" />}
      title={title}
      status={machine.since ? elapsed(now - machine.since) : t('firstRun.bg.gettingReady')}
      detail={machine.line || t('firstRun.bg.gettingReady')}
    />
  );
}

function MachineActions({ onDetails }: { onDetails?: () => void }) {
  const t = useT();
  return (
    <>
      <Button size="sm" variant="secondary" onClick={() => void retryMachine()} data-retry-machine>
        <RefreshCw />
        {t('firstRun.bg.retry')}
      </Button>
      {onDetails && (
        <Button size="sm" variant="ghost" onClick={onDetails}>
          {t('firstRun.bg.details')}
        </Button>
      )}
    </>
  );
}

function ImageRow({ connected }: { connected: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const image = useQuery({
    queryKey: ['image'],
    queryFn: api.image,
    enabled: connected,
    refetchInterval: (query) => (query.state.data?.build === 'building' ? 2_000 : 10_000),
    retry: false,
  });
  const retry = useMutation({
    mutationFn: () => api.buildImage(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['image'] });
      void queryClient.invalidateQueries({ queryKey: ['setup'] });
    },
  });
  const state: ImageState = imageState(connected, image.data);
  const title = t('firstRun.bg.image');
  const data = { 'data-background-image': state.kind };
  switch (state.kind) {
    case 'ready':
      return <Row data={data} icon={<CircleCheck className="size-4 text-emerald-400" />} title={title} status={t('firstRun.bg.ready')} />;
    case 'waiting':
      return <Row data={data} icon={<Clock className="size-4 text-subtle" />} title={title} status={t('firstRun.bg.later')} detail={t('firstRun.bg.imageWaiting')} />;
    case 'missing':
      return <Row data={data} icon={<CircleDashed className="size-4 text-subtle" />} title={title} status={t('firstRun.bg.later')} detail={t('firstRun.bg.imageMissing')} />;
    case 'running':
      return (
        <Row
          data={data}
          icon={<LoaderCircle className="size-4 animate-spin text-sky-300" />}
          title={title}
          status={t('firstRun.bg.downloading')}
          detail={state.step}
        />
      );
    case 'failed':
      return (
        <Row
          data={data}
          icon={<TriangleAlert className="size-4 text-rose-300" />}
          title={title}
          status={t('firstRun.bg.failed')}
          tone="bad"
          detail={retry.error ? errorMessage(retry.error) : state.error}
          action={
            <Button size="sm" variant="secondary" disabled={retry.isPending} onClick={() => retry.mutate()} data-retry-image>
              {retry.isPending ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
              {t('firstRun.bg.retry')}
            </Button>
          }
        />
      );
  }
}

// MachineWait is a step that asks the daemon, reached before it answers: it
// says it waits for the machine, with how that's going, and the first run
// goes on by itself once the daemon answers. A failure has its retry here too.
export function MachineWait({ machine, onDetails }: { machine: MachineState; onDetails: () => void }) {
  const t = useT();
  const now = useNow(1_000);
  if (machine.kind === 'failed')
    return (
      <div className="grid gap-3" data-machine-wait="failed">
        <Notice>
          <div>{t('firstRun.wait.failed')}</div>
          {machine.error && <div className="mt-1 break-words font-mono text-caption">{machine.error}</div>}
        </Notice>
        <div className="flex flex-wrap gap-2">
          <MachineActions onDetails={onDetails} />
        </div>
      </div>
    );
  return (
    <div className="grid gap-2" data-machine-wait="running">
      <p className="flex items-center gap-2 text-ui text-secondary">
        <LoaderCircle className="size-4 shrink-0 animate-spin text-sky-300" />
        {t('firstRun.wait.title')}
        {machine.kind === 'running' && machine.since && <span className="ml-auto tabular-nums text-caption text-subtle">{elapsed(now - machine.since)}</span>}
      </p>
      <p className="text-ui text-muted">{t('firstRun.wait.body')}</p>
      {machine.kind === 'running' && machine.line && <p className="truncate font-mono text-caption text-subtle" title={machine.line}>{machine.line}</p>}
    </div>
  );
}

// MachineChip is the machine being made, in the top bar, once the first run
// is over (or was left for later) and it still is: a sky chip while it runs,
// a rose one with a retry once it failed, nothing otherwise.
export function MachineChip({ machine }: { machine: MachineState }) {
  const t = useT();
  const now = useNow(1_000);
  if (machine.kind !== 'running' && machine.kind !== 'failed') return null;
  const failed = machine.kind === 'failed';
  const label = failed ? t('shell.machineChip.failed') : t('shell.machineChip.running');
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex min-w-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset transition',
            failed ? 'bg-rose-400/10 text-rose-200 ring-rose-400/30 hover:bg-rose-400/15' : 'bg-sky-400/10 text-sky-200 ring-sky-400/25 hover:bg-sky-400/15',
          )}
          aria-label={label}
          data-machine-chip={machine.kind}
        >
          {failed ? <TriangleAlert className="size-3.5 shrink-0" /> : <LoaderCircle className="size-3.5 shrink-0 animate-spin" />}
          <span className="hidden sm:inline">{label}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[26rem] max-w-[calc(100vw-2rem)]">
        <div className="grid gap-2">
          <span className={cn('flex items-center gap-1.5 text-ui font-medium', failed ? 'text-rose-200' : 'text-sky-200')}>
            <MonitorCog className="size-3.5" />
            {label}
            {!failed && machine.since && <span className="ml-auto tabular-nums text-caption text-subtle">{elapsed(now - machine.since)}</span>}
          </span>
          <span className="text-caption leading-relaxed text-secondary">{failed ? t('shell.machineChip.failedBody') : t('shell.machineChip.runningBody')}</span>
          {failed
            ? machine.error && <span className="break-words font-mono text-caption text-rose-200/80">{machine.error}</span>
            : machine.line && <span className="truncate font-mono text-caption text-muted" title={machine.line}>{machine.line}</span>}
          {failed && (
            <div className="flex justify-end border-t border-line pt-2.5">
              <Button size="sm" variant="primary" onClick={() => void retryMachine()}>
                <RefreshCw />
                {t('firstRun.bg.retry')}
              </Button>
            </div>
          )}
          {!failed && (
            <span className="flex items-center gap-1.5 text-caption text-subtle">
              <Layers className="size-3.5" />
              {t('shell.machineChip.imageAfter')}
            </span>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
