import { useQuery } from '@tanstack/react-query';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { docked } from '../../lib/pip';

// useDesktops is when each of a project's agents last used its desktop, asked
// again every few seconds while something shows it.
export function useDesktops(project: string) {
  return useQuery({ queryKey: ['desktops', project], queryFn: () => api.projectDesktops(project), refetchInterval: 5_000 });
}

// useInUse is the project's agents using their desktops now (lib/pip.ts): the
// ones the right-hand panel's list shows live, a small desktop in their row.
export function useInUse(project: string): T.Agent[] {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const desktops = useDesktops(project);
  return docked(project, agents.data ?? [], desktops.data ?? [], Date.now());
}
