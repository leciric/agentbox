// What the app shows while it starts the daemon, instead of the last attempt's
// error: how long it has taken, and what `agentbox daemon start` last said
// it's doing. A first start after an update takes minutes on Windows (#433),
// and an error banner for a start still under way read as a broken install.
import { useEffect, useState } from 'react';
import type { ConnectionState } from '../../preload';
import { useT } from '../lib/i18n';
import { elapsed } from '../lib/machineStatus';
import { Notice } from './ui/card';

export function StartingNotice({ starting, windows }: { starting: NonNullable<ConnectionState['starting']>; windows: boolean }) {
  const t = useT();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const took = now - starting.since;
  return (
    <Notice tone="info">
      <div className="tabular-nums">{t('shell.app.daemonStarting', { elapsed: elapsed(took) })}</div>
      {starting.step && <div className="truncate font-mono text-[12px] text-secondary">{starting.step}</div>}
      {windows && took > 20_000 && <div className="text-[12px] text-secondary">{t('shell.app.daemonStartingSlow')}</div>}
    </Notice>
  );
}
