import { useQuery } from '@tanstack/react-query';
import { useMemo, type ReactNode } from 'react';
import { api } from '../../lib/api';
import { pastAgentsKey } from '../../lib/pastAgents';
import { leadOf, openPanelOn, pastRef } from '../../lib/rightPanel';
import { useInUse } from './PipLane';
import { AgentMentions } from './mentionChip';

export function AgentMentionsProvider({ project, children }: { project: string; children: ReactNode }) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const past = useQuery({ queryKey: [...pastAgentsKey, 'mentions', project], queryFn: () => api.archivedAgents({ project, limit: 200 }), staleTime: 60_000 });
  const names = useMemo(
    () => new Set([...(agents.data ?? []).filter((a) => a.project === project).map((a) => a.name), ...(past.data ?? []).map((a) => a.name)]),
    [agents.data, past.data, project],
  );
  const inUse = useInUse(project);
  const liveKey = inUse.map((a) => a.name).join(',');
  const live = useMemo(() => new Set(liveKey ? liveKey.split(',') : []), [liveKey]);
  const value = useMemo(
    () => ({
      project,
      names,
      live,
      // A live agent opens on its desktop; a past one has none, so on what it left.
      onOpen: (name: string) => {
        const ref = `${project}/${name}`;
        const alive = (agents.data ?? []).some((a) => a.ref === ref);
        openPanelOn(leadOf(project), alive ? 'desktop' : 'evidence', alive ? ref : pastRef(ref));
      },
    }),
    [project, names, live, agents.data],
  );
  return <AgentMentions value={value}>{children}</AgentMentions>;
}
