// v2 moves Overview, Media and Past agents out of the sidebar and the project
// tabs into Settings, as sections next to General, each across every project
// with a project filter. Their views (home, media, pastAgents) stay, so a
// link or Ctrl+K still opens them: App draws Settings at that section.
import { useState } from 'react';
import type { View } from '../../App';
import { useT } from '../../lib/i18n';
import type { SettingSection } from '../../lib/settingsSearch';
import { AllMediaView } from '../AllMediaView';
import { HomeView } from '../HomeView';
import { PastAgents } from '../PastAgents';
import { ProjectFilter } from './ProjectFilter';

export type V2Page = 'overview' | 'media' | 'pastAgents';

// pageOf is the section of Settings a view opens in v2, or undefined for a
// view that isn't one (Settings itself opens where it was).
export function pageOf(view: View): V2Page | undefined {
  if (view.kind === 'home') return 'overview';
  if (view.kind === 'media') return 'media';
  if (view.kind === 'pastAgents' && !view.ref) return 'pastAgents';
  return undefined;
}

export const inSettings = (view: View) => view.kind === 'settings' || pageOf(view) !== undefined;

// viewOf is the view for a section the user picked in Settings.
export function viewOf(section: string): View {
  if (section === 'overview') return { kind: 'home' };
  if (section === 'media') return { kind: 'media' };
  if (section === 'pastAgents') return { kind: 'pastAgents' };
  return { kind: 'settings' };
}

export function useV2Pages({ onSelect, onAddProject, onNewAgent }: { onSelect: (view: View) => void; onAddProject: () => void; onNewAgent: () => void }): SettingSection[] {
  const t = useT();
  const [overview, setOverview] = useState('');
  const [past, setPast] = useState('');
  return [
    {
      id: 'overview',
      title: t('v2.sidebar.overview'),
      description: t('v2.settings.overview'),
      scope: 'installation',
      groups: [],
      page: () => <HomeView onSelect={onSelect} onAddProject={onAddProject} onNewAgent={onNewAgent} project={overview} filter={<ProjectFilter value={overview} onChange={setOverview} />} />,
    },
    {
      id: 'media',
      title: t('shell.nav.media'),
      description: t('shell.allMedia.subtitle'),
      scope: 'installation',
      groups: [],
      page: () => <AllMediaView onSelect={onSelect} />,
    },
    {
      id: 'pastAgents',
      title: t('pastAgents.title'),
      description: t('pastAgents.description'),
      scope: 'installation',
      groups: [],
      page: () => (
        <PastAgents
          project={past || undefined}
          filter={<ProjectFilter value={past} onChange={setPast} />}
          onOpen={(ref) => ref && onSelect({ kind: 'pastAgents', ref })}
          onSelect={onSelect}
        />
      ),
    },
  ];
}
