// useAppUpdate runs an update from "Update available" (lib/appUpdate.ts).
import { useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { AppUpdateProgress, AppUpdateResult } from '../../shared/appupdate';
import { api } from './api';
import { failureMessage, updateLabel } from './appUpdate';
import { t } from './i18n';
import { openLatestRelease } from './releaseLink';

export function useAppUpdate() {
  const support = useQuery({ queryKey: ['app-update-support'], queryFn: () => window.agentbox.appUpdate.support(), staleTime: Infinity });
  const [progress, setProgress] = useState<AppUpdateProgress | 'preparing' | null>(null);
  // Progress goes to every window's listeners: an update started in Settings
  // shows in the sidebar too.
  useEffect(() => window.agentbox.appUpdate.onProgress(setProgress), []);

  const releasePage = (pinned: string) => void openLatestRelease(api.latestRelease, window.agentbox.openExternal, pinned);

  // start updates to the channel's latest release, or opens its page when
  // this install can't; pinned is the page of the release the check found.
  const start = async (pinned: string) => {
    if (!support.data?.inPlace) return releasePage(pinned);
    if (progress !== null) return;
    setProgress('preparing');
    const result = await window.agentbox.appUpdate.start().catch(
      (err: unknown): AppUpdateResult => ({ ok: false, code: 'install', detail: err instanceof Error ? err.message : String(err) }),
    );
    // Succeeded: the app restarts, still saying so.
    if (result.ok) return;
    setProgress(null);
    toast.error(t('shell.update.failed'), {
      description: failureMessage(result),
      duration: 15_000,
      action: { label: t('shell.update.releasePage'), onClick: () => releasePage(result.url ?? pinned) },
    });
  };

  return { support: support.data, label: updateLabel(progress), updating: progress !== null, start };
}
