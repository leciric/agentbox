import { useMutation, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, MonitorCog } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { WSLStatus } from '../../preload';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';

// WSLSetup is what Windows shows while AgentBox's WSL distro isn't there to
// talk to. On Windows the whole of AgentBox runs in that distro, daemon
// included, so until it exists there is no daemon for the app to reach, and no
// Setup page to show: this makes the distro from the app, with `agentbox wsl
// init`, and streams what it prints. The daemon it starts is the app's to
// connect to when it finishes (D94).
export function WSLSetup({ wsl }: { wsl: WSLStatus }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.hostSetup.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  const run = useMutation({
    mutationFn: () => window.agentbox.hostSetup.run(),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast(t('vm.wsl.toast'), { description: t('vm.setup.toastDescription') });
      await queryClient.invalidateQueries();
    },
  });

  // Installing WSL itself takes an administrator and, the first time, a
  // restart: that stays the user's to do.
  const noWSL = wsl.wsl === '';
  // "Not set up" is what this whole card says; anything else (a WSL 1 distro,
  // a missing Linux binary) is worth showing on its own.
  const otherProblem = wsl.problem && !(!wsl.exists && wsl.problem.includes("isn't set up")) ? wsl.problem : '';
  return (
    <Panel className="grid gap-3 p-4" data-wsl-setup={noWSL ? 'wsl' : wsl.exists ? 'repair' : 'create'}>
      <div className="flex items-start gap-3">
        <MonitorCog className="mt-0.5 size-5 shrink-0 text-brand-400" />
        <div className="grid min-w-0 gap-1">
          <h2 className="text-[15px] font-medium text-primary">{t('vm.wsl.title')}</h2>
          <p className="text-[13px] text-muted">
            {t('vm.wsl.description', { name: wsl.name, path: `\\\\wsl.localhost\\${wsl.name}` })}
          </p>
        </div>
      </div>
      {noWSL ? (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">
            {t('vm.wsl.noWSL')}
          </p>
          <CommandBox command="wsl --install --no-distribution" />
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="primary" disabled={run.isPending} onClick={() => run.mutate()}>
            {run.isPending ? <LoaderCircle className="animate-spin" /> : <MonitorCog />}
            {wsl.exists ? t('vm.wsl.finish') : t('vm.wsl.setUp')}
          </Button>
          <span className="text-xs text-subtle">{run.isPending ? t('vm.wsl.takesAWhile') : t('vm.wsl.noPassword')}</span>
        </div>
      )}
      {(lines.length > 0 || run.isPending) && <SetupLog lines={lines} label={t('vm.wsl.log')} />}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      {!noWSL && otherProblem && !run.isPending && !run.error && lines.length === 0 && <Notice tone="warning">{otherProblem}</Notice>}
      {!noWSL && (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">{t('vm.wsl.orTerminal')}</p>
          <CommandBox command="agentbox wsl init" />
        </div>
      )}
    </Panel>
  );
}
