import { useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, MonitorCog } from 'lucide-react';
import { toast } from 'sonner';
import type { WSLStatus } from '../../preload';
import { useT } from '../lib/i18n';
import { startMachine, useMachineRun } from '../lib/machineRun';
import { CommandBox, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';

// WSLSetup is what Windows shows while AgentBox's WSL distro isn't there to
// talk to. On Windows the whole of AgentBox runs in that distro, daemon
// included, so until it exists there is no daemon for the app to reach, and no
// Setup page to show: this makes the distro from the app, with `agentbox wsl
// init`, and streams what it prints. The daemon it starts is the app's to
// connect to when it finishes (D94). The run goes on in the background
// (lib/machineRun), as a Mac's VMSetup's does.
export function WSLSetup({ wsl }: { wsl: WSLStatus }) {
  const t = useT();
  const queryClient = useQueryClient();
  const machine = useMachineRun();
  const pending = machine.state === 'running';
  const lines = machine.lines;
  const error = machine.state === 'failed' ? machine.error : undefined;
  const run = () =>
    void startMachine(undefined, () => {
      toast(t('vm.wsl.toast'), { description: t('vm.setup.toastDescription') });
      void queryClient.invalidateQueries();
    });

  // Installing WSL itself takes an administrator and, the first time, a
  // restart: that stays the user's to do.
  const noWSL = wsl.wsl === '';
  // WSL is there but can't start a WSL 2 VM: `agentbox wsl init` says so,
  // before downloading anything (hostwsl.ErrNoVirtualization).
  const noVirtualization = error ? error.includes('Virtual Machine Platform is off') : false;
  // "Not set up" is what this whole card says; anything else (a WSL 1 distro,
  // a missing Linux binary) is worth showing on its own.
  const otherProblem = wsl.problem && !(!wsl.exists && wsl.problem.includes("isn't set up")) ? wsl.problem : '';
  return (
    <Panel className="grid gap-3 p-4" data-wsl-setup={noWSL ? 'wsl' : wsl.exists ? 'repair' : 'create'}>
      <div className="flex items-start gap-3">
        <MonitorCog className="mt-0.5 size-5 shrink-0 text-brand-400" />
        <div className="grid min-w-0 gap-1">
          <h2 className="text-heading font-medium text-primary">{t('vm.wsl.title')}</h2>
          <p className="text-ui text-muted">
            {t('vm.wsl.description', { name: wsl.name, path: `\\\\wsl.localhost\\${wsl.name}` })}
          </p>
        </div>
      </div>
      {noWSL ? (
        <div className="grid gap-2">
          <p className="text-ui text-muted">
            {t('vm.wsl.noWSL')}
          </p>
          <CommandBox command="wsl --install --no-distribution" />
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="primary" disabled={pending} onClick={() => run()}>
            {pending ? <LoaderCircle className="animate-spin" /> : <MonitorCog />}
            {wsl.exists ? t('vm.wsl.finish') : t('vm.wsl.setUp')}
          </Button>
          <span className="text-xs text-subtle">{pending ? t('vm.wsl.takesAWhile') : t('vm.wsl.noPassword')}</span>
        </div>
      )}
      {(lines.length > 0 || pending) && <SetupLog lines={lines} label={t('vm.wsl.log')} />}
      {noVirtualization ? (
        <div className="grid gap-2">
          <Notice>{t('vm.wsl.noVirtualization')}</Notice>
          <CommandBox command="wsl --install --no-distribution" />
          <p className="text-ui text-muted">{t('vm.wsl.noVirtualizationDism')}</p>
          <CommandBox command="dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart" />
          <p className="text-ui text-muted">{t('vm.wsl.noVirtualizationBios')}</p>
        </div>
      ) : (
        error && <Notice>{error}</Notice>
      )}
      {!noWSL && otherProblem && !pending && !error && lines.length === 0 && <Notice tone="warning">{otherProblem}</Notice>}
      {!noWSL && (
        <div className="grid gap-2">
          <p className="text-ui text-muted">{t('vm.wsl.orTerminal')}</p>
          <CommandBox command="agentbox wsl init" />
        </div>
      )}
    </Panel>
  );
}
