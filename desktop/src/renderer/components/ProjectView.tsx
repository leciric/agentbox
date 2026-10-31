import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, Wand2, Coins, FileText, FolderGit2, FolderOpen, KeyRound, Moon, NotebookPen, Plug, SlidersHorizontal, Smile, Trash } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT, type Translate } from '../lib/i18n';
import { useProjectName } from '../lib/useProjectName';
import { projectPlace, type ProjectPlaceName, type ProjectSection } from '../lib/tabs';
import { countFeature, projectTabFeatures } from '../lib/usageStats';
import { errorMessage, timeAgo } from '../lib/utils';
import { ChatStatus, useChatMenu } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { ConnectorsTab } from './ConnectorsTab';
import { leadAgentFrom, ProjectChatPanel, useCurrentSession } from './ProjectChatPanel';
import { ProjectBasePanel } from './ProjectBasePanel';
import { ProjectMemoryPanel } from './ProjectMemoryPanel';
import { ProjectSettings } from './ProjectSettings';
import { SecretsTab } from './SecretsTab';
import { SettingsSections, type SettingsSection } from './SettingsSections';
import { SkillsPanel } from './SkillsPanel';
import { TokensPanel } from './TokensPanel';
import { Button } from './ui/button';
import { Card, Row } from './ui/card';
import { Textarea } from './ui/input';
import { ProjectIcon, ProjectIconDialog } from './ProjectIcon';
import { InTopBar, ProjectMenu } from './v2/ProjectHeader';
import { AgentMentionsProvider } from './pip/Mentions';
import { PanelButton } from './v2/PanelButton';
import { openPanelOn, setPanel } from '../lib/rightPanel';

// The project's notes: what every agent of it is told, over and above the brief
// AgentBox writes. The user writes them here, and the project's chat adds what
// it learns, so nothing has to be explained to each new agent in turn.
function ProjectNotes({ project, className }: { project: string; className?: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  // The lead writes here too, and so does anyone editing the file by hand:
  // poll while the tab is open, and keep what the user is typing either way.
  const projectName = useProjectName(project);
  const notes = useQuery({ queryKey: ['notes', project], queryFn: () => api.notes(project), refetchInterval: 5000 });
  const saved = notes.data?.text ?? '';
  const [draft, setDraft] = useState<string | null>(null);
  const text = draft ?? saved;
  const dirty = draft !== null && draft !== saved;
  const save = useMutation({
    mutationFn: () => api.saveNotes(project, text),
    onSuccess: async (updated) => {
      setDraft(null);
      queryClient.setQueryData(['notes', project], updated);
      await queryClient.invalidateQueries({ queryKey: ['brief', project] });
      toast(updated.text ? t('project.notes.saved', { project: projectName }) : t('project.notes.cleared', { project: projectName }));
    },
  });

  return (
    <Card
      className={className}
      title={t('project.notes.title')}
      icon={NotebookPen}
      description={t('project.notes.description')}
      action={
        <>
          <Button variant="ghost" size="sm" disabled={!dirty || save.isPending} onClick={() => setDraft(null)}>
            {t('project.notes.revert')}
          </Button>
          <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t('common.saving') : t('common.save')}
          </Button>
        </>
      }
    >
      <Textarea
        aria-label={t('project.notes.title')}
        data-project-notes
        className="min-h-44 font-mono text-ui"
        spellCheck={false}
        value={text}
        placeholder={t('project.notes.placeholder')}
        onChange={(event) => setDraft(event.target.value)}
      />
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-subtle">
        <span>
          {dirty
            ? t('project.notes.unsaved')
            : notes.data?.updatedAt
              ? t('project.notes.savedAgo', { when: timeAgo(notes.data.updatedAt) })
              : notes.isPending
                ? t('common.loading')
                : t('project.notes.none')}
        </span>
        <span>·</span>
        <span>{t('project.notes.leadWrites')}</span>
        {save.error && <span className="text-rose-300">{errorMessage(save.error)}</span>}
      </div>
    </Card>
  );
}

// A project opens on its chat: the conversation that directs its agents. What
// the page showed before is the Settings tab, beside what used to be tabs of
// their own: memory, tokens, secrets and connectors. `where` is a tab or a
// Settings section, by name (lib/tabs.ts).
const projectSettingsSections = (t: Translate): SettingsSection<ProjectSection>[] => [
  { id: 'repository', title: t('project.view.section.repository'), icon: FolderGit2 },
  { id: 'general', title: t('project.view.section.general'), icon: SlidersHorizontal },
  { id: 'brief', title: t('project.view.section.brief'), icon: FileText },
  { id: 'memory', title: t('project.view.section.memory'), icon: Brain },
  { id: 'tokens', title: t('project.view.section.tokens'), icon: Coins },
  { id: 'secrets', title: t('project.view.section.secrets'), icon: KeyRound },
  { id: 'connectors', title: t('project.view.section.connectors'), icon: Plug },
  { id: 'skills', title: t('project.view.section.skills'), icon: Wand2 },
];

export function ProjectView({ name, tab: opensAt, onNewAgent }: { name: string; tab?: ProjectPlaceName; onNewAgent: () => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // A project's page is its lead's chat, with no tabs: Media and Past agents
  // are pages the sidebar's foot opens, and Pull requests and Settings are
  // tabs of the right-hand panel, which a link to them opens.
  useEffect(() => {
    if (!opensAt) return;
    const place = projectPlace(opensAt);
    if (place.tab === 'pulls' || place.tab === 'issues') openPanelOn(`${name}/${T.LeadName}`, place.tab);
    else if (place.tab === 'settings') {
      setPanel({ section: place.section });
      openPanelOn(`${name}/${T.LeadName}`, 'settings');
    }
  }, [opensAt, name]);
  const [iconOpen, setIconOpen] = useState(false);
  useEffect(() => countFeature(projectTabFeatures.chat), [name]);
  // The session its chat shows (ProjectChatPanel's tabs): its status, files
  // and ⋯ are that session's.
  const session = useCurrentSession(name);
  const leadChat = useQuery({ queryKey: ['projectChat', name, session?.id], queryFn: () => api.projectChat(name, session!.id), enabled: !!session });
  const project = projects.data?.find((p) => p.name === name);
  // ProjectMenu in the top bar takes the chat's New chat and Reload tools as
  // items; the rest are its sidebar menu's and its Settings' (ProjectActions).
  const lead = project && leadChat.data && session ? leadAgentFrom(project, leadChat.data, session.topic) : undefined;
  const chatMenu = useChatMenu(lead);

  if (!project) {
    return <div className="p-6 text-sm text-subtle">{projects.isPending ? t('common.loading') : t('project.view.noProject', { name })}</div>;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <InTopBar>
        {lead && <ChatStatus agent={lead} />}
        {chatMenu?.dialogs}
        {chatMenu?.untracked}
        <InTopBar end>
          {lead && <ProjectMenu project={project} leadRef={lead.ref} onNewAgent={onNewAgent} onChangeIcon={() => setIconOpen(true)} newChat={chatMenu?.newChat} more={chatMenu?.reload} />}
          <PanelButton project={name} chat={`${name}/${T.LeadName}`} />
        </InTopBar>
      </InTopBar>

      <div className="relative mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
        {/* The lead links its agents' names in the chat (components/pip);
            the desktops they're using are in the right-hand panel's list. */}
        <AgentMentionsProvider project={name}>
          <ProjectChatPanel project={project} />
        </AgentMentionsProvider>
      </div>

      <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />
    </div>
  );
}

// useProjectActions is what a project offers besides its chat: freeing its
// idle agents' machines and removing it, with their confirmations, in its
// Settings (ProjectActions).
function useProjectActions(name: string, onSelect: (view: View) => void) {
  const t = useT();
  const queryClient = useQueryClient();
  const [removing, setRemoving] = useState(false);
  const [freeing, setFreeing] = useState(false);
  // Stopping frees the memory an idle agent holds and keeps its disk, so it
  // comes back in seconds. Nothing is lost: the work is on its branch.
  const retire = useMutation({
    mutationFn: () => api.retire(name, { how: 'stop' }),
    onSuccess: async (result) => {
      const freed = result.retired.length;
      toast(freed === 0 ? t('project.view.nothingToFree') : t('project.view.stopped', { count: freed }), {
        description: freed === 0 ? t('project.view.nothingToFreeDetail') : t('project.view.stoppedDetail'),
      });
      await queryClient.invalidateQueries({ queryKey: ['fleet', name] });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    onError: (err) => toast.error(String(err)),
  });
  const dialogs = (
    <>
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t('project.view.removeTitle', { name })}
        description={t('project.view.removeDescription')}
        confirmLabel={t('common.remove')}
        destructive
        onConfirm={async () => {
          await api.removeProject(name);
          await queryClient.invalidateQueries({ queryKey: ['projects'] });
          onSelect({ kind: 'home' });
        }}
      />
      <ConfirmDialog
        open={freeing}
        onOpenChange={setFreeing}
        title={t('project.view.freeTitle')}
        description={t('project.view.freeDescription')}
        confirmLabel={t('project.view.freeConfirm')}
        onConfirm={async () => {
          await retire.mutateAsync();
        }}
      />
    </>
  );
  return { retire, setFreeing, setRemoving, dialogs };
}

// ProjectActions is their place, in Settings → General, with the
// repository's folder: there's no project ⋯ for them.
function ProjectActions({ project, onSelect }: { project: T.Project; onSelect: (view: View) => void }) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const mine = agents.data?.filter((a) => a.project === project.name) ?? [];
  const { retire, setFreeing, setRemoving, dialogs } = useProjectActions(project.name, onSelect);
  return (
    <Card className="mt-4" title={t('project.view.actions')} icon={FolderGit2}>
      <div className="flex flex-wrap gap-2" data-project-actions>
        <Button size="sm" variant="secondary" onClick={() => void window.agentbox.openPath(project.root)}>
          <FolderOpen />
          {t('project.view.openFolder')}
        </Button>
        <Button size="sm" variant="secondary" disabled={mine.length === 0 || retire.isPending} onClick={() => setFreeing(true)}>
          <Moon />
          {t('project.view.freeMachines')}
        </Button>
        <Button size="sm" variant="danger" disabled={mine.length > 0} title={mine.length > 0 ? t('project.view.hasAgents') : undefined} onClick={() => setRemoving(true)}>
          <Trash />
          {t('project.view.removeProject')}
        </Button>
      </div>
      {dialogs}
    </Card>
  );
}

// ProjectSettingsBody is a project's Settings: its sections down the side,
// or along the top where it's narrow: the right-hand panel's Settings tab
// (components/v2/RightPanel.tsx).
export function ProjectSettingsBody({ project, section, onSection, onSelect, onOpenMedia }: { project: T.Project; section: ProjectSection; onSection: (section: ProjectSection) => void; onSelect: (view: View) => void; onOpenMedia: () => void }) {
  const t = useT();
  const name = project.name;
  const [iconOpen, setIconOpen] = useState(false);
  const [showBrief, setShowBrief] = useState(false);
  const brief = useQuery({ queryKey: ['brief', name], queryFn: () => api.brief(name), enabled: section === 'brief' && showBrief });
  return (
    <>
    <SettingsSections label={t('project.view.settingsLabel', { name })} sections={projectSettingsSections(t)} value={section} onValueChange={onSection}>
      {section === 'repository' && (
        <div className="mx-auto grid w-full max-w-3xl grid-cols-1 gap-6 px-4 py-6 md:px-8 md:py-7">
          <Card title={t('project.view.section.repository')} icon={FolderGit2}>
            <Row label={t('project.view.folder')} mono>
              <span className="truncate" title={project.root}>
                {project.root}
              </span>
            </Row>
            <Row label={t('project.view.newAgentsFrom')} mono>
              {project.branch}
            </Row>
            <Row label={t('project.view.envFiles')} mono>
              {project.envFiles.length > 0 ? project.envFiles.join(', ') : t('project.view.envNone')}
            </Row>
          </Card>
          <ProjectBasePanel project={project} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
        </div>
      )}
      {section === 'general' && (
        <div className="mx-auto w-full max-w-3xl px-4 py-6 md:px-8 md:py-7">
          <Card className="mb-4" title={t('projectIcon.label')} icon={Smile} description={t('projectIcon.description')} action={<Button size="sm" onClick={() => setIconOpen(true)} data-project-settings-icon>{t('projectIcon.change')}</Button>}>
            <ProjectIcon project={project} className="size-10 rounded-xl text-heading" />
          </Card>
          <ProjectSettings project={project} />
          <ProjectActions project={project} onSelect={onSelect} />
        </div>
      )}
      {section === 'brief' && (
        <div className="mx-auto w-full max-w-3xl px-4 py-6 md:px-8 md:py-7">
          <ProjectNotes project={name} />
          <Card
            className="mt-4"
            title={t('project.view.briefTitle')}
            icon={FileText}
            description={t('project.view.briefDescription')}
            action={
              <Button variant="ghost" size="sm" onClick={() => setShowBrief((v) => !v)}>
                {showBrief ? t('project.view.hide') : t('project.view.show')}
              </Button>
            }
          >
            {showBrief && (
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-xl border border-line-faint bg-sunken p-4 font-mono text-caption leading-relaxed text-tertiary">
                {brief.data ?? t('common.loading')}
              </pre>
            )}
          </Card>
        </div>
      )}
      {section === 'memory' && <ProjectMemoryPanel project={name} onOpenMedia={onOpenMedia} />}
      {section === 'tokens' && (
        <div className="mx-auto w-full max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <TokensPanel project={name} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
        </div>
      )}
      {section === 'secrets' && <SecretsTab target={name} />}
      {section === 'connectors' && <ConnectorsTab target={name} />}
      {section === 'skills' && <SkillsPanel project={name} />}
    </SettingsSections>
      <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />
    </>
  );
}
