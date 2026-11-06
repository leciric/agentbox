import { useQuery } from '@tanstack/react-query';
import { Gauge, LoaderCircle } from 'lucide-react';
import { api } from '../lib/api';
import { useDaemonBusy } from '../lib/daemonBusy';
import { useConnection } from '../lib/events';
import { useT } from '../lib/i18n';
import type { MessageKey } from '../../shared/i18n';
import { Tip } from './ui/tooltip';

// BusyMarks are the top bar's two marks for a machine under load, shown only
// while it is: the daemon slow to answer (lib/daemonBusy.ts), while every
// view keeps showing what it last had, and this computer busy enough that
// agents' heavy commands wait for it to calm down (internal/hostload).
export function BusyMarks() {
  const t = useT();
  const slow = useDaemonBusy();
  const connected = useConnection().state === 'connected';
  const host = useQuery({ queryKey: ['pressure'], queryFn: api.pressure, refetchInterval: 10_000, enabled: connected }).data?.host;
  const why = host?.busy ? hostWhys[host.why ?? ''] : undefined;
  return (
    <>
      {slow && connected && (
        <Tip label={t('shell.busy.daemonTip')}>
          <span className="flex items-center gap-1.5 rounded-full px-2 py-1 text-xs text-muted ring-1 ring-inset ring-line" data-busy="daemon">
            <LoaderCircle className="size-3.5 animate-spin" />
            <span className="hidden sm:inline">{t('shell.busy.daemon')}</span>
          </span>
        </Tip>
      )}
      {host?.busy && (
        <Tip label={t('shell.busy.hostTip', { why: why ? t(why) : t('shell.busy.why.calming') })}>
          <span className="flex items-center gap-1.5 rounded-full bg-amber-400/10 px-2 py-1 text-xs text-amber-200 ring-1 ring-inset ring-amber-400/25" data-busy="host">
            <Gauge className="size-3.5" />
            <span className="hidden sm:inline">{t('shell.busy.host')}</span>
          </span>
        </Tip>
      )}
    </>
  );
}

const hostWhys: Record<string, MessageKey> = {
  memory: 'shell.busy.why.memory',
  cpu: 'shell.busy.why.cpu',
  io: 'shell.busy.why.io',
};
