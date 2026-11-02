// useAgentCounts reads what agentCounts.ts counts from, the same queries for
// everyone who shows a count: every agent, the agents ready to review (the
// review head, when this computer reviews at all), each project's questions
// (isAsking, as the panel's avatars read them) and each lead's chat. The
// sidebar, its filter, the agent panel and Home all read this, so they say
// the same numbers.
import { useQueries, useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { api } from './api.ts';
import { isAsking } from './agentStatus.ts';
import { emptyTally, stRows, tally, tallyByProject, type Tally } from './agentCounts.ts';
import { useReviewHead } from './inboxPages.ts';
import type { AgentSt, Row } from './leadFirst.ts';
import { useReviewing } from './reviewAlert.ts';

export type AgentCounts = {
  // Until the agents are in, every count would be 0.
  loaded: boolean;
  rows: Row[];
  st: (ref: string) => AgentSt | undefined;
  // Each project's lead's chat state.
  leads: ReadonlyMap<string, string | undefined>;
  review: ReadonlySet<string>;
  asking: ReadonlySet<string>;
  all: Tally;
  of: (project: string) => Tally;
};

export function useAgentCounts(): AgentCounts {
  const reviewing = useReviewing();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const head = useReviewHead(reviewing);
  const names = (projects.data ?? []).map((p) => p.name);
  const questions = useQueries({ queries: names.map((name) => ({ queryKey: ['questions', name], queryFn: () => api.questions(name) })) });
  const chats = useQueries({ queries: names.map((name) => ({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), refetchInterval: 60_000 })) });
  const fleets = useQueries({ queries: names.map((name) => ({ queryKey: ['fleet', name], queryFn: () => api.fleet(name), staleTime: 30_000 })) });

  // Keys of what the counts depend on, so they're worked out again only when
  // one changes, not on every render of every query.
  const reviewKey = reviewing ? (head.data?.items ?? []).map((i) => i.ref).join('\n') : '';
  // A project's questions that can't be read count none, rather than take
  // every count down with them.
  const askKey = questions.flatMap((q) => (Array.isArray(q.data) ? q.data : []).filter((x) => isAsking([x], x.ref)).map((x) => x.ref)).join('\n');
  const leadKey = names.map((n, i) => `${n}\t${chats[i]?.data?.chat ?? ''}`).join('\n');
  const prKey = fleets.flatMap((f) => (f.data?.agents ?? []).flatMap((a) => (a.pr?.number ? [`${a.ref}\t${a.pr.number}`] : []))).join('\n');

  return useMemo(() => {
    const review = new Set(reviewKey ? reviewKey.split('\n') : []);
    const asking = new Set(askKey ? askKey.split('\n') : []);
    const leads = new Map(leadKey ? leadKey.split('\n').map((l) => l.split('\t') as [string, string]) : []);
    const prs = new Map(prKey ? prKey.split('\n').map((l) => { const [ref, n] = l.split('\t'); return [ref, Number(n)] as const; }) : []);
    const rows = stRows(agents.data ?? [], { review, asking }, prs);
    const byRef = new Map(rows.map((r) => [r.agent.ref, r.st]));
    const by = tallyByProject(rows, leads);
    return {
      loaded: !!agents.data,
      rows,
      st: (ref: string) => byRef.get(ref),
      leads,
      review,
      asking,
      all: tally(rows, leads),
      of: (project: string) => by.get(project) ?? emptyTally(),
    };
  }, [agents.data, reviewKey, askKey, leadKey, prKey]);
}
