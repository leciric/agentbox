import { Snowflake } from 'lucide-react';
import type * as T from '../../shared/api';
import { frozenTip } from '../lib/agentStatus';
import { useT } from '../lib/i18n';
import { cn } from '../lib/utils';

// FrozenBadge is "Frozen" on an agent's row while its machine is paused for
// memory or idle reclaim has frozen its processes; its tooltip says which,
// and that it resumes by itself. Nothing when it isn't frozen.
export function FrozenBadge({ agent, className }: { agent: Pick<T.Agent, 'state' | 'chat' | 'memory' | 'reclaimed'>; className?: string }) {
  const t = useT();
  const tip = frozenTip(agent);
  if (!tip) return null;
  return (
    <span
      title={tip}
      className={cn('inline-flex shrink-0 items-center gap-1 rounded bg-sky-400/15 px-1 text-[10.5px] font-medium leading-4 text-sky-300', className)}
      data-frozen
    >
      <Snowflake className="size-2.5" aria-hidden />
      {t('agent.frozen')}
    </span>
  );
}
