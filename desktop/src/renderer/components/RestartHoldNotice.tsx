// What AgentBox didn't carry on by itself as it started, after it stopped
// unexpectedly (a crash, the VM or the computer going down with it running)
// or restarted too many times in a row while agents worked: those agents On
// hold and those chats' turns wait for you (T.RestartHold), and Resume all
// carries them on one at a time, as the VM's memory allows
// (internal/daemon/restart.go).
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';

export function useRestartHold(): T.RestartHold | undefined {
  // EventRestartHold keeps it current (lib/events.ts); the poll is for a
  // stream that dropped.
  return useQuery({ queryKey: ['restartHold'], queryFn: api.restartHold, refetchInterval: 60_000, retry: false }).data;
}

export function RestartHoldNotice() {
  const t = useT();
  const hold = useRestartHold();
  const queryClient = useQueryClient();
  const resume = useMutation({
    mutationFn: api.resumeAll,
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ['restartHold'] }),
  });
  const agents = hold?.agents?.length ?? 0;
  const chats = hold?.chats?.length ?? 0;
  if (!hold || agents + chats === 0) return null;
  const waiting =
    agents > 0 && chats > 0
      ? t('shell.app.restartHoldBoth', { agents, chats })
      : agents > 0
        ? t('shell.app.restartHoldAgents', { count: agents })
        : t('shell.app.restartHoldChats', { count: chats });
  return (
    <div className="px-4 pt-4 md:px-6">
      <Notice tone="warning">
        <div data-restart-hold={hold.unexpected ? 'unexpected' : 'too-often'} className="font-medium">
          {hold.unexpected ? t('shell.app.restartHoldUnexpected') : t('shell.app.restartHoldTooOften')}
        </div>
        <div className="text-caption text-secondary">{waiting}</div>
        {hold.why && <div className="text-caption text-secondary">{t('shell.app.restartHoldWhy', { why: hold.why })}</div>}
        {resume.error && <div className="text-caption text-secondary">{errorMessage(resume.error)}</div>}
        <Button size="sm" variant="secondary" className="mt-2" disabled={resume.isPending} onClick={() => resume.mutate()}>
          {resume.isPending ? t('shell.app.restartHoldResuming') : t('shell.app.restartHoldResumeAll')}
        </Button>
      </Notice>
    </div>
  );
}
