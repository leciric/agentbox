import { useMutation, useQueryClient } from '@tanstack/react-query';
import { FlaskConical, LoaderCircle, MonitorCog } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMStatus } from '../../preload';
import { cn, errorMessage } from '../lib/utils';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';

type Driver = 'lima' | 'vz';

// drivers are what can make the VM: Lima, the default, or Apple's
// Virtualization framework run by AgentBox itself (`agentbox vm init --driver
// vz`), which is experimental until it has been tried on a real Mac.
const drivers: { driver: Driver; label: string; hint: string }[] = [
  { driver: 'lima', label: 'Lima', hint: 'Recommended. Needs Lima from Homebrew.' },
  { driver: 'vz', label: 'Apple Virtualization, without Lima', hint: 'AgentBox runs the VM itself. Not yet tried on a real Mac.' },
];

// VMSetup is what a Mac shows while AgentBox's Linux VM isn't there to talk to.
// On a Mac the whole of AgentBox runs in that VM, daemon included, so until it
// exists there is no daemon for the app to reach, and no Setup page to show:
// this makes the VM from the app, with `agentbox vm init`, and streams what it
// prints. The daemon it starts is the app's to connect to when it finishes.
// A VM that exists keeps the driver it was made with; a new one is Lima's
// unless the experimental vz driver is chosen.
export function VMSetup({ vm }: { vm: VMStatus }) {
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  const [chosen, setChosen] = useState<Driver>('lima');
  useEffect(() => window.agentbox.hostSetup.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);
  const driver: Driver = vm.driver === 'vz' ? 'vz' : vm.exists ? 'lima' : chosen;
  const vz = driver === 'vz';

  const run = useMutation({
    mutationFn: () => window.agentbox.hostSetup.run(vz ? { driver: 'vz' } : undefined),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast("AgentBox's VM is ready", { description: 'Next, the Setup page builds the base image your agents are copied from.' });
      await queryClient.invalidateQueries();
    },
  });

  const noLima = vm.lima === '' && !vz;
  // "Not set up" is what this whole card says; anything else (a broken VM, a
  // missing Linux binary) is worth showing on its own.
  const otherProblem = vm.problem && !(!vm.exists && vm.problem.includes("isn't set up")) ? vm.problem : '';
  const command = vz ? 'agentbox vm init --driver vz' : 'agentbox vm init';
  return (
    <Panel className="grid gap-3 p-4" data-vm-setup={noLima ? 'lima' : vm.exists ? 'repair' : 'create'} data-vm-driver={driver}>
      <div className="flex items-start gap-3">
        <MonitorCog className="mt-0.5 size-5 shrink-0 text-brand-400" />
        <div className="grid min-w-0 gap-1">
          <h2 className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-primary">
            Set up AgentBox's Linux VM
            {vz && (
              <Badge variant="warning">
                <FlaskConical />
                Experimental
              </Badge>
            )}
          </h2>
          <p className="text-[13px] text-muted">
            On a Mac, AgentBox runs in a Linux VM: the daemon, Incus and every agent live there, and your home folder is shared with it at the same
            path, so your projects and the agents' worktrees stay where your editor can open them. The first setup downloads Debian and installs
            Incus, which takes a few minutes.
          </p>
        </div>
      </div>
      {!vm.exists && (
        <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="What runs the VM">
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
                {d.label}
                {d.driver === 'vz' && <Badge variant="warning">Experimental</Badge>}
              </span>
              <span className="text-[11px] text-subtle">{d.hint}</span>
            </button>
          ))}
        </div>
      )}
      {vz && (
        <Notice tone="warning">
          The vz driver is experimental: nobody has run it on a real Mac yet. Your VM's memory is its whole cap, and the Mac may not get back what
          it gives up. To go back, agentbox vm delete --yes removes it, and every agent in it; your projects and worktrees stay.
        </Notice>
      )}
      {noLima ? (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">The VM is made with Lima, which isn't installed. Install it with Homebrew, then come back:</p>
          <CommandBox command="brew install lima" />
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="primary" disabled={run.isPending} onClick={() => run.mutate()}>
            {run.isPending ? <LoaderCircle className="animate-spin" /> : <MonitorCog />}
            {vm.exists ? 'Finish setting up the VM' : 'Set up the VM'}
          </Button>
          <span className="text-xs text-subtle">{run.isPending ? 'This takes a few minutes the first time.' : 'No password needed'}</span>
        </div>
      )}
      {(lines.length > 0 || run.isPending) && <SetupLog lines={lines} label="VM setup log" />}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      {!noLima && otherProblem && !run.isPending && !run.error && lines.length === 0 && <Notice tone="warning">{otherProblem}</Notice>}
      {!noLima && (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">Or in a terminal:</p>
          <CommandBox command={command} />
        </div>
      )}
    </Panel>
  );
}
