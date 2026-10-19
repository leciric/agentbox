import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, Brain, Clapperboard, Wand2, Coins, Ellipsis, FileText, FolderGit2, FolderOpen, GitPullRequest, Image, KeyRound, MessagesSquare, Moon, NotebookPen, Plug, Plus, SlidersHorizontal, Smile, Trash } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT, type Translate } from '../lib/i18n';
import { useProjectName } from '../lib/useProjectName';
import { projectLabel } from '../lib/projectName';
import { projectPlace, type ProjectPlaceName, type ProjectSection } from '../lib/tabs';
import { countFeature, projectTabFeatures } from '../lib/usageStats';
import { errorMessage, timeAgo } from '../lib/utils';
import { ChatStatus, FilesOpen, useChatMenu } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { ConnectorsTab } from './ConnectorsTab';
import { leadAgentFrom, ProjectChatPanel } from './ProjectChatPanel';
import { PastAgents } from './PastAgents';
import { ProjectBasePanel } from './ProjectBasePanel';
import { ProjectMediaPanel } from './ProjectMediaPanel';
import { ScopedMediaPages } from './pages/MediaPages';
import { ProjectMemoryPanel } from './ProjectMemoryPanel';
import { ProjectSettings } from './ProjectSettings';
import { PullRequestsPanel } from './PullRequestsPanel';
import { SecretsTab } from './SecretsTab';
import { SettingsSections, type SettingsSection } from './SettingsSections';
import { SkillsPanel } from './SkillsPanel';
import { TokensPanel } from './TokensPanel';
import { Button } from './ui/button';
import { Tip } from './ui/tooltip';
import { Card, Row } from './ui/card';
import { Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';
import { useUiV2 } from '../lib/uiV2';
import { ProjectIcon, ProjectIconDialog } from './ProjectIcon';
import { InTopBar, ProjectMenu, ProjectPlaces } from './v2/ProjectHeader';
import { AgentMentionsProvider } from './pip/Mentions';
import { PanelButton } from './v2/PanelButton';
import { openPanelOn, setPanel } from '../lib/rightPanel';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

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
        className="min-h-44 font-mono text-[12.5px]"
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

export function ProjectView({ name, tab: opensAt, onSelect, onNewAgent }: { name: string; tab?: ProjectPlaceName; onSelect: (view: View) => void; onNewAgent: () => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  // v2 has no tabs: Media and Past agents are pages the sidebar's foot opens, and Pull
  // requests and Settings are tabs of the right-hand panel, which a link to
  // them opens.
  const v2 = useUiV2();
  const [where, setWhere] = useState<string>(v2 ? 'chat' : (opensAt ?? 'chat'));
  useEffect(() => {
    if (!v2 || !opensAt) return;
    const place = projectPlace(opensAt);
    if (place.tab === 'pulls') openPanelOn(`${name}/${T.LeadName}`, 'pulls');
    else if (place.tab === 'settings') {
      setPanel({ section: place.section });
      openPanelOn(`${name}/${T.LeadName}`, 'settings');
    }
  }, [v2, opensAt, name]);
  const [iconOpen, setIconOpen] = useState(false);
  const { tab, section } = projectPlace(where);
  // The past agent read in the Past agents tab, if one is.
  const [past, setPast] = useState<string | null>(null);
  useEffect(() => countFeature(projectTabFeatures[tab === 'settings' ? section : tab]), [tab, section, name]);
  const leadChat = useQuery({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), enabled: tab === 'chat' });
  const project = projects.data?.find((p) => p.name === name);
  // The lead's chat controls join the project's ⋯ while the chat is open.
  const lead = project && tab === 'chat' && leadChat.data ? leadAgentFrom(project, leadChat.data) : undefined;
  const chatMenu = useChatMenu(lead);
  const mine = agents.data?.filter((a) => a.project === name) ?? [];
  const { retire, setFreeing, setRemoving, dialogs: projectDialogs } = useProjectActions(name, onSelect);

  // v1's ⋯. v2's is ProjectMenu in the top bar, which takes the chat's New
  // chat and Reload tools as items; the rest are its sidebar menu's and its
  // Settings' (ProjectActions).
  const menuItems = project && (
    <>
      {chatMenu && (
        <>
          {chatMenu.items}
          <MenuSeparator />
          <MenuLabel>{t('project.view.projectSection')}</MenuLabel>
        </>
      )}
      <MenuItem icon={FolderOpen} onSelect={() => void window.agentbox.openPath(project.root)}>
        {t('project.view.openFolder')}
      </MenuItem>
      <MenuItem icon={Moon} disabled={mine.length === 0 || retire.isPending} onSelect={() => setFreeing(true)}>
        {t('project.view.freeMachines')}
      </MenuItem>
      <MenuItem icon={Trash} destructive disabled={mine.length > 0} hint={mine.length > 0 ? t('project.view.hasAgents') : undefined} onSelect={() => setRemoving(true)}>
        {t('project.view.removeProject')}
      </MenuItem>
    </>
  );

  if (!project) {
    return <div className="p-6 text-sm text-subtle">{projects.isPending ? t('common.loading') : t('project.view.noProject', { name })}</div>;
  }

  return (
    <Tabs value={tab} onValueChange={setWhere} className="flex h-full min-h-0 flex-col">
      {v2 ? (
        <InTopBar>
          {lead && <ChatStatus agent={lead} />}
          {chatMenu?.dialogs}
          {chatMenu?.untracked}
          <InTopBar end>
            <ProjectPlaces project={name} leadRef={`${name}/${T.LeadName}`} />
            <Tip label={t('today.open')}>
              <Button size="icon-sm" variant="ghost" aria-label={t('today.open')} onClick={() => onSelect({ kind: 'today', project: name })} data-open-today>
                <Clapperboard />
              </Button>
            </Tip>
            {lead && <ProjectMenu project={project} leadRef={lead.ref} onNewAgent={onNewAgent} onChangeIcon={() => setIconOpen(true)} newChat={chatMenu?.newChat} more={chatMenu?.reload} />}
            <PanelButton project={name} chat={`${name}/${T.LeadName}`} />
          </InTopBar>
        </InTopBar>
      ) : (
      <div className="w-full shrink-0 px-4 pb-3 pt-4 md:px-8 md:pb-4 md:pt-5">
        <div className="flex flex-wrap items-center gap-3">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-xl border border-line-strong bg-surface text-tertiary">
            <FolderGit2 className="size-4" />
          </span>
          {/* It gives way to the actions beside it, cutting a long root short,
              before they wrap under it. */}
          <div className="min-w-0 flex-1 basis-40">
            <h1 className="truncate text-[15px] font-semibold tracking-tight text-title">{projectLabel(project)}</h1>
            <p className="truncate font-mono text-[11px] text-subtle">{project.root}</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <Button onClick={onNewAgent}>
              <Plus />
              {t('project.view.newAgent')}
            </Button>
            <Menu>
              <MenuTrigger asChild>
                <Button size="icon" variant="ghost" aria-label={t('project.view.actions')} data-project-menu>
                  <Ellipsis />
                </Button>
              </MenuTrigger>
              <MenuContent>{menuItems}</MenuContent>
            </Menu>
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3">
            {/* It scrolls rather than wraps once the tabs outgrow the column, and
                without a scrollbar: the tabs are the control, and a bar under
                them only says the last one is a few pixels away. */}
            <TabsList className="min-w-0 flex-1 overflow-x-auto [scrollbar-width:none]">
              <TabsTrigger value="chat">
                <MessagesSquare />
                {t('project.view.tab.chat')}
              </TabsTrigger>
              <TabsTrigger value="pulls">
                <GitPullRequest />
                {t('project.view.tab.pulls')}
              </TabsTrigger>
              <TabsTrigger value="media">
                <Image />
                {t('project.view.tab.media')}
              </TabsTrigger>
              <TabsTrigger value="past" data-project-tab="past">
                <Archive />
                {t('project.view.tab.past')}
              </TabsTrigger>
              <TabsTrigger value="settings">
                <SlidersHorizontal />
                {t('project.view.tab.settings')}
              </TabsTrigger>
            </TabsList>
            {lead && (
              <div className="flex shrink-0 items-center gap-2">
                <ChatStatus agent={lead} />
                <FilesOpen agent={lead} />
              </div>
            )}
            {chatMenu?.dialogs}
          </div>
      </div>
      )}

      <TabsContent value="chat" className="flex flex-col">
        <div className="relative mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
          {v2 ? (
            // UI v2's lead links its agents' names in the chat (components/pip);
            // the desktops they're using are in the right-hand panel's list.
            <AgentMentionsProvider project={name}>
              <ProjectChatPanel project={project} />
            </AgentMentionsProvider>
          ) : (
            <ProjectChatPanel project={project} />
          )}
        </div>
      </TabsContent>

      <TabsContent value="pulls" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <PullRequestsPanel project={name} onSelect={onSelect} onOpenAccount={() => setWhere('general')} />
        </div>
      </TabsContent>

      <TabsContent value="media" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <ScopedMediaPages project={name} variant="page">
            <ProjectMediaPanel project={name} />
          </ScopedMediaPages>
        </div>
      </TabsContent>

      <TabsContent value="past" className="flex flex-col">
        <div className="min-h-0 flex-1">
          <PastAgents project={name} open={past ?? undefined} onOpen={setPast} onSelect={onSelect} />
        </div>
      </TabsContent>

      <TabsContent value="settings" className="flex flex-col">
        <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
          <ProjectSettingsBody project={project} section={section} onSection={setWhere} onSelect={onSelect} onOpenMedia={() => setWhere('media')} />
        </div>
      </TabsContent>

      {v2 && <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />}
      {projectDialogs}
    </Tabs>
  );
}

// useProjectActions is what a project offers besides its chat: freeing its
// idle agents' machines and removing it, with their confirmations. v1's ⋯
// has them, and in v2 its Settings (ProjectActions).
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

// ProjectActions is v2's place for them, in Settings → General, with the
// repository's folder: v2 has no project ⋯.
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
// or along the top where it's narrow. The Settings tab of ProjectView, and in
// UI v2 the right-hand panel's (components/v2/RightPanel.tsx).
export function ProjectSettingsBody({ project, section, onSection, onSelect, onOpenMedia }: { project: T.Project; section: ProjectSection; onSection: (section: ProjectSection) => void; onSelect: (view: View) => void; onOpenMedia: () => void }) {
  const t = useT();
  const v2 = useUiV2();
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
          {v2 && (
            <Card className="mb-4" title={t('projectIcon.label')} icon={Smile} description={t('projectIcon.description')} action={<Button size="sm" onClick={() => setIconOpen(true)} data-project-settings-icon>{t('projectIcon.change')}</Button>}>
              <ProjectIcon project={project} className="size-10 rounded-xl text-[16px]" />
            </Card>
          )}
          <ProjectSettings project={project} />
          {v2 && <ProjectActions project={project} onSelect={onSelect} />}
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
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-xl border border-line-faint bg-sunken p-4 font-mono text-[12px] leading-relaxed text-tertiary">
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
      {v2 && <ProjectIconDialog project={project} open={iconOpen} onOpenChange={setIconOpen} />}
    </>
  );
}
