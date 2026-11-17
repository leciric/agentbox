// useOffice is the Office's model of every project, its lead and its agents
// (model.ts), read from the same queries the sidebar and the panels read.
// Cockpit mode (components/cockpit) reads it too.
import { useQueries, useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useReviewHead } from '../lib/inboxPages';
import { useReviewing } from '../lib/reviewAlert';
import { useAgentCounts } from '../lib/useAgentCounts';
import { officeModel, type OfficeModel } from './model';

// useOffice reads what the Office shows: the same queries the sidebar and the
// panels read, so it says what they say and moves when they do.
export function useOffice(): { model: OfficeModel; loaded: boolean; agents: ReadonlyMap<string, T.Agent>; review: readonly T.ReviewItem[]; fleets: ReadonlyMap<string, T.Fleet | undefined> } {
  const counts = useAgentCounts();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const names = (projects.data ?? []).map((p) => p.name);
  const fleets = useQueries({ queries: names.map((name) => ({ queryKey: ['fleet', name], queryFn: () => api.fleet(name), refetchInterval: 30_000 })) });
  const questions = useQueries({ queries: names.map((name) => ({ queryKey: ['questions', name], queryFn: () => api.questions(name) })) });
  const chats = useQueries({ queries: names.map((name) => ({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), refetchInterval: 60_000 })) });
  const reviewing = useReviewing();
  const head = useReviewHead(reviewing);
  const review = useMemo(() => (reviewing ? (head.data?.items ?? []) : []), [reviewing, head.data]);
  // Keys of what changed, so the maps are made again only when a query's
  // data does, not on every render.
  const namesKey = names.join('\n');
  const fleetKey = fleets.map((f) => f.dataUpdatedAt).join(',');
  const qKey = questions.map((q) => q.dataUpdatedAt).join(',');
  const chatKeyAll = chats.map((c) => `${c.dataUpdatedAt}:${c.data?.chat ?? ''}:${c.data?.last?.at ?? ''}`).join(',');
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const fleetMap = useMemo(() => new Map(names.map((n, i) => [n, fleets[i]?.data])), [namesKey, fleetKey]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const qMap = useMemo(() => new Map(names.map((n, i) => [n, Array.isArray(questions[i]?.data) ? questions[i].data : undefined])), [namesKey, qKey]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const leadMap = useMemo(() => new Map(names.map((n, i) => [n, chats[i]?.data])), [namesKey, chatKeyAll]);
  const model = useMemo(
    () => officeModel({ projects: projects.data ?? [], rows: counts.rows, fleets: fleetMap, questions: qMap, leads: leadMap, review }),
    [projects.data, counts.rows, fleetMap, qMap, leadMap, review],
  );
  const agents = useMemo(() => new Map(counts.rows.map((r) => [r.agent.ref, r.agent])), [counts.rows]);
  return { model, loaded: counts.loaded && !!projects.data, agents, review, fleets: fleetMap };
}
