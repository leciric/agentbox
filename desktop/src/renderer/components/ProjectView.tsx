import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, Coins, Ellipsis, FileText, FolderGit2, FolderOpen, GitPullRequest, Image, KeyRound, ListTodo, MessagesSquare, NotebookPen, Plug, Plus, SlidersHorizontal, Trash, Users } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import { api } from '../lib/api';
import { countFeature, projectTabFeatures } from '../lib/usageStats';
import { errorMessage, timeAgo } from '../lib/utils';
import { ChatHeaderControls } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { ConnectorsTab } from './ConnectorsTab';
import { FleetPanel } from './FleetPanel';
import { leadAgentFrom, ProjectChatPanel } from './ProjectChatPanel';
import { ProjectBasePanel } from './ProjectBasePanel';
import { ProjectMediaPanel } from './ProjectMediaPanel';
import { ProjectMemoryPanel } from './ProjectMemoryPanel';
import { ProjectSettings } from './ProjectSettings';
import { ProjectTasksPanel } from './ProjectTasksPanel';
import { PullRequestsPanel } from './PullRequestsPanel';
import { SecretsTab } from './SecretsTab';
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
      toast(updated.text ? `Saved ${project}'s notes` : `Cleared ${project}'s notes`);
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
// the page showed before is the Overview tab.
type ProjectTab = 'chat' | 'agents' | 'tasks' | 'pulls' | 'media' | 'memory' | 'tokens' | 'secrets' | 'connectors' | 'overview';
type OverviewSection = 'repository' | 'settings' | 'brief';

export function ProjectView({ name, onSelect, onNewAgent }: { name: string; onSelect: (view: View) => void; onNewAgent: () => void }) {
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const [section, setSection] = useState<OverviewSection>('repository');
  const [showBrief, setShowBrief] = useState(false);
  const brief = useQuery({ queryKey: ['brief', name], queryFn: () => api.brief(name), enabled: section === 'brief' && showBrief });
  const [removing, setRemoving] = useState(false);
  const [tab, setTab] = useState<ProjectTab>('chat');
  useEffect(() => countFeature(projectTabFeatures[tab]), [tab, name]);
  const leadChat = useQuery({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), enabled: tab === 'chat' });
  const project = projects.data?.find((p) => p.name === name);
  const mine = agents.data?.filter((a) => a.project === name) ?? [];

  if (!project) {
    return <div className="p-6 text-sm text-subtle">{projects.isPending ? 'Loading…' : `There's no project named ${name}.`}</div>;
  }

  return (
    <Tabs value={tab} onValueChange={(value) => setTab(value as ProjectTab)} className="flex h-full min-h-0 flex-col">
      <div className="w-full shrink-0 px-4 pb-3 pt-4 md:px-8 md:pb-4 md:pt-5">
        <div className="flex flex-wrap items-center gap-3">
          <span className="flex size-9 items-center justify-center rounded-xl border border-line-strong bg-surface text-tertiary">
            <FolderGit2 className="size-4" />
          </span>
          <div className="min-w-0">
            <h1 className="truncate text-[15px] font-semibold tracking-tight text-title">{project.name}</h1>
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
            <TabsTrigger value="agents">
              <Users />
              Agents
              {mine.length > 0 && <span className="tabular-nums text-subtle">{mine.length}</span>}
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
            <TabsTrigger value="memory">
              <Brain />
              Memory
            </TabsTrigger>
            <TabsTrigger value="tokens">
              <Coins />
              Tokens
            </TabsTrigger>
            <TabsTrigger value="secrets">
              <KeyRound />
              Secrets
            </TabsTrigger>
            <TabsTrigger value="connectors">
              <Plug />
              Connectors
            </TabsTrigger>
            <TabsTrigger value="overview">
              <SlidersHorizontal />
              Overview
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

      <TabsContent value="agents" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <FleetPanel project={name} onSelect={onSelect} />
        </div>
      </TabsContent>

      <TabsContent value="tasks" className="overflow-y-auto">
        <ProjectTasksPanel project={name} onSelect={onSelect} onOpenChat={() => setTab('chat')} />
      </TabsContent>

      <TabsContent value="pulls" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <PullRequestsPanel project={name} onSelect={onSelect} onOpenAccount={() => {
            setTab('overview');
            setSection('settings');
          }} />
        </div>
      </TabsContent>

      <TabsContent value="media" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <ProjectMediaPanel project={name} />
        </div>
      </TabsContent>

      <TabsContent value="memory" className="overflow-y-auto">
        <ProjectMemoryPanel project={name} onOpenMedia={() => setTab('media')} />
      </TabsContent>

      <TabsContent value="tokens" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <TokensPanel project={name} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
        </div>
      </TabsContent>

      <TabsContent value="secrets" className="flex flex-col">
        <SecretsTab target={name} />
      </TabsContent>

      <TabsContent value="connectors" className="flex flex-col">
        <ConnectorsTab target={name} />
      </TabsContent>

      <TabsContent value="overview" className="overflow-y-auto">
        <div className="mx-auto max-w-3xl px-4 py-6 md:px-8 md:py-7">
          <Tabs value={section} onValueChange={(value) => setSection(value as OverviewSection)}>
            <TabsList className="mb-4">
              <TabsTrigger value="repository">
                <FolderGit2 />
                Repository
              </TabsTrigger>
              <TabsTrigger value="settings">
                <SlidersHorizontal />
                Settings
              </TabsTrigger>
              <TabsTrigger value="brief">
                <FileText />
                Brief
              </TabsTrigger>
            </TabsList>

            <TabsContent value="repository">
              <div className="grid gap-6">
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
            </TabsContent>

            <TabsContent value="settings">
              <ProjectSettings project={project} />
            </TabsContent>

            <TabsContent value="brief">
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
            </TabsContent>
          </Tabs>
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
    </Tabs>
  );
}
