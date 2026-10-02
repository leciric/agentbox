import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, Coins, Ellipsis, FileText, FolderGit2, FolderOpen, GitPullRequest, Image, KeyRound, ListTodo, MessagesSquare, Moon, NotebookPen, Plug, Plus, SlidersHorizontal, Trash } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import { api } from '../lib/api';
import { useProjectName } from '../lib/useProjectName';
import { projectLabel } from '../lib/projectName';
import { projectPlace, type ProjectPlaceName, type ProjectSection } from '../lib/tabs';
import { countFeature, projectTabFeatures } from '../lib/usageStats';
import { errorMessage, timeAgo } from '../lib/utils';
import { ChatHeaderControls } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { ConnectorsTab } from './ConnectorsTab';
import { leadAgentFrom, ProjectChatPanel } from './ProjectChatPanel';
import { ProjectBasePanel } from './ProjectBasePanel';
import { ProjectMediaPanel } from './ProjectMediaPanel';
import { ProjectMemoryPanel } from './ProjectMemoryPanel';
import { ProjectSettings } from './ProjectSettings';
import { ProjectTasksPanel } from './ProjectTasksPanel';
import { PullRequestsPanel } from './PullRequestsPanel';
import { SecretsTab } from './SecretsTab';
import { SettingsSections, type SettingsSection } from './SettingsSections';
import { TokensPanel } from './TokensPanel';
import { Button } from './ui/button';
import { Card, Row } from './ui/card';
import { Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

// The project's notes: what every agent of it is told, over and above the brief
// AgentBox writes. The user writes them here, and the project's chat adds what
// it learns, so nothing has to be explained to each new agent in turn.
function ProjectNotes({ project, className }: { project: string; className?: string }) {
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
      toast(updated.text ? `Saved ${projectName}'s notes` : `Cleared ${projectName}'s notes`);
    },
  });

  return (
    <Card
      className={className}
      title="Notes for agents"
      icon={NotebookPen}
      description="Folded into every agent's brief, so the project doesn't have to be explained to each one."
      action={
        <>
          <Button variant="ghost" size="sm" disabled={!dirty || save.isPending} onClick={() => setDraft(null)}>
            Revert
          </Button>
          <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? 'Saving…' : 'Save'}
          </Button>
        </>
      }
    >
      <Textarea
        aria-label="Notes for agents"
        className="min-h-44 font-mono text-[12.5px]"
        spellCheck={false}
        value={text}
        placeholder={'What every agent should know: how to run it, the conventions it should keep, decisions you have made.\nMarkdown, and the shorter the better.'}
        onChange={(event) => setDraft(event.target.value)}
      />
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-subtle">
        <span>
          {dirty
            ? 'Unsaved changes'
            : notes.data?.updatedAt
              ? `Saved ${timeAgo(notes.data.updatedAt)}`
              : notes.isPending
                ? 'Loading…'
                : 'No notes yet'}
        </span>
        <span>·</span>
        <span>The project's chat writes here too, under a heading of its own; what you write is left alone.</span>
        {save.error && <span className="text-rose-300">{errorMessage(save.error)}</span>}
      </div>
    </Card>
  );
}

// A project opens on its chat: the conversation that directs its agents. What
// the page showed before is the Settings tab, beside what used to be tabs of
// their own: memory, tokens, secrets and connectors. `where` is a tab or a
// Settings section, by name (lib/tabs.ts).
const projectSettingsSections: SettingsSection<ProjectSection>[] = [
  { id: 'repository', title: 'Repository', icon: FolderGit2 },
  { id: 'general', title: 'General', icon: SlidersHorizontal },
  { id: 'brief', title: 'Notes and brief', icon: FileText },
  { id: 'memory', title: 'Memory', icon: Brain },
  { id: 'tokens', title: 'Tokens', icon: Coins },
  { id: 'secrets', title: 'Secrets', icon: KeyRound },
  { id: 'connectors', title: 'Connectors', icon: Plug },
];

export function ProjectView({ name, tab: opensAt, onSelect, onNewAgent }: { name: string; tab?: ProjectPlaceName; onSelect: (view: View) => void; onNewAgent: () => void }) {
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const [where, setWhere] = useState<string>(opensAt ?? 'chat');
  const { tab, section } = projectPlace(where);
  const [showBrief, setShowBrief] = useState(false);
  const brief = useQuery({ queryKey: ['brief', name], queryFn: () => api.brief(name), enabled: tab === 'settings' && section === 'brief' && showBrief });
  const [removing, setRemoving] = useState(false);
  const [freeing, setFreeing] = useState(false);
  useEffect(() => countFeature(projectTabFeatures[tab === 'settings' ? section : tab]), [tab, section, name]);
  const leadChat = useQuery({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), enabled: tab === 'chat' });
  const project = projects.data?.find((p) => p.name === name);
  const mine = agents.data?.filter((a) => a.project === name) ?? [];
  // Stopping frees the memory an idle agent holds and keeps its disk, so it
  // comes back in seconds. Nothing is lost: the work is on its branch.
  const retire = useMutation({
    mutationFn: () => api.retire(name, { how: 'stop' }),
    onSuccess: async (result) => {
      const freed = result.retired.length;
      toast(freed === 0 ? 'Nothing to free' : `Stopped ${freed} finished agent${freed === 1 ? '' : 's'}`, {
        description: freed === 0 ? 'No agent of this project is both finished and holding a machine.' : 'Their work stays on their branches. Start them again any time.',
      });
      await queryClient.invalidateQueries({ queryKey: ['fleet', name] });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    onError: (err) => toast.error(String(err)),
  });

  if (!project) {
    return <div className="p-6 text-sm text-subtle">{projects.isPending ? 'Loading…' : `There's no project named ${name}.`}</div>;
  }

  return (
    <Tabs value={tab} onValueChange={setWhere} className="flex h-full min-h-0 flex-col">
      <div className="w-full shrink-0 px-4 pb-3 pt-4 md:px-8 md:pb-4 md:pt-5">
        <div className="flex flex-wrap items-center gap-3">
          <span className="flex size-9 items-center justify-center rounded-xl border border-line-strong bg-surface text-tertiary">
            <FolderGit2 className="size-4" />
          </span>
          <div className="min-w-0">
            <h1 className="truncate text-[15px] font-semibold tracking-tight text-title">{projectLabel(project)}</h1>
            <p className="truncate font-mono text-[11px] text-subtle">{project.root}</p>
          </div>
          <div className="ml-auto flex gap-2">
            <Button onClick={onNewAgent}>
              <Plus />
              New agent
            </Button>
            <Menu>
              <MenuTrigger asChild>
                <Button size="icon" variant="ghost" aria-label="Project actions">
                  <Ellipsis />
                </Button>
              </MenuTrigger>
              <MenuContent>
                <MenuItem icon={FolderOpen} onSelect={() => void window.agentbox.openPath(project.root)}>
                  Open the repository folder
                </MenuItem>
                <MenuItem icon={Moon} disabled={mine.length === 0 || retire.isPending} onSelect={() => setFreeing(true)}>
                  Free finished agents' machines
                </MenuItem>
                <MenuItem icon={Trash} destructive disabled={mine.length > 0} hint={mine.length > 0 ? 'has agents' : undefined} onSelect={() => setRemoving(true)}>
                  Remove project
                </MenuItem>
              </MenuContent>
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
              Chat
            </TabsTrigger>
            <TabsTrigger value="tasks">
              <ListTodo />
              Tasks
            </TabsTrigger>
            <TabsTrigger value="pulls">
              <GitPullRequest />
              Pull requests
            </TabsTrigger>
            <TabsTrigger value="media">
              <Image />
              Media
            </TabsTrigger>
            <TabsTrigger value="settings">
              <SlidersHorizontal />
              Settings
            </TabsTrigger>
          </TabsList>
          {tab === 'chat' && leadChat.data && (
            <div className="flex shrink-0 items-center gap-2">
              <ChatHeaderControls agent={leadAgentFrom(project, leadChat.data)} />
            </div>
          )}
        </div>
      </div>

      <TabsContent value="chat" className="flex flex-col">
        <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
          <ProjectChatPanel project={project} />
        </div>
      </TabsContent>

      <TabsContent value="tasks" className="overflow-y-auto">
        <ProjectTasksPanel project={name} onSelect={onSelect} onOpenChat={() => setWhere('chat')} />
      </TabsContent>

      <TabsContent value="pulls" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <PullRequestsPanel project={name} onSelect={onSelect} onOpenAccount={() => setWhere('general')} />
        </div>
      </TabsContent>

      <TabsContent value="media" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <ProjectMediaPanel project={name} />
        </div>
      </TabsContent>

      <TabsContent value="settings" className="flex flex-col">
        <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
          <SettingsSections label={`${name}'s settings`} sections={projectSettingsSections} value={section} onValueChange={setWhere}>
            {section === 'repository' && (
              <div className="mx-auto grid w-full max-w-3xl grid-cols-1 gap-6 px-4 py-6 md:px-8 md:py-7">
                <Card title="Repository" icon={FolderGit2}>
                  <Row label="Folder" mono>
                    <span className="truncate" title={project.root}>
                      {project.root}
                    </span>
                  </Row>
                  <Row label="New agents from" mono>
                    {project.branch}
                  </Row>
                  <Row label="Env files" mono>
                    {project.envFiles.length > 0 ? project.envFiles.join(', ') : 'none'}
                  </Row>
                </Card>
                <ProjectBasePanel project={project} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
              </div>
            )}
            {section === 'general' && (
              <div className="mx-auto w-full max-w-3xl px-4 py-6 md:px-8 md:py-7">
                <ProjectSettings project={project} />
              </div>
            )}
            {section === 'brief' && (
              <div className="mx-auto w-full max-w-3xl px-4 py-6 md:px-8 md:py-7">
                <ProjectNotes project={name} />
                <Card
                  className="mt-4"
                  title="Agent brief"
                  icon={FileText}
                  description="What every agent is told about its machine, its branch and this project's notes. Agents set the project up themselves, the way a new developer would."
                  action={
                    <Button variant="ghost" size="sm" onClick={() => setShowBrief((v) => !v)}>
                      {showBrief ? 'Hide' : 'Show'}
                    </Button>
                  }
                >
                  {showBrief && (
                    <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-xl border border-line-faint bg-sunken p-4 font-mono text-[12px] leading-relaxed text-tertiary">
                      {brief.data ?? 'Loading…'}
                    </pre>
                  )}
                </Card>
              </div>
            )}
            {section === 'memory' && <ProjectMemoryPanel project={name} onOpenMedia={() => setWhere('media')} />}
            {section === 'tokens' && (
              <div className="mx-auto w-full max-w-6xl px-4 py-6 md:px-8 md:py-7">
                <TokensPanel project={name} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
              </div>
            )}
            {section === 'secrets' && <SecretsTab target={name} />}
            {section === 'connectors' && <ConnectorsTab target={name} />}
          </SettingsSections>
        </div>
      </TabsContent>

      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={`Remove ${name}?`}
        description="AgentBox forgets the project. The repository and its branches aren't touched."
        confirmLabel="Remove"
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
        title="Free the finished agents' machines?"
        description="Their machines are shut down, which frees the memory they hold. Nothing is lost: each agent's work stays on its branch, its worktree stays on disk, and starting it again takes seconds. Agents that are still working, or have uncommitted changes, are left alone."
        confirmLabel="Free them"
        onConfirm={async () => {
          await retire.mutateAsync();
        }}
      />
    </Tabs>
  );
}
