import { useMutation, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, MonitorCog } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMStatus } from '../../preload';
import { errorMessage } from '../lib/utils';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';

// VMSetup is what a Mac shows while AgentBox's Linux VM isn't there to talk to.
// On a Mac the whole of AgentBox runs in that VM, daemon included, so until it
// exists there is no daemon for the app to reach, and no Setup page to show:
// this makes the VM from the app, with `agentbox vm init`, and streams what it
// prints. The daemon it starts is the app's to connect to when it finishes.
export function VMSetup({ vm }: { vm: VMStatus }) {
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.hostSetup.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  const run = useMutation({
    mutationFn: () => window.agentbox.hostSetup.run(),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast("AgentBox's VM is ready", { description: 'Next, the Setup page builds the base image your agents are copied from.' });
      await queryClient.invalidateQueries();
    },
  });

  const noLima = vm.lima === '';
  // "Not set up" is what this whole card says; anything else (a broken VM, a
  // missing Linux binary) is worth showing on its own.
  const otherProblem = vm.problem && !(!vm.exists && vm.problem.includes("isn't set up")) ? vm.problem : '';
  return (
    <Panel className="grid gap-3 p-4" data-vm-setup={noLima ? 'lima' : vm.exists ? 'repair' : 'create'}>
      <div className="flex items-start gap-3">
        <MonitorCog className="mt-0.5 size-5 shrink-0 text-brand-400" />
        <div className="grid min-w-0 gap-1">
          <h2 className="text-[15px] font-medium text-primary">Set up AgentBox's Linux VM</h2>
          <p className="text-[13px] text-muted">
            On a Mac, AgentBox runs in a Linux VM made with Lima: the daemon, Incus and every agent live there, and your home folder is shared with
            it at the same path, so your projects and the agents' worktrees stay where your editor can open them. The first setup downloads Debian and
            installs Incus, which takes a few minutes.
          </p>
        </div>
      </div>
      {noLima ? (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">The VM is made with Lima, which isn't installed. Install it with Homebrew, then come back:</p>
          <CommandBox command="brew install lima" />
        </div>
      ) : (
        <>
          {!vm.exists && vm.krunkit && <KrunkitNote check={vm.krunkit} />}
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" disabled={run.isPending} onClick={() => run.mutate()}>
              {run.isPending ? <LoaderCircle className="animate-spin" /> : <MonitorCog />}
              {vm.exists ? 'Finish setting up the VM' : 'Set up the VM'}
            </Button>
            <span className="text-xs text-subtle">{run.isPending ? 'This takes a few minutes the first time.' : 'No password needed'}</span>
          </div>
        </>
      )}
      {(lines.length > 0 || run.isPending) && <SetupLog lines={lines} label="VM setup log" />}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      {!noLima && otherProblem && !run.isPending && !run.error && lines.length === 0 && <Notice tone="warning">{otherProblem}</Notice>}
      {!noLima && (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">Or in a terminal:</p>
          <CommandBox command="agentbox vm init" />
        </div>
      )}
    </Panel>
  );
}

// KrunkitNote says whether the VM about to be made gives memory back to the
// Mac. Made with krunkit, it hands back what its agents stop using; made with
// Apple's Virtualization framework, Lima's default, it keeps whatever it has
// used until it stops. Setup doesn't install krunkit itself: it says how.
function KrunkitNote({ check }: { check: NonNullable<VMStatus['krunkit']> }) {
  if (check.available)
    return (
      <p className="text-[13px] text-muted" data-krunkit="available">
        krunkit is installed, so the VM is made with it and gives the memory its agents stop using back to your Mac.
      </p>
    );
  if (check.missing === 'driver')
    return (
      <p className="text-[13px] text-muted" data-krunkit="driver">
        Your Lima has no krunkit driver (lima-driver-krunkit), so the VM is made with Apple's Virtualization framework and keeps the memory it
        has used until it stops.
      </p>
    );
  return (
    <div className="grid gap-2" data-krunkit="missing">
      <p className="text-[13px] text-muted">
        Install krunkit first and the VM gives the memory its agents stop using back to your Mac. Without it, the VM is made with Apple's
        Virtualization framework and keeps what it has used until it stops:
      </p>
      <CommandBox command="brew tap slp/krun && brew trust slp/krun && brew install krunkit" />
    </div>
  );
}
