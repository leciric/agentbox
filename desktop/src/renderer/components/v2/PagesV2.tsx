// v2's pages for every project, with a project filter, since v2's sidebar
// has no project tabs to narrow them from: Home's Status tab (every
// project's agents and the machine's load). Work (WorkView's WorkPage), Jobs
// and Media, like Settings, open from the sidebar's foot.
import { useState } from 'react';
import type { View } from '../../App';
import { HomeView } from '../HomeView';
import { ProjectFilter } from './ProjectFilter';

export function HomeStatusV2({ onSelect, onAddProject, onNewAgent }: { onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: () => void }) {
  const [project, setProject] = useState('');
  return (
    <HomeView
      onSelect={onSelect}
      onAddProject={onAddProject}
      onNewAgent={onNewAgent}
      heading={false}
      pastAgents={false}
      project={project}
      filter={<ProjectFilter value={project} onChange={setProject} />}
    />
  );
}
