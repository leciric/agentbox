import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { toast } from 'sonner';
import { api } from '../../lib/api';
import { t } from '../../lib/i18n';
import { onSessionsSettled } from '../../lib/sessions';
import { errorMessage } from '../../lib/utils';

// SettledToasts says when chat sessions settle into History by themselves,
// their work in (daemon/sessionsettle.go), with Undo: reopening them, as
// History's Reopen does. Several at once, as after an upgrade, are one toast.
export function SettledToasts() {
  const queryClient = useQueryClient();
  useEffect(
    () =>
      onSessionsSettled(({ project, sessions }) => {
        if (sessions.length === 0) return;
        const message = sessions.length === 1 ? t('sessions.settled', { topic: sessions[0].topic }) : t('sessions.settledMany', { count: sessions.length });
        toast(message, {
          duration: 12_000,
          action: {
            label: t('sessions.undo'),
            onClick: () =>
              void Promise.all(sessions.map((s) => api.reopenLeadSession(project, s.id)))
                .catch((err) => toast.error(errorMessage(err)))
                .finally(() => void queryClient.invalidateQueries({ queryKey: ['leadSessions', project] })),
          },
        });
      }),
    [queryClient],
  );
  return null;
}
