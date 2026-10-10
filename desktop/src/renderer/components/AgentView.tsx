import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowUpToLine,
  Box,
  Camera,
  CircleX,
  Copy,
  Ellipsis,
  FolderGit2,
  FolderOpen,
  GitBranch,
  Hash,
  Images,
  KeyRound,
  ListTodo,
  LoaderCircle,
  MessageSquare,
  Monitor,
  Network,
  Pause,
  Pencil,
  Play,
  Plug,
  SlidersHorizontal,
  Smartphone,
  Square,
  SquareTerminal,
} from 'lucide-react';
import { useEffect, useRef, useState, type ComponentType, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { lifecycleActions, usesChat, type LifecycleAction } from '../lib/agentActions';
import { waitingLine } from '../lib/agentSize';
import { api, type AgentAction } from '../lib/api';
import { useChatOpenAt } from '../lib/reveal';
import { useT, type MessageKey } from '../lib/i18n';
import { useProjectName } from '../lib/useProjectName';
import { agentPlace, type AgentPlaceName, type AgentSection, type AgentTab } from '../lib/tabs';
import { agentTabFeatures, countFeature } from '../lib/usageStats';
import { cn, errorMessage } from '../lib/utils';
import { AndroidTab } from './AndroidTab';
import { BrowserTab } from './BrowserTab';
import { ChatHeaderControls, ChatTab } from './chat/ChatTab';
import { ConnectorsTab } from './ConnectorsTab';
import { DestroyAgentDialog } from './DestroyAgentDialog';
import { MediaTab } from './MediaTab';
import { OverviewTab } from './OverviewTab';
import { SecretsTab } from './SecretsTab';
import { SettingsSections, type SettingsSection } from './SettingsSections';
import { SnapshotsTab } from './SnapshotsTab';
import { AIIcon, aiLabel, LiveAgentAvatar, StateBadge } from './state';
import { TerminalTab } from './TerminalTab';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from './ui/menu';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Tip } from './ui/tooltip';

// An agent's Settings tab: what it says about the agent's machine, code and AI
// tool (OverviewTab), and the secrets and connectors only it gets.
const agentSettingsSections = (t: ReturnType<typeof useT>): SettingsSection<AgentSection>[] => [
  { id: 'machine', title: t('agent.section.machine'), icon: Box },
  { id: 'code', title: t('agent.section.code'), icon: GitBranch },
  { id: 'ai', title: t('agent.section.ai'), icon: SquareTerminal },
  { id: 'secrets', title: t('agent.section.secrets'), icon: KeyRound },
  { id: 'connectors', title: t('agent.section.connectors'), icon: Plug },
];

export function AgentView({
  agentRef,
  tab,
  onTab,
  onSelect,
}: {
  agentRef: string;
  tab?: AgentPlaceName; // a tab or a section of Settings (lib/tabs.ts); unset opens the agent's first tab
  onTab: (tab: AgentPlaceName) => void;
  onSelect: (view: View) => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const media = useQuery({ queryKey: ['media', agentRef], queryFn: () => api.media(agentRef) });
  // A message of this chat the search palette opened.
  const [refProject, refName] = agentRef.split('/');
  const openAt = useChatOpenAt(refProject, refName);
  const agent = agents.data?.find((a) => a.ref === agentRef);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const android = useQuery({ queryKey: ['android', agentRef], queryFn: () => api.android(agentRef), enabled: agent?.state === 'running' });
  // Only Android projects get the tab, unless an emulator was started anyway.
  const hasAndroid = projects.data?.some((p) => p.name === agent?.project && p.android) || android.data?.running === true;
  const [destroying, setDestroying] = useState(false);
  const [editingTitle, setEditingTitle] = useState(false);
  // A tab opened on purpose, by its tab, a link or the agent's menu; the one
  // an agent opens on by itself isn't counted.
  useEffect(() => countFeature(tab && agentTabFeatures[tab]), [tab, agentRef]);

  const replace = (updated: T.Agent) =>
    queryClient.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === updated.ref ? updated : a)));
  const action = useMutation({ mutationFn: (name: AgentAction) => api.agentAction(agentRef, name), onSuccess: replace });
  const rename = useMutation({
    mutationFn: (title: string) => api.updateAgent(agentRef, { title }),
    onSuccess: (updated) => {
      replace(updated);
      toast(updated.title ? t('agent.view.titled', { title: updated.title }) : t('agent.view.titleCleared'));
    },
  });

  if (!agent) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-subtle">
        <LoaderCircle className="mr-2 size-4 animate-spin" />
        {t('agent.view.loading', { ref: agentRef })}
      </div>
    );
  }

  // A queued agent has no machine yet: nothing here — chat, terminal, browser
  // — has anything to attach to, so it gets its own quiet placeholder instead
  // of the tabs below.
  if (agent.state === 'queued') {
    return <QueuedAgentPlaceholder agent={agent} onSelect={onSelect} />;
  }

  const run = (name: AgentAction) => action.mutate(name);
  const busy = action.isPending;
  const mediaCount = media.data?.length ?? 0;
  const error = action.error ?? rename.error;
  // An agent you use through the chat opens on it; one you use from the terminal has no Chat tab.
  const chatty = usesChat(agent);
  const place = agentPlace(tab);
  const active: AgentTab = !place.tab || (place.tab === 'chat' && !chatty) ? (chatty ? 'chat' : 'terminal') : place.tab;

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 md:flex-nowrap md:px-6">
        <LiveAgentAvatar agent={agent} />
        <AgentTitle agent={agent} editing={editingTitle} onEditing={setEditingTitle} onSave={(title) => rename.mutate(title)} />
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <StateBadge state={agent.state} />
          <Chip icon={GitBranch} mono label={t('agent.view.branch')}>
            {agent.branch}
          </Chip>
          <Chip icon={Hash} mono label={t('agent.view.agentName')}>
            {agent.name}
          </Chip>
          {agent.ip && (
            <Chip
              icon={Network}
              mono
              label={t('agent.view.copyIp')}
              onClick={() => {
                window.agentbox.copyText(agent.ip);
                toast(t('agent.view.copiedIp'));
              }}
            >
              {agent.ip}
            </Chip>
          )}
          <Chip icon={({ className }) => <AIIcon ai={agent.ai} className={className} />} label={t('agent.view.aiTool')}>
            {aiLabel(agent.ai)}
            {chatty ? t('agent.view.chatSuffix') : ''}
            {agent.autonomous ? t('agent.view.autonomousSuffix') : ''}
          </Chip>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          {busy && <LoaderCircle className="size-4 animate-spin text-subtle" />}
          {lifecycleActions(agent.state).map((name) => {
            const { icon: Icon, label, primary } = lifecycleButton[name];
            return (
              <Button key={name} size="sm" variant={primary ? 'primary' : undefined} disabled={busy} onClick={() => run(name)}>
                <Icon />
                {t(label)}
              </Button>
            );
          })}
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon-sm" variant="ghost" aria-label={t('agent.view.moreActions')}>
                <Ellipsis />
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuItem icon={Pencil} onSelect={() => setEditingTitle(true)}>
                {t('agent.view.editTitle')}
              </MenuItem>
              <MenuItem icon={FolderOpen} onSelect={() => void window.agentbox.openPath(agent.worktree)}>
                {t('agent.view.openWorktree')}
              </MenuItem>
              <MenuItem
                icon={Copy}
                onSelect={() => {
                  window.agentbox.copyText(agent.worktree);
                  toast(t('agent.view.copiedWorktree'));
                }}
              >
                {t('agent.view.copyWorktree')}
              </MenuItem>
              <MenuItem
                icon={SquareTerminal}
                onSelect={() => {
                  window.agentbox.copyText(`agentbox shell ${agent.ref}`);
                  toast(t('common.copied'), { description: `agentbox shell ${agent.ref}` });
                }}
              >
                {t('agent.view.copyShell')}
              </MenuItem>
              <MenuSeparator />
              <MenuItem
                icon={CircleX}
                destructive
                onSelect={() => setDestroying(true)}
              >
                {t('agent.view.destroy')}
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
            {chatty && (
              <TabsTrigger value="chat">
                <MessageSquare />
                {t('agent.tab.chat')}
                {(agent.chat === 'running' || agent.chat === 'waiting') && (
                  <span
                    className={cn('size-1.5 rounded-full', agent.chat === 'waiting' ? 'bg-amber-400' : 'animate-pulse bg-sky-400')}
                    aria-label={agent.chat === 'waiting' ? t('agent.view.waitingForYou') : t('agent.view.working')}
                  />
                )}
              </TabsTrigger>
            )}
            <TabsTrigger value="terminal">
              <SquareTerminal />
              {t('agent.tab.terminal')}
            </TabsTrigger>
            <TabsTrigger value="browser">
              <Monitor />
              {t('agent.tab.desktop')}
            </TabsTrigger>
            {hasAndroid && (
              <TabsTrigger value="android">
                <Smartphone />
                {t('agent.tab.android')}
                {android.data?.booted && <span className="size-1.5 rounded-full bg-emerald-400" aria-label={t('agent.view.running')} />}
              </TabsTrigger>
            )}
            <TabsTrigger value="media">
              <Images />
              {t('agent.tab.media')}
              {mediaCount > 0 && <span className="rounded-full bg-brand-500/20 px-1.5 text-[10px] tabular-nums text-brand-200">{mediaCount}</span>}
            </TabsTrigger>
            <TabsTrigger value="settings">
              <SlidersHorizontal />
              {t('agent.tab.settings')}
            </TabsTrigger>
            <TabsTrigger value="snapshots">
              <Camera />
              {t('agent.tab.snapshots')}
            </TabsTrigger>
          </TabsList>
          {chatty && active === 'chat' && (
            <div className="flex shrink-0 items-center gap-2">
              <ChatHeaderControls agent={agent} />
            </div>
          )}
        </div>
        <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line bg-sunken shadow-[0_30px_80px_-40px_var(--ab-shadow-deep)] md:mx-6 md:mb-6">
          {chatty && (
            <TabsContent value="chat" className="flex flex-col">
              <ChatTab agent={agent} starting={busy} onStart={() => run(agent.state === 'paused' ? 'resume' : 'start')} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} openAt={openAt} />
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
          <TabsContent value="settings" className="flex flex-col">
            <SettingsSections label={t('agent.view.settingsOf', { name: agent.title || agent.name })} sections={agentSettingsSections(t)} value={place.section} onValueChange={onTab}>
              {(place.section === 'machine' || place.section === 'code' || place.section === 'ai') && <OverviewTab agent={agent} section={place.section} />}
              {place.section === 'secrets' && <SecretsTab target={agent.ref} />}
              {place.section === 'connectors' && <ConnectorsTab target={agent.ref} />}
            </SettingsSections>
          </TabsContent>
          <TabsContent value="snapshots">
            <SnapshotsTab agent={agent} onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })} />
          </TabsContent>
        </div>
      </Tabs>

      <DestroyAgentDialog
        agent={agent}
        open={destroying}
        onOpenChange={setDestroying}
        onDestroyed={() => onSelect({ kind: 'project', project: agent.project })}
      />
    </div>
  );
}

// QueuedAgentPlaceholder is what a queued agent opens on: no machine exists
// yet, so there's no chat, terminal or browser to show — only its place in
// line and its task, read from the project's queue (GET /v1/queue), and the
// ways out of it (AgentContextMenu offers the same from the rail).
function QueuedAgentPlaceholder({ agent, onSelect }: { agent: T.Agent; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projectName = useProjectName(agent.project);
  const queue = useQuery({ queryKey: ['queue', agent.project], queryFn: () => api.queue(agent.project) });
  const entry = queue.data?.queued.find((q) => q.name === agent.name);

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ['agents'] });
    await queryClient.invalidateQueries({ queryKey: ['queue', agent.project] });
  };
  const moveToFront = useMutation({
    mutationFn: () => api.moveQueued(agent.project, agent.name, 1),
    onSuccess: () => void invalidate(),
    onError: (err) => toast.error(errorMessage(err)),
  });
  // Starts it whatever admission says: the way past a wait the user can see
  // is wrong, the VM having the memory free.
  const startNow = useMutation({
    mutationFn: () => api.startQueued(agent.project, agent.name),
    onSuccess: () => void invalidate(),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const remove = useMutation({
    mutationFn: () => api.removeQueued(agent.project, agent.name),
    onSuccess: () => {
      toast(t('agent.queued.removed'));
      void invalidate();
      onSelect({ kind: 'project', project: agent.project });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 md:flex-nowrap md:px-6">
        <LiveAgentAvatar agent={agent} />
        <span className="min-w-0 flex-1 truncate text-[15px] font-semibold tracking-tight text-title">{agent.title || agent.name}</span>
        <StateBadge state={agent.state} />
      </div>
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 px-6 pb-10 text-center">
        <span className="flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-surface-faint text-subtle">
          <ListTodo className="size-5" />
        </span>
        <div className="grid max-w-md gap-1.5">
          <p className="text-[14px] font-medium text-primary">
            {t('agent.queued.title', {
              position: agent.queuePosition ?? entry?.position ?? '?',
              reason: waitingLine(agent.waiting ?? entry?.waiting) || t('agent.queued.slotFree', { project: projectName }),
            })}
          </p>
          {entry?.task && <p className="text-[13px] leading-relaxed text-subtle">{entry.task}</p>}
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" disabled={startNow.isPending} onClick={() => startNow.mutate()}>
            <Play />
            {t('agent.queued.startNow')}
          </Button>
          <Button variant="ghost" size="sm" disabled={moveToFront.isPending || agent.queuePosition === 1} onClick={() => moveToFront.mutate()}>
            <ArrowUpToLine />
            {t('agent.queued.moveToFront')}
          </Button>
          <Button variant="ghost" size="sm" disabled={remove.isPending} onClick={() => remove.mutate()}>
            <CircleX />
            {t('agent.queued.remove')}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => onSelect({ kind: 'project', project: agent.project })}>
            <FolderGit2 />
            {t('agent.queued.back', { project: projectName })}
          </Button>
        </div>
      </div>
    </div>
  );
}

// The header's button for each lifecycle action lifecycleActions allows; the
// one that brings the agent back is the primary.
const lifecycleButton: Record<LifecycleAction, { icon: ComponentType; label: MessageKey; primary?: boolean }> = {
  pause: { icon: Pause, label: 'agent.action.pause' },
  resume: { icon: Play, label: 'agent.action.resume', primary: true },
  start: { icon: Play, label: 'common.start', primary: true },
  stop: { icon: Square, label: 'common.stop' },
};

function AgentTitle({ agent, editing, onEditing, onSave }: { agent: T.Agent; editing: boolean; onEditing: (editing: boolean) => void; onSave: (title: string) => void }) {
  const t = useT();
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
        aria-label={t('agent.view.agentTitle')}
        value={value}
        maxLength={80}
        placeholder={t('agent.view.titlePlaceholder', { name: agent.name })}
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
    <button className="group flex min-w-0 max-w-64 shrink items-center gap-1.5 text-left" onClick={() => onEditing(true)} aria-label={t('agent.view.editTitleLabel')}>
      <span className="truncate text-[15px] font-semibold tracking-tight text-title">{agent.title || agent.name}</span>
      {!agent.title && <span className="hidden shrink-0 text-xs text-faint opacity-0 transition group-hover:opacity-100 sm:inline">{t('agent.view.addTitle')}</span>}
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
