// useAppUpdate is "Update available" for the sidebar and Settings: where an
// in-place update stands (main/appupdate.ts), and what clicking it does —
// download the update, restart into it once it's ready, or open the release
// page where the app can't update itself (updateOrOpen).
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { AppUpdateState } from '../../shared/appUpdate';
import { api } from './api';
import { t } from './i18n';
import { updateOrOpen } from './releaseLink';

export function useAppUpdate(available: { version: string; url: string } | undefined) {
  const [state, setState] = useState<AppUpdateState>({ state: 'idle' });
  const [supported, setSupported] = useState(false);
  useEffect(() => {
    void window.agentbox.appUpdate.supported().then(setSupported);
    void window.agentbox.appUpdate.state().then(setState);
    return window.agentbox.appUpdate.onState(setState);
  }, []);

  const start = () => {
    if (!available || state.state === 'downloading' || state.state === 'installing') return;
    if (state.state === 'ready') {
      void window.agentbox.appUpdate.install().then(setState);
      return;
    }
    void updateOrOpen(api.latestRelease, window.agentbox.appUpdate, window.agentbox.openExternal, available, (error) =>
      toast.error(t('appUpdate.failed'), { description: error }),
    );
  };
  return { state, supported, start };
}
