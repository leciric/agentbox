// What the app shows while its daemon is another version than itself, after
// the app updated: main/daemon.ts restarts that daemon once its jobs are done,
// and until then pages that need the new version's requests would fail with
// the old daemon's bare 404s. This says why, and offers the restart now.
import { useEffect, useState } from 'react';
import type { StaleDaemon } from '../../preload';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';

export function useStaleDaemon(): StaleDaemon | null {
  const [stale, setStale] = useState<StaleDaemon | null>(null);
  useEffect(() => {
    let live = true;
    const off = window.agentbox.onStaleDaemon?.((s) => setStale(s));
    void window.agentbox
      .staleDaemon?.()
      .then((s) => live && setStale(s))
      .catch(() => {});
    return () => {
      live = false;
      off?.();
    };
  }, []);
  return stale;
}

export function StaleDaemonNotice({ stale }: { stale: StaleDaemon }) {
  const t = useT();
  const [restarting, setRestarting] = useState(false);
  const [error, setError] = useState('');
  const restart = async () => {
    setRestarting(true);
    setError('');
    try {
      await window.agentbox.restartStaleDaemon();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setRestarting(false);
    }
  };
  return (
    <Notice tone="warning">
      <div data-stale-daemon={stale.daemon}>
        {stale.jobs > 0
          ? t('shell.app.daemonStale', { daemon: stale.daemon, app: stale.app, count: stale.jobs })
          : t('shell.app.daemonStaleRestarted', { daemon: stale.daemon, app: stale.app })}
      </div>
      {error && <div className="text-[12px] text-secondary">{error}</div>}
      <Button size="sm" variant="secondary" className="mt-2" disabled={restarting} onClick={() => void restart()}>
        {restarting
          ? t('shell.app.daemonStaleRestarting')
          : stale.jobs > 0
            ? t('shell.app.daemonStaleRestartNow', { count: stale.jobs })
            : t('shell.app.daemonStaleRestart')}
      </Button>
    </Notice>
  );
}
