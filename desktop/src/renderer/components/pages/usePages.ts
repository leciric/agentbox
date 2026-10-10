// The queries every pages surface reads: one project's pages, or every
// project's for the all-projects Media view. Reading a project's pages for
// the first time here takes them all as seen (lib/pages.ts readPages).
import { useQueries, useQuery } from '@tanstack/react-query';
import { api, isHomeChat } from '../../lib/api';
import { allLivePages, pagesKey, readPages, useSeenPages, type PageAt } from '../../lib/pages';

export const pagesQuery = (project: string) => ({ queryKey: pagesKey(project), queryFn: readPages(project, api.pages), staleTime: 60_000 });

export function useProjectPages(project: string | null | undefined) {
  const query = useQuery({ ...pagesQuery(project ?? ''), enabled: !!project && !isHomeChat(project) });
  // Seeing a page re-renders whoever reads them, for what's new.
  useSeenPages();
  return query;
}

// useAllPages is every project's live pages, each with its project.
export function useAllPages(): PageAt[] {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const names = (projects.data ?? []).map((p) => p.name);
  const lists = useQueries({ queries: names.map(pagesQuery) });
  useSeenPages();
  return allLivePages(lists.map((q, i) => [names[i], q.data]));
}

// useHatchConnected is whether any project has Hatch connected, for a
// setting that's global but only means something with it.
export function useHatchConnected(): boolean {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const names = (projects.data ?? []).map((p) => p.name);
  const lists = useQueries({ queries: names.map(pagesQuery) });
  return lists.some((q) => !!q.data?.connector);
}
