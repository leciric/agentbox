import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Camera,
  CircleX,
  Copy,
  Ellipsis,
  FolderOpen,
  GitBranch,
  Hash,
  Images,
  KeyRound,
  LayoutDashboard,
  LoaderCircle,
  MessageSquare,
  Monitor,
  Network,
  Pause,
  Pencil,
  Play,
  Smartphone,
  Square,
  SquareTerminal,
} from 'lucide-react';
import { useEffect, useRef, useState, type ComponentType, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api, type AgentAction } from '../lib/api';
import { disposeTerminal } from '../lib/terminals';
import { cn, errorMessage } from '../lib/utils';
import { AndroidTab } from './AndroidTab';
import { BrowserTab } from './BrowserTab';
import { ChatHeaderControls, ChatTab } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { MediaTab } from './MediaTab';
import { OverviewTab } from './OverviewTab';
import { SecretsTab } from './SecretsTab';
import { SnapshotsTab } from './SnapshotsTab';
import { AgentAvatar, AIIcon, aiLabel, StateBadge } from './state';
import { TerminalTab } from './TerminalTab';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Label } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from './ui/menu';
import { Switch } from './ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Tip } from './ui/tooltip';

export type AgentTab = 'chat' | 'terminal' | 'browser' | 'android' | 'media' | 'overview' | 'secrets' | 'snapshots';

export function AgentView({
  agentRef,
  tab,
  onTab,
  onSelect,
}: {
  agentRef: string;
  tab?: AgentTab; // unset opens the agent's first tab
  onTab: (tab: AgentTab) => void;
  onSelect: (view: View) => void;
}) {
  const queryClient = useQueryClient();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const media = useQuery({ queryKey: ['media', agentRef], queryFn: () => api.media(agentRef) });
  const agent = agents.data?.find((a) => a.ref === agentRef);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const android = useQuery({ queryKey: ['android', agentRef], queryFn: () => api.android(agentRef), enabled: agent?.state === 'running' });
  // Only Android projects get the tab, unless an emulator was started anyway.
  const hasAndroid = projects.data?.some((p) => p.name === agent?.project && p.android) || android.data?.running === true;
  const [destroying, setDestroying] = useState(false);
  const [force, setForce] = useState(false);
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [deleteMedia, setDeleteMedia] = useState(false);
  const [editingTitle, setEditingTitle] = useState(false);

  const replace = (updated: T.Agent) =>
    queryClient.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === updated.ref ? updated : a)));
  const action = useMutation({ mutationFn: (name: AgentAction) => api.agentAction(agentRef, name), onSuccess: replace });
  const rename = useMutation({
    mutationFn: (title: string) => api.updateAgent(agentRef, { title }),
    onSuccess: (updated) => {
      replace(updated);
      toast(updated.title ? `Titled “${updated.title}”` : 'Title cleared');
    },
  });

  if (!agent) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-subtle">
        <LoaderCircle className="mr-2 size-4 animate-spin" />
        Loading {agentRef}…
      </div>
    );
  }

  const run = (name: AgentAction) => action.mutate(name);
  const busy = action.isPending;
  const mediaCount = media.data?.length ?? 0;
  const error = action.error ?? rename.error;
  // An agent you use through the chat opens on it; one you use from the terminal has no Chat tab.
  const usesChat = agent.ai !== 'none' && agent.interface === 'chat';
  const active: AgentTab = !tab || (tab === 'chat' && !usesChat) ? (usesChat ? 'chat' : 'terminal') : tab;

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 md:flex-nowrap md:px-6">
        <AgentAvatar ai={agent.ai} state={agent.state} className="size-8" />
        <AgentTitle agent={agent} editing={editingTitle} onEditing={setEditingTitle} onSave={(title) => rename.mutate(title)} />
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <StateBadge state={agent.state} />
          <Chip icon={GitBranch} mono label="Branch">
            {agent.branch}
          </Chip>
          <Chip icon={Hash} mono label="Agent name">
            {agent.name}
          </Chip>
          {agent.ip && (
            <Chip
              icon={Network}
              mono
              label="Copy the IP address"
              onClick={() => {
                window.agentbox.copyText(agent.ip);
                toast('Copied the IP address');
              }}
            >
              {agent.ip}
            </Chip>
          )}
          <Chip icon={({ className }) => <AIIcon ai={agent.ai} className={className} />} label="AI tool">
            {aiLabel(agent.ai)}
            {usesChat ? ' · chat' : ''}
            {agent.autonomous ? ' · autonomous' : ''}
          </Chip>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          {busy && <LoaderCircle className="size-4 animate-spin text-subtle" />}
          {agent.state === 'running' && (
            <Button size="sm" disabled={busy} onClick={() => run('pause')}>
              <Pause />
              Pause
            </Button>
          )}
          {agent.state === 'paused' && (
            <Button size="sm" variant="primary" disabled={busy} onClick={() => run('resume')}>
              <Play />
              Resume
            </Button>
          )}
          {agent.state === 'stopped' && (
            <Button size="sm" variant="primary" disabled={busy} onClick={() => run('start')}>
              <Play />
              Start
            </Button>
          )}
          {(agent.state === 'running' || agent.state === 'paused') && (
            <Button size="sm" disabled={busy} onClick={() => run('stop')}>
              <Square />
              Stop
            </Button>
          )}
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon-sm" variant="ghost" aria-label="More actions">
                <Ellipsis />
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuItem icon={Pencil} onSelect={() => setEditingTitle(true)}>
                Edit title
              </MenuItem>
              <MenuItem icon={FolderOpen} onSelect={() => void window.agentbox.openPath(agent.worktree)}>
                Open worktree folder
              </MenuItem>
              <MenuItem
                icon={Copy}
                onSelect={() => {
                  window.agentbox.copyText(agent.worktree);
                  toast('Copied the worktree path');
                }}
              >
                Copy worktree path
              </MenuItem>
              <MenuItem
                icon={SquareTerminal}
                onSelect={() => {
                  window.agentbox.copyText(`agentbox shell ${agent.ref}`);
                  toast('Copied', { description: `agentbox shell ${agent.ref}` });
                }}
              >
                Copy shell command
              </MenuItem>
              <MenuSeparator />
              <MenuItem
                icon={CircleX}
                destructive
                onSelect={() => {
                  setForce(agent.state === 'incomplete' || agent.state === 'missing');
                  setDeleteBranch(false);
                  setDeleteMedia(false);
                  setDestroying(true);
                }}
              >
                Destroy agent…
              </MenuItem>
            </MenuContent>
          </Menu>
        </div>
      </div>
      {error && (
        <div className="px-4 pb-3 md:px-6">
          <Notice>{errorMessage(error)}</Notice>
        </div>
      )}

      <Tabs value={active} onValueChange={(value) => onTab(value as AgentTab)} className="flex min-h-0 flex-1 flex-col">
        <div className="flex shrink-0 items-center gap-3 border-t border-line-faint px-4 py-2 md:px-6">
          <TabsList className="min-w-0 flex-1 overflow-x-auto">
            {usesChat && (
              <TabsTrigger value="chat">
                <MessageSquare />
                Chat
                {(agent.chat === 'running' || agent.chat === 'waiting') && (
                  <span
                    className={cn('size-1.5 rounded-full', agent.chat === 'waiting' ? 'bg-amber-400' : 'animate-pulse bg-sky-400')}
                    aria-label={agent.chat === 'waiting' ? 'waiting for you' : 'working'}
                  />
                )}
              </TabsTrigger>
            )}
            <TabsTrigger value="terminal">
              <SquareTerminal />
              Terminal
            </TabsTrigger>
            <TabsTrigger value="browser">
              <Monitor />
              Desktop
            </TabsTrigger>
            {hasAndroid && (
              <TabsTrigger value="android">
                <Smartphone />
                Android
                {android.data?.booted && <span className="size-1.5 rounded-full bg-emerald-400" aria-label="running" />}
              </TabsTrigger>
            )}
            <TabsTrigger value="media">
              <Images />
              Media
              {mediaCount > 0 && <span className="rounded-full bg-brand-500/20 px-1.5 text-[10px] tabular-nums text-brand-200">{mediaCount}</span>}
            </TabsTrigger>
            <TabsTrigger value="overview">
              <LayoutDashboard />
              Overview
            </TabsTrigger>
            <TabsTrigger value="secrets">
              <KeyRound />
              Secrets
            </TabsTrigger>
            <TabsTrigger value="snapshots">
              <Camera />
              Snapshots
            </TabsTrigger>
          </TabsList>
          {usesChat && active === 'chat' && (
            <div className="flex shrink-0 items-center gap-2">
              <ChatHeaderControls agent={agent} />
            </div>
          )}
        </div>
        <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line bg-sunken shadow-[0_30px_80px_-40px_var(--ab-shadow-deep)] md:mx-6 md:mb-6">
          {usesChat && (
            <TabsContent value="chat" className="flex flex-col">
              <ChatTab agent={agent} starting={busy} onStart={() => run(agent.state === 'paused' ? 'resume' : 'start')} />
            </TabsContent>
          )}
          <TabsContent value="terminal" className="flex flex-col">
            <TerminalTab agent={agent} starting={busy} onStart={() => run(agent.state === 'paused' ? 'resume' : 'start')} />
          </TabsContent>
          <TabsContent value="browser" className="flex flex-col">
            <BrowserTab agent={agent} onOpenMedia={() => onTab('media')} />
          </TabsContent>
          {hasAndroid && (
            <TabsContent value="android" className="flex flex-col">
              <AndroidTab agent={agent} onOpenMedia={() => onTab('media')} onSetup={() => onSelect({ kind: 'settings' })} />
            </TabsContent>
          )}
          <TabsContent value="media" className="flex flex-col">
            <MediaTab agent={agent} />
          </TabsContent>
          <TabsContent value="overview">
            <OverviewTab agent={agent} />
          </TabsContent>
          <TabsContent value="secrets" className="flex flex-col">
            <SecretsTab target={agent.ref} />
          </TabsContent>
          <TabsContent value="snapshots">
            <SnapshotsTab agent={agent} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
          </TabsContent>
        </div>
      </Tabs>

      <ConfirmDialog
        open={destroying}
        onOpenChange={setDestroying}
        title={`Destroy ${agent.title || agent.ref}?`}
        description="Deletes the machine, its snapshots and the worktree. The branch is deleted too once it's merged or pushed; otherwise its commits stay on it. Media stays in the project's media view for as long as Settings keeps it, unless you delete it now."
        confirmLabel="Destroy"
        destructive
        onConfirm={async () => {
          await api.destroyAgent(agent.ref, force, deleteBranch, deleteMedia);
          disposeTerminal(agent.ref);
          await queryClient.invalidateQueries({ queryKey: ['agents'] });
          onSelect({ kind: 'project', project: agent.project });
        }}
      >
        <div className="grid gap-3 rounded-xl border border-line bg-surface-faint p-3.5">
          <div className="flex items-center gap-3">
            <Switch id="destroy-force" checked={force} onCheckedChange={setForce} />
            <Label htmlFor="destroy-force" className="font-normal">
              Discard uncommitted changes
            </Label>
          </div>
          <div className="flex items-center gap-3">
            <Switch id="destroy-branch" checked={deleteBranch} onCheckedChange={setDeleteBranch} />
            <Label htmlFor="destroy-branch" className="font-normal">
              Delete the branch <Code>{agent.branch}</Code> even if it isn't merged or pushed
            </Label>
          </div>
          <div className="flex items-center gap-3">
            <Switch id="destroy-media" checked={deleteMedia} onCheckedChange={setDeleteMedia} />
            <Label htmlFor="destroy-media" className="font-normal">
              Also delete its media, instead of keeping it in the project's media view
            </Label>
          </div>
        </div>
      </ConfirmDialog>
    </div>
  );
}

function AgentTitle({ agent, editing, onEditing, onSave }: { agent: T.Agent; editing: boolean; onEditing: (editing: boolean) => void; onSave: (title: string) => void }) {
  const [value, setValue] = useState(agent.title);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!editing) return;
    setValue(agent.title);
    requestAnimationFrame(() => input.current?.select());
  }, [editing, agent.title]);

  const finish = (save: boolean) => {
    if (save && value.trim() !== agent.title) onSave(value);
    onEditing(false);
  };

  if (editing) {
    return (
      <input
        ref={input}
        aria-label="Agent title"
        value={value}
        maxLength={80}
        placeholder={`What is ${agent.name} working on?`}
        onChange={(event) => setValue(event.target.value)}
        onBlur={() => finish(true)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') finish(true);
          if (event.key === 'Escape') finish(false);
        }}
        className="w-full max-w-64 rounded-lg border border-brand-400/40 bg-well px-2 py-0.5 text-[15px] font-semibold tracking-tight text-title outline-none ring-2 ring-brand-400/20 placeholder:text-faint"
      />
    );
  }
  return (
    <button className="group flex min-w-0 max-w-64 shrink items-center gap-1.5 text-left" onClick={() => onEditing(true)} aria-label="Edit title">
      <span className="truncate text-[15px] font-semibold tracking-tight text-title">{agent.title || agent.name}</span>
      {!agent.title && <span className="hidden shrink-0 text-xs text-faint opacity-0 transition group-hover:opacity-100 sm:inline">Add a title</span>}
      <Pencil className="size-3.5 shrink-0 text-faint opacity-0 transition group-hover:opacity-100" />
    </button>
  );
}

function Chip({
  icon: Icon,
  mono,
  label,
  onClick,
  children,
}: {
  icon: ComponentType<{ className?: string }>;
  mono?: boolean;
  label: string;
  onClick?: () => void;
  children: ReactNode;
}) {
  const className = cn(
    'inline-flex max-w-[280px] items-center gap-1.5 rounded-md border border-line bg-surface-faint px-2 py-0.5 text-[11.5px] text-muted',
    mono && 'font-mono text-[11px]',
    onClick && 'transition hover:bg-surface-raised hover:text-secondary',
  );
  const content = (
    <>
      <Icon className="size-3 shrink-0 text-subtle" />
      <span className="truncate">{children}</span>
    </>
  );
  return (
    <Tip label={label}>
      {onClick ? (
        <button className={className} onClick={onClick}>
          {content}
        </button>
      ) : (
        <span className={className}>{content}</span>
      )}
    </Tip>
  );
}
