// useHasEvidence is whether an agent (by name), or the project when there's
// none, has anything in Evidence: media, or a page it published. Its view
// shows on the panel's switch, the lead's tabs and the machine bar only then,
// or while open (lib/agentTabs.ts).
import { useMediaCounts } from '../../lib/mediaPages';
import { livePages } from '../../lib/pages';
import { useProjectPages } from '../pages/usePages';

export function useHasEvidence(project: string, name?: string): boolean {
  const counts = useMediaCounts({ project, ...(name ? { agent: name } : {}) });
  const pages = useProjectPages(project);
  const own = name && `${project}/${name}`;
  return (counts.data?.total ?? 0) > 0 || livePages(pages.data).some((p) => !own || p.agents.includes(own));
}
