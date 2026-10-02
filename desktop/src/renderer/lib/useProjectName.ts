import { useQuery } from '@tanstack/react-query';
import { api } from './api.ts';
import { projectLabel } from './projectName.ts';

// useProjectName is what the project with that slug is called, read from the
// projects list every view already keeps (projectLabel).
export function useProjectName(slug: string): string {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  return projectLabel(slug, projects.data);
}
