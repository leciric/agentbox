import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Download, FlaskConical, LoaderCircle, MonitorCog } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMStatus } from '../../preload';
import { useT, type MessageKey } from '../lib/i18n';
import { krunkitPlan, type KrunkitPlan } from '../lib/krunkit';
import { cn, errorMessage } from '../lib/utils';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';

type Driver = 'lima' | 'vz';

// drivers are what can make the VM: Lima, the default, or Apple's
// Virtualization framework run by AgentBox itself (`agentbox vm init --driver
// vz`), which is experimental until it has been tried on a real Mac.
const drivers: { driver: Driver; label: MessageKey | null; hint: MessageKey }[] = [
  { driver: 'lima', label: null, hint: 'vm.setup.limaHint' },
  { driver: 'vz', label: 'vm.setup.vzLabel', hint: 'vm.setup.vzHint' },
];

// VMSetup is what a Mac shows while AgentBox's Linux VM isn't there to talk to.
// On a Mac the whole of AgentBox runs in that VM, daemon included, so until it
// exists there is no daemon for the app to reach, and no Setup page to show:
// this makes the VM from the app, with `agentbox vm init`, and streams what it
// prints. The daemon it starts is the app's to connect to when it finishes.
// A VM that exists keeps the driver it was made with; a new one is Lima's
// unless the experimental vz driver is chosen. When krunkit is missing and
// Homebrew can install it, the main button installs it first, so the VM is
// made with krunkit; setting up without it stays a button away.
export function VMSetup({ vm }: { vm: VMStatus }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  const [chosen, setChosen] = useState<Driver>('lima');
  useEffect(() => window.agentbox.hostSetup.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);
  const driver: Driver = vm.driver === 'vz' ? 'vz' : vm.exists ? 'lima' : chosen;
  const vz = driver === 'vz';

  // What Lima's new VM can do about krunkit, which gives agents a GPU.
  const krunkit = !vm.exists && !vz ? krunkitPlan(vm.krunkit) : null;
  const brew = krunkit?.kind === 'brew' ? krunkit : null;
  const run = useMutation({
    mutationFn: (withKrunkit: boolean) =>
      window.agentbox.hostSetup.run(vz ? { driver: 'vz' } : withKrunkit ? { krunkit: true } : undefined),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast(t('vm.setup.toast'), { description: t('vm.setup.toastDescription') });
      await queryClient.invalidateQueries();
    },
  });

  const noLima = vm.lima === '' && !vz;
  // "Not set up" is what this whole card says; anything else (a broken VM, a
  // missing Linux binary) is worth showing on its own.
  const otherProblem = vm.problem && !(!vm.exists && vm.problem.includes("isn't set up")) ? vm.problem : '';
  const command = vz ? 'agentbox vm init --driver vz' : brew ? 'agentbox vm krunkit && agentbox vm init' : 'agentbox vm init';
  return (
    <Panel className="grid gap-3 p-4" data-vm-setup={noLima ? 'lima' : vm.exists ? 'repair' : 'create'} data-vm-driver={driver}>
      <div className="flex items-start gap-3">
        <MonitorCog className="mt-0.5 size-5 shrink-0 text-brand-400" />
        <div className="grid min-w-0 gap-1">
          <h2 className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-primary">
            {t('vm.setup.title')}
            {vz && (
              <Badge variant="warning">
                <FlaskConical />
                {t('vm.setup.experimental')}
              </Badge>
            )}
          </h2>
          <p className="text-[13px] text-muted">
            {t('vm.setup.description')}
          </p>
        </div>
      </div>
      {!vm.exists && (
        <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label={t('vm.setup.whatRuns')}>
          {drivers.map((d) => (
            <button
              key={d.driver}
              type="button"
              role="radio"
              aria-checked={chosen === d.driver}
              data-driver={d.driver}
              disabled={run.isPending}
              onClick={() => setChosen(d.driver)}
              className={cn(
                'grid gap-1 rounded-xl border border-line bg-surface-faint px-3 py-2.5 text-left transition hover:border-line-vivid hover:bg-surface disabled:opacity-60',
                chosen === d.driver && 'border-brand-400/50 bg-brand-500/10 shadow-[0_0_0_3px_rgb(139_92_246/0.12)]',
              )}
            >
              <span className="flex flex-wrap items-center gap-2 text-[13px] font-medium text-primary">
                {d.label ? t(d.label) : 'Lima'}
                {d.driver === 'vz' && <Badge variant="warning">{t('vm.setup.experimental')}</Badge>}
              </span>
              <span className="text-[11px] text-subtle">{t(d.hint)}</span>
            </button>
          ))}
        </div>
      )}
      {vz && (
        <Notice tone="warning">
          {t('vm.setup.vzNotice')}
        </Notice>
      )}
      {noLima ? (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">{t('vm.setup.noLima')}</p>
          <CommandBox command="brew install lima" />
        </div>
      ) : (
        <>
          {krunkit && <KrunkitNote plan={krunkit} />}
          <div className="flex flex-wrap items-center gap-2">
            {brew ? (
              <>
                <Button variant="primary" data-install-krunkit disabled={run.isPending} onClick={() => run.mutate(true)}>
                  {run.isPending && run.variables ? <LoaderCircle className="animate-spin" /> : <Download />}
                  {brew.missing === 'driver' ? t('vm.setup.upgradeLima') : t('vm.setup.installKrunkit')}
                </Button>
                <Button variant="ghost" disabled={run.isPending} onClick={() => run.mutate(false)}>
                  {run.isPending && !run.variables && <LoaderCircle className="animate-spin" />}
                  {t('vm.setup.withoutKrunkit')}
                </Button>
              </>
            ) : (
              <Button variant="primary" disabled={run.isPending} onClick={() => run.mutate(false)}>
                {run.isPending ? <LoaderCircle className="animate-spin" /> : <MonitorCog />}
                {vm.exists ? t('vm.setup.finish') : t('vm.setup.setUp')}
              </Button>
            )}
            <span className="text-xs text-subtle">{run.isPending ? t('vm.setup.takesAWhile') : t('vm.setup.noPassword')}</span>
          </div>
        </>
      )}
      {(lines.length > 0 || run.isPending) && <SetupLog lines={lines} label={t('vm.setup.log')} />}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      {!noLima && otherProblem && !run.isPending && !run.error && lines.length === 0 && <Notice tone="warning">{otherProblem}</Notice>}
      {!noLima && (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">{t('vm.setup.orTerminal')}</p>
          <CommandBox command={command} />
        </div>
      )}
    </Panel>
  );
}

// KrunkitNote says whether the VM about to be made has a GPU for its agents
// and gives memory back to the Mac: made with krunkit, it does both; made with
// Apple's Virtualization framework, Lima's default, it has no GPU and keeps
// whatever it has used until it stops. When Homebrew can install what's
// missing, it shows what Homebrew will run; otherwise how to install it.
function KrunkitNote({ plan }: { plan: KrunkitPlan }) {
  const t = useT();
  if (plan.kind === 'ready')
    return (
      <p className="text-[13px] text-muted" data-krunkit="available">
        {t('vm.setup.krunkitAvailable')}
      </p>
    );
  if (plan.kind === 'brew')
    return (
      <div className="grid gap-2" data-krunkit={`brew-${plan.missing}`}>
        <p className="text-[13px] text-muted">
          {plan.missing === 'driver' ? t('vm.setup.krunkitDriverBrew') : t('vm.setup.krunkitBrew')}
        </p>
        <p className="text-xs text-subtle">{t('vm.setup.brewRuns')}</p>
        <CommandBox command={plan.command} />
      </div>
    );
  if (plan.missing === 'driver')
    return (
      <p className="text-[13px] text-muted" data-krunkit="driver">
        {t('vm.setup.krunkitDriver')}
      </p>
    );
  return (
    <div className="grid gap-2" data-krunkit="missing">
      <p className="text-[13px] text-muted">
        {t('vm.setup.krunkitMissing')}
      </p>
      <CommandBox command={plan.command} />
    </div>
  );
}
