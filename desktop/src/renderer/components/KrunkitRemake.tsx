import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Download, LoaderCircle, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMStatus } from '../../preload';
import { useT } from '../lib/i18n';
import { krunkitPlan } from '../lib/krunkit';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice } from './ui/card';

// KrunkitRemake is GPU for agents' way to one on a Mac whose VM was made with
// Apple's Virtualization framework, which gives agents none: the VM made
// again with krunkit (`agentbox vm krunkit --remake --yes`), after Homebrew
// installs krunkit, or a Lima with its driver, when they're missing. Remaking
// deletes the VM, agents' machines and AgentBox's state with it, so it asks
// first. Without Homebrew it shows the commands instead.
export function KrunkitRemake({ vm }: { vm: VMStatus }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.vm.onKrunkitOutput((text) => setLines((prev) => appendOutput(prev, text))), []);
  const remake = useMutation({
    mutationFn: () => window.agentbox.vm.remakeWithKrunkit(),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast(t('settings.agentGPU.krunkit.done'), { description: t('settings.agentGPU.krunkit.doneDescription') });
      await queryClient.invalidateQueries();
    },
  });
  const plan = krunkitPlan(vm.krunkit);
  if (!plan) return null;
  const manual = plan.kind === 'manual';
  const label =
    plan.kind === 'ready'
      ? t('settings.agentGPU.krunkit.remake')
      : plan.missing === 'driver'
        ? t('settings.agentGPU.krunkit.upgrade')
        : t('settings.agentGPU.krunkit.install');
  return (
    <div className="grid gap-2" data-krunkit-remake={plan.kind}>
      <p className="text-[13px] text-muted">{t('settings.agentGPU.krunkit.intro')}</p>
      {manual ? (
        <>
          <p className="text-[13px] text-muted">
            {plan.missing === 'driver' ? t('settings.agentGPU.krunkit.manualDriver') : t('settings.agentGPU.krunkit.manual')}
          </p>
          <CommandBox command={plan.command ? `${plan.command} && agentbox vm krunkit --remake --yes` : 'agentbox vm krunkit --remake --yes'} />
        </>
      ) : (
        <>
          {plan.kind === 'brew' && (
            <>
              <p className="text-xs text-subtle">{t('settings.agentGPU.krunkit.brewRuns')}</p>
              <CommandBox command={plan.command} />
            </>
          )}
          <Notice tone="warning">{t('settings.agentGPU.krunkit.warning')}</Notice>
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="destructive" data-krunkit-remake-button disabled={remake.isPending} onClick={() => setConfirming(true)}>
              {remake.isPending ? <LoaderCircle className="animate-spin" /> : plan.kind === 'ready' ? <RefreshCw /> : <Download />}
              {label}
            </Button>
            {remake.isPending && <span className="text-xs text-subtle">{t('settings.agentGPU.krunkit.running')}</span>}
          </div>
        </>
      )}
      {(lines.length > 0 || remake.isPending) && <SetupLog lines={lines} label={t('settings.agentGPU.krunkit.log')} />}
      {remake.error && <Notice>{errorMessage(remake.error)}</Notice>}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('settings.agentGPU.krunkit.confirmTitle')}
        description={t('settings.agentGPU.krunkit.confirmDescription')}
        confirmLabel={t('settings.agentGPU.krunkit.confirm')}
        destructive
        onConfirm={async () => {
          // The remake outlasts the dialog: its progress is the log above.
          remake.mutate();
        }}
      />
    </div>
  );
}
