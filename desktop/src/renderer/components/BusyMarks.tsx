import { LoaderCircle } from 'lucide-react';
import { useDaemonBusy } from '../lib/daemonBusy';
import { useConnection } from '../lib/events';
import { useT } from '../lib/i18n';
import { Tip } from './ui/tooltip';

// BusyMarks is the top bar's mark for the daemon slow to answer
// (lib/daemonBusy.ts), shown only while it is, while every view keeps showing
// what it last had. This computer being busy has no mark: nothing waits or
// runs fewer jobs for it (internal/hostload).
export function BusyMarks() {
  const t = useT();
  const slow = useDaemonBusy();
  const connected = useConnection().state === 'connected';
  if (!slow || !connected) return null;
  return (
    <Tip label={t('shell.busy.daemonTip')}>
      <span className="flex items-center gap-1.5 rounded-full px-2 py-1 text-xs text-muted bg-surface-raised" data-busy="daemon">
        <LoaderCircle className="size-3.5 animate-spin" />
        <span className="hidden sm:inline">{t('shell.busy.daemon')}</span>
      </span>
    </Tip>
  );
}
