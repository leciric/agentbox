// Keeps the computer from idle-sleeping while agents work: on a Mac, sleep
// pauses the VM and freezes them mid-turn. Busy is worked out here from the
// state the daemon already sends; the main process only holds the blocker.
import { useQuery, useQueries } from '@tanstack/react-query';
import { useEffect } from 'react';
import * as T from '../../shared/api.ts';
import { isAsking } from './agentStatus.ts';
import { api } from './api.ts';
import { useConnection } from './events.ts';
import { useGrace } from './grace.ts';

// How long after the last sign of work the blocker is kept, so the gap
// between one turn and the next doesn't release and retake it.
export const releaseMs = 30_000;

// busy is whether anything is at work: an agent mid-turn, a lead mid-turn
// (leads maps a project, or T.HomeProject, to its chat state) or a job
// running. A stalled turn (the daemon found no progress on it) is stuck, not
// working, and an agent waiting on a permission or on a question to the user
// (asking, by ref) doesn't count: that can wait forever.
export function busy(
  agents: readonly Pick<T.Agent, 'ref' | 'state' | 'chat' | 'stalledSince'>[],
  leads: ReadonlyMap<string, string | undefined>,
  jobs: readonly Pick<T.Job, 'status'>[],
  asking: ReadonlySet<string> = new Set(),
): boolean {
  if (jobs.some((j) => j.status === T.JobRunning)) return true;
  for (const chat of leads.values()) if (chat === 'running') return true;
  return agents.some((a) => a.state === 'running' && a.chat === 'running' && !a.stalledSince && !asking.has(a.ref));
}

export function useKeepAwake(): void {
  const connected = useConnection().state === 'connected';
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const names = [T.HomeProject, ...(projects.data ?? []).map((p) => p.name)];
  const chats = useQueries({
    queries: names.map((name) => ({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), refetchInterval: 60_000 })),
  });
  const questions = useQueries({
    queries: [...new Set((agents.data ?? []).map((a) => a.project))].map((name) => ({ queryKey: ['questions', name], queryFn: () => api.questions(name) })),
  });
  const chatKey = names.map((n, i) => `${n}\t${chats[i]?.data?.chat ?? ''}`).join('\n');
  const asked = questions.flatMap((q) => q.data ?? []);
  const leads = new Map(chatKey.split('\n').map((l) => l.split('\t') as [string, string]));
  const asking = new Set((agents.data ?? []).filter((a) => isAsking(asked, a.ref)).map((a) => a.ref));
  // Without the daemon the data is only the last it said; nothing is running.
  const now = new Set(connected && busy(agents.data ?? [], leads, jobs.data ?? [], asking) ? ['busy'] : []);
  const held = useGrace(now, releaseMs).has('busy');
  useEffect(() => {
    void window.agentbox.keepAwake(held).catch(() => {});
  }, [held]);
  useEffect(() => () => void window.agentbox.keepAwake(false).catch(() => {}), []);
}
