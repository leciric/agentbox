// v2's Overview and Past agents: the pages for every project, each with a
// project filter beside its title, since v2's sidebar has no project tabs to
// narrow them from. They're places, not settings: the sidebar's foot opens
// them, beside Media and Jobs.
import { useState } from 'react';
import type { View } from '../../App';
import { useT } from '../../lib/i18n';
import { HomeView } from '../HomeView';
import { PastAgents } from '../PastAgents';
import { ProjectFilter } from './ProjectFilter';

export function OverviewV2({ onSelect, onAddProject, onNewAgent }: { onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: () => void }) {
  const t = useT();
  const [project, setProject] = useState('');
  return (
    <HomeView
      onSelect={onSelect}
      onAddProject={onAddProject}
      onNewAgent={onNewAgent}
      title={t('v2.sidebar.overview')}
      project={project}
      filter={<ProjectFilter value={project} onChange={setProject} />}
    />
  );
}

export function PastAgentsV2({ open, onSelect }: { open?: string; onSelect: (view: View) => void }) {
  const [project, setProject] = useState('');
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
