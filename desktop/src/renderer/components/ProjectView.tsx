import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, Check, Coins, Ellipsis, FileText, FolderGit2, FolderOpen, GitPullRequest, Image, KeyRound, LoaderCircle, MessagesSquare, NotebookPen, Plus, SlidersHorizontal, Trash, Users } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { countFeature, projectTabFeatures } from '../lib/usageStats';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { ChatHeaderControls } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { FleetPanel } from './FleetPanel';
import { leadAgentFrom, ProjectChatPanel } from './ProjectChatPanel';
import { ProjectBasePanel } from './ProjectBasePanel';
import { ProjectMediaPanel } from './ProjectMediaPanel';
import { ProjectMemoryPanel } from './ProjectMemoryPanel';
import { ProjectSettings } from './ProjectSettings';
import { PullRequestsPanel } from './PullRequestsPanel';
import { SecretsTab } from './SecretsTab';
import { TokensPanel } from './TokensPanel';
import { Button } from './ui/button';
import { Card, Notice, Row } from './ui/card';
import { Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Select, SelectOption } from './ui/select';
import { SettingNote, SettingRow, SettingsGroup } from './ui/settings';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

// ClaudeAccountPicker chooses which stored Claude Code login this project's new
// agents get. Agents that already exist keep the token they were created with.
function ClaudeAccountPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const fleet = useQuery({ queryKey: ['fleet', project.name], queryFn: () => api.fleet(project.name) });
  const accounts = auth.data?.claudeAccounts ?? [];
  const fallback = accounts.find((a) => a.default)?.name;
  const [confirm, setConfirm] = useState<{ account: string; from: string; count: number } | null>(null);
  const pick = useMutation({
    mutationFn: ({ claudeAccount, moveClaudeAgents }: { claudeAccount: string; moveClaudeAgents?: boolean }) =>
      api.updateProject(project.name, { claudeAccount, moveClaudeAgents }),
    onSuccess: async (updated) => {
      toast(
        updated.claudeAccount
          ? `New agents of ${updated.name} use the Claude Code account "${updated.claudeAccount}"`
          : `New agents of ${updated.name} use this machine's default Claude Code account`,
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['fleet', project.name] });
    },
  });

  const choose = (value: string) => {
    if (value === project.claudeAccount) {
      pick.mutate({ claudeAccount: value });
      return;
    }
    const from = project.claudeAccount || fallback || '';
    const count = (fleet.data?.agents ?? []).filter((a) => a.ai === 'claude' && a.claudeAccount === from).length;
    if (count === 0) {
      pick.mutate({ claudeAccount: value });
      return;
    }
    setConfirm({ account: value, from, count });
  };

  return (
    <>
      <SettingRow
        label="Claude Code account"
        description={
          auth.isPending
            ? 'Loading…'
            : accounts.length === 0
              ? 'No account stored yet — add one in Settings.'
              : 'The login new agents get. Agents that already exist keep the one they were made with.'
        }
        control={
          accounts.length > 0 && (
            <Select aria-label="Claude Code account" value={project.claudeAccount} disabled={pick.isPending} onChange={choose}>
              <SelectOption value="">Default{fallback ? ` (${fallback})` : ''}</SelectOption>
              {accounts.filter((account) => allowed(project, account.name)).map((account) => (
                <SelectOption key={account.name} value={account.name}>
                  {account.name}
                </SelectOption>
              ))}
            </Select>
          )
        }
      >
        {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
      </SettingRow>
      {confirm && (
        <MoveAgentsDialog
          open
          onOpenChange={(open) => {
            if (!open) setConfirm(null);
          }}
          count={confirm.count}
          from={confirm.from}
          to={confirm.account || fallback || ''}
          onChoose={(move) => pick.mutateAsync({ claudeAccount: confirm.account, moveClaudeAgents: move })}
        />
      )}
    </>
  );
}

function allowed(project: T.Project, name: string) {
  return project.claudeAccounts.length === 0 || project.claudeAccounts.includes(name);
}

// accountLabel names an account the way a question reads best: quoted, or
// "the default account" for the machine's own.
function accountLabel(name: string) {
  return name ? `"${name}"` : 'the default account';
}

// MoveAgentsDialog asks whether a project's agents still on the account
// being replaced should move to the new one too, or stay where they are.
// Both choices go ahead with the account change; only whether the agents
// come with it differs, so this isn't a Cancel/Confirm ConfirmDialog.
function MoveAgentsDialog({
  open,
  onOpenChange,
  count,
  from,
  to,
  onChoose,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  count: number;
  from: string;
  to: string;
  onChoose: (move: boolean) => Promise<unknown>;
}) {
  const [pending, setPending] = useState<'move' | 'leave' | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setPending(null);
      setError(null);
    }
  }, [open]);

  const choose = async (move: boolean) => {
    setPending(move ? 'move' : 'leave');
    setError(null);
    try {
      await onChoose(move);
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setPending(null);
    }
  };

  const plural = count === 1 ? '' : 's';
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move its agents too?</DialogTitle>
          <DialogDescription>
            {count} agent{plural} still {count === 1 ? 'uses' : 'use'} {accountLabel(from)}. Move {count === 1 ? 'it' : 'them'} to {accountLabel(to)} too, or
            leave {count === 1 ? 'it' : 'them'} where {count === 1 ? 'it is' : 'they are'}?
          </DialogDescription>
        </DialogHeader>
        {error && <Notice>{error}</Notice>}
        <DialogFooter>
          <Button variant="ghost" disabled={pending !== null} onClick={() => void choose(false)}>
            {pending === 'leave' && <LoaderCircle className="animate-spin" />}
            Leave them
          </Button>
          <Button variant="primary" disabled={pending !== null} onClick={() => void choose(true)}>
            {pending === 'move' && <LoaderCircle className="animate-spin" />}
            Move {count} agent{plural} too
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ClaudeAccountsPicker limits which of the machine's accounts this project's
// agents may use; none ticked off means every one. The project's own
// account can't be left out, so its chip is locked.
function ClaudeAccountsPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = auth.data?.claudeAccounts ?? [];
  const fallback = accounts.find((a) => a.default)?.name;
  const save = useMutation({
    mutationFn: (claudeAccounts: string[]) => api.updateProject(project.name, { claudeAccounts }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  if (accounts.length < 2) {
    return (
      <SettingRow
        label="Allowed accounts"
        description={auth.isPending ? 'Loading…' : `Every account — there is only ${accounts.length === 1 ? 'one' : 'none'} on this machine.`}
      />
    );
  }
  const toggle = (name: string) => {
    const current = accounts.map((a) => a.name).filter((n) => allowed(project, n));
    const next = current.includes(name) ? current.filter((n) => n !== name) : [...current, name];
    if (next.length === 0) return;
    save.mutate(next.length === accounts.length ? [] : next);
  };
  const effective = project.claudeAccount || fallback;
  return (
    <SettingRow
      label="Allowed accounts"
      description={
        <>
          Which of this machine's Claude Code accounts this project's agents may use.{' '}
          {project.claudeAccounts.length === 0 ? 'Every account allowed.' : `${project.claudeAccounts.length} of ${accounts.length} allowed.`} Agents that
          already have an account keep it.
        </>
      }
    >
      <div className="flex min-w-0 flex-wrap items-center gap-1.5" role="group" aria-label="Allowed Claude Code accounts">
        {accounts.map((account) => {
          const on = allowed(project, account.name);
          const locked = account.name === project.claudeAccount;
          return (
            <button
              key={account.name}
              type="button"
              aria-pressed={on}
              disabled={save.isPending || (on && locked)}
              title={locked ? "The project's own account: pick another one above to leave it out" : undefined}
              onClick={() => toggle(account.name)}
              className={cn(
                'flex h-8 min-w-0 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[13px] text-muted transition hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 disabled:cursor-default',
                on && 'border-transparent bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]',
              )}
            >
              {on ? <Check className="size-3.5 shrink-0" /> : <span className="size-3.5 shrink-0" />}
              <span className="truncate">{account.name}</span>
              {account.name === effective && <span className="text-subtle">default</span>}
            </button>
          );
        })}
      </div>
      {effective && !allowed(project, effective) && (
        <SettingNote tone="warning">New agents default to {effective}, which isn't allowed: pick an allowed account above.</SettingNote>
      )}
      {save.error && <SettingNote tone="error">{errorMessage(save.error)}</SettingNote>}
    </SettingRow>
  );
}

// GitHubAccountPicker chooses which stored GitHub login this project's new
// agents get. Agents that already exist keep the token they were created with.
export function GitHubAccountPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const fleet = useQuery({ queryKey: ['fleet', project.name], queryFn: () => api.fleet(project.name) });
  const accounts = auth.data?.githubAccounts ?? [];
  const fallback = accounts.find((a) => a.default)?.name;
  const [confirm, setConfirm] = useState<{ account: string; from: string; count: number } | null>(null);
  const pick = useMutation({
    mutationFn: ({ githubAccount, moveGitHubAgents }: { githubAccount: string; moveGitHubAgents?: boolean }) =>
      api.updateProject(project.name, { githubAccount, moveGitHubAgents }),
    onSuccess: async (updated) => {
      toast(
        updated.githubAccount
          ? `New agents of ${updated.name} use the GitHub account "${updated.githubAccount}"`
          : `New agents of ${updated.name} use this machine's default GitHub account`,
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      // What the Pull requests tab and the fleet are showing was read with the
      // other account, and the daemon has just dropped it: a repository that
      // one couldn't see may be this one's everyday repository. Without this
      // they keep the old answer until their next poll, which is a minute.
      await queryClient.invalidateQueries({ queryKey: ['pulls', project.name] });
      await queryClient.invalidateQueries({ queryKey: ['fleet', project.name] });
    },
  });

  const choose = (value: string) => {
    if (value === project.githubAccount) {
      pick.mutate({ githubAccount: value });
      return;
    }
    const from = project.githubAccount || fallback || '';
    const count = (fleet.data?.agents ?? []).filter((a) => a.githubAccount === from).length;
    if (count === 0) {
      pick.mutate({ githubAccount: value });
      return;
    }
    setConfirm({ account: value, from, count });
  };

  return (
    <>
      <SettingRow
        label="GitHub account"
        description={
          auth.isPending
            ? 'Loading…'
            : accounts.length === 0
              ? 'No account stored yet — add one in Settings.'
              : 'The login new agents push and open pull requests with. Agents that already exist keep the one they were made with.'
        }
        control={
          accounts.length > 0 && (
            <Select aria-label="GitHub account" value={project.githubAccount} disabled={pick.isPending} onChange={choose}>
              <SelectOption value="">Default{fallback ? ` (${fallback})` : ''}</SelectOption>
              {/* Every stored account: the project's allow-list is for Claude Code
                  accounts, and filtering these by it left only Default (and the
                  picked account showing as "Select…") on a project that has one. */}
              {accounts.map((account) => (
                <SelectOption key={account.name} value={account.name}>
                  {account.name}
                </SelectOption>
              ))}
              {/* An account the project picked and that was removed since: it is
                  still what the project names, so it shows rather than "Select…". */}
              {project.githubAccount && !accounts.some((a) => a.name === project.githubAccount) && (
                <SelectOption value={project.githubAccount} disabled>
                  {project.githubAccount} (removed)
                </SelectOption>
              )}
            </Select>
          )
        }
      >
        {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
      </SettingRow>
      {confirm && (
        <MoveAgentsDialog
          open
          onOpenChange={(open) => {
            if (!open) setConfirm(null);
          }}
          count={confirm.count}
          from={confirm.from}
          to={confirm.account || fallback || ''}
          onChoose={(move) => pick.mutateAsync({ githubAccount: confirm.account, moveGitHubAgents: move })}
        />
      )}
    </>
  );
}

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
type ProjectTab = 'chat' | 'agents' | 'pulls' | 'media' | 'memory' | 'tokens' | 'secrets' | 'overview';
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

      <TabsContent value="pulls" className="overflow-y-auto">
        <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
          <PullRequestsPanel project={name} onSelect={onSelect} onOpenAccount={() => setTab('overview')} />
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
                <SettingsGroup title="Accounts" description="The logins this project's new agents get, from the ones stored in Settings.">
                  <ClaudeAccountPicker project={project} />
                  <ClaudeAccountsPicker project={project} />
                  <GitHubAccountPicker project={project} />
                </SettingsGroup>
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
