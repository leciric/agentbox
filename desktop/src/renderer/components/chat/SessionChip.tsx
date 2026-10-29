import { MessagesSquare } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';

// SessionChip says which session of its project's chat an agent is, the one
// its notices go to: on its row in the rail, once the chat has more than one
// session. An agent no session owns says nothing.
export function SessionChip({ agent, className }: { agent: T.Agent; className?: string }) {
  const t = useT();
  const sessions = useQuery({ queryKey: ['leadSessions', agent.project], queryFn: () => api.leadSessions(agent.project) });
  const list = sessions.data ?? [];
  const own = list.find((s) => s.id === agent.leadSession);
  if (list.length < 2 || !own) return null;
  return (
    <span title={t('sessions.owner', { topic: own.topic })} className={cn('inline-flex min-w-0 max-w-28 shrink items-center gap-1 rounded bg-surface-raised px-1 text-[10px] leading-4 text-subtle', className)} data-session-chip={own.id}>
      <MessagesSquare className="size-2.5 shrink-0" />
      <span className="min-w-0 truncate">{own.topic}</span>
    </span>
  );
}
