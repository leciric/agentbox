// v2's pages for every project, each with a project filter, since v2's
// sidebar has no project tabs to narrow them from: Home's Status tab (every
// project's agents and the machine's load) and Past agents. Jobs and Media,
// like Past agents and Settings, open from the sidebar's foot.
import { useState } from 'react';
import type { View } from '../../App';
import { HomeView } from '../HomeView';
import { PastAgents } from '../PastAgents';
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

export function PastAgentsV2({ open, project: from, onSelect }: { open?: string; project?: string; onSelect: (view: View) => void }) {
  // Opened from a project's list of agents, on that project's.
  const [project, setProject] = useState(from ?? '');
  return (
    <PastAgents
      open={open}
      project={project || undefined}
      filter={<ProjectFilter value={project} onChange={setProject} />}
      onOpen={(ref) => onSelect({ kind: 'pastAgents', ref: ref ?? undefined })}
      onSelect={onSelect}
    />
  );
}
