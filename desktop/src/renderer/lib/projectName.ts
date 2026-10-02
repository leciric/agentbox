import type * as T from '../../shared/api';

// A project has two names: what the user calls it (displayName), free-form and
// shown wherever they see it, and its slug (name), the id in its API paths,
// refs, URLs and query keys. projectLabel is the one to show, from a project
// or from the slug the rest of the app passes around; a slug no project has
// (one that has gone, or the list not loaded yet) is shown as it is.
export function projectLabel(project: Pick<T.Project, 'name' | 'displayName'> | undefined): string;
export function projectLabel(slug: string, projects: readonly Pick<T.Project, 'name' | 'displayName'>[] | undefined): string;
export function projectLabel(
  project: string | Pick<T.Project, 'name' | 'displayName'> | undefined,
  projects?: readonly Pick<T.Project, 'name' | 'displayName'>[],
): string {
  if (project === undefined) return '';
  if (typeof project === 'string') {
    const found = projects?.find((p) => p.name === project);
    return found ? projectLabel(found) : project;
  }
  return project.displayName || project.name;
}

