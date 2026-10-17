import { useQuery } from '@tanstack/react-query';
import { LoaderCircle, MonitorOff } from 'lucide-react';
import { useSyncExternalStore } from 'react';
import type * as T from '../../../shared/api';
import { api, browserViewPath } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { useVncView } from '../../lib/useVncView';
import { standIn } from './standIn';

// The window being hidden stops every stream: nobody is watching.
const onVisibility = (l: () => void) => {
  document.addEventListener('visibilitychange', l);
  return () => document.removeEventListener('visibilitychange', l);
};
const useShown = () => useSyncExternalStore(onVisibility, () => document.visibilityState !== 'hidden');

// LiveDesktop is an agent's display, live and only to watch, at 16:10. small
// asks for coarse frames, for a tile.
export function LiveDesktop({ agent, small }: { agent: T.Agent; small?: boolean }) {
  const t = useT();
  const shown = useShown();
  const running = agent.state === 'running';
  const status = useQuery({ queryKey: ['browser', agent.ref], queryFn: () => api.browser(agent.ref), enabled: running && !standIn, refetchInterval: 10_000 });
  const up = !!standIn || (running && status.data?.display === true);
  const { view, connected } = useVncView(browserViewPath(agent.ref), up && shown && !standIn, false, small);
  return (
    <div className="relative aspect-[16/10] w-full overflow-hidden bg-stage" data-pip-live={agent.name}>
      {standIn ? (
        standIn(agent)
      ) : (
        <>
          <div ref={view} className="pointer-events-none absolute inset-0" />
          {!connected && (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-1.5 text-[11px] text-subtle">
              {up || status.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <MonitorOff className="size-4" />}
              {!small && <span>{up || status.isPending ? t('pip.connecting') : t('pip.noDesktop')}</span>}
            </div>
          )}
        </>
      )}
    </div>
  );
}
