import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle } from 'lucide-react';
import { useState } from 'react';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { restartVM, useVMPower } from '../lib/vm';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Button } from './ui/button';
import { Notice } from './ui/card';

const phases: Record<string, MessageKey> = {
  stopping: 'settings.restartVM.stopping',
  starting: 'settings.restartVM.starting',
  waiting: 'settings.restartVM.waiting',
};

// RestartVM is what Settings offers where a setting needs AgentBox's VM
// restarted (the GPU for agents on an old VM, or after the Mac's new kernel):
// it stops and starts the VM from the host side, waits for the daemon to
// answer, and refreshes everything the app shows. Nothing outside VM mode.
export function RestartVM() {
  const t = useT();
  const queryClient = useQueryClient();
  const vm = useVMPower().data;
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const running = (agents.data ?? []).filter((a) => a.state === 'running').length;
  const [confirming, setConfirming] = useState(false);
  const [phase, setPhase] = useState('');
  const restart = useMutation({
    mutationFn: () =>
      restartVM(
        setPhase,
        // Settings is the first thing the app needs back from the daemon.
        async () => queryClient.setQueryData(['settings'], await api.settings()),
      ),
    onSuccess: () => void queryClient.invalidateQueries(),
  });
  if (!vm) return null;
  return (
    <div className="mt-2 grid gap-2">
      <p className="text-[13px] text-muted">{t('settings.restartVM.needed')}</p>
      <div>
        <Button size="sm" disabled={restart.isPending} onClick={() => setConfirming(true)}>
          {restart.isPending && <LoaderCircle className="size-3.5 animate-spin" />}
          {restart.isPending && phases[phase] ? t(phases[phase]) : t('settings.restartVM.button')}
        </Button>
      </div>
      {restart.error && <Notice>{t('settings.restartVM.failed', { error: errorMessage(restart.error) })}</Notice>}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('settings.restartVM.confirmTitle')}
        description={t.rich('settings.restartVM.confirm', {
          running,
          b: (c) => <strong className="font-medium text-primary">{c}</strong>,
        })}
        confirmLabel={t('settings.restartVM.button')}
        destructive
        // The restart outlives the dialog: its progress is on the button.
        onConfirm={async () => restart.mutate()}
      />
    </div>
  );
}
