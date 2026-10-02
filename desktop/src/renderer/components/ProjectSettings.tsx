import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, CircleAlert, GitBranch, LoaderCircle, Search, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { AgentModelAuto } from '../../shared/api';
import { agentSizes } from '../lib/agentSize';
import { api } from '../lib/api';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../lib/modelChoices';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { ModelByName } from './ModelByName';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';
import { Select, SelectOption, selectTrigger } from './ui/select';
import type { SettingGroup, SettingSection } from '../lib/settingsSearch';
import { ClaudeAccountPicker, ClaudeAccountsPicker, GitHubAccountPicker } from './ProjectAccounts';
import { SettingGroups } from './SettingsPage';
import { SettingNote, SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// The settings that belong to one project rather than to the whole
// installation: what its agents run on and with which accounts, what they do
// with their pull requests, and how much its chat does on its own. They're a
// section of Settings, under Projects, and the project's Overview draws the
// same groups, so a setting looks the same and says the same whichever page
// it's on.
//
// Each has a default every project starts on, which is how a row knows it's
// been changed. A setting a project can leave to Settings (the model, the pull
// request watch) says what Settings currently makes of it, so "the default"
// is never a promise you have to go elsewhere to read.
export function projectSettingGroups(project: T.Project): SettingGroup[] {
  return [
    {
      id: 'agents',
      title: 'New agents',
      description: `What ${project.name} gives the agents it creates. Agents it already has keep what they were made with.`,
      entries: [
        {
          id: 'model',
          label: 'Model',
          keywords: 'agents model claude opus sonnet haiku fable auto lead picks per task',
          modified: project.agentModel !== '',
          render: () => <AgentModelPicker project={project} />,
        },
        {
          id: 'size',
          label: 'Size',
          keywords: 'agents size memory reserve light normal heavy auto lead picks queue',
          modified: project.agentSize !== '',
          render: () => <AgentSizePicker project={project} />,
        },
        {
          id: 'branch-prefix',
          label: 'Branch prefix',
          keywords: 'git branch name prefix agentbox/ slug',
          advanced: true,
          modified: project.branchPrefix !== 'agentbox/',
          // Keyed on the saved value, so a save (or another client's) starts the draft over.
          render: () => <BranchPrefixField key={project.branchPrefix} project={project} />,
        },
      ],
    },
    {
      id: 'queue',
      title: 'Agent queue',
      description: `How many of ${project.name}'s agents may run at once, and what a new one does when there's no room.`,
      entries: [
        {
          id: 'slots',
          label: 'Agents at once',
          keywords: 'queue slots concurrency budget memory auto fixed at once running',
          modified: project.slots !== 0,
          render: () => <SlotsSetting project={project} />,
        },
        {
          id: 'always-queue',
          label: 'Always queue new agents',
          keywords: 'queue new agents default start immediately slot free',
          modified: project.alwaysQueue,
          render: () => <AlwaysQueueToggle project={project} />,
        },
      ],
    },
    {
      id: 'accounts',
      title: 'Accounts',
      description: 'The logins its new agents get, from the ones stored under Accounts.',
      entries: [
        {
          id: 'claude-account',
          label: 'Claude Code account',
          keywords: 'claude login token anthropic subscription',
          modified: project.claudeAccount !== '',
          render: () => <ClaudeAccountPicker project={project} />,
        },
        {
          id: 'claude-accounts',
          label: 'Allowed accounts',
          keywords: 'claude code accounts allow list which logins',
          modified: project.claudeAccounts.length > 0,
          render: () => <ClaudeAccountsPicker project={project} />,
        },
        {
          id: 'github-account',
          label: 'GitHub account',
          keywords: 'github login token gh_token push pull requests',
          modified: project.githubAccount !== '',
          render: () => <GitHubAccountPicker project={project} />,
        },
      ],
    },
    {
      id: 'repository',
      title: 'Repository',
      description: "How AgentBox keeps the project's base branch.",
      entries: [
        {
          id: 'sync-base',
          label: 'Keep main up to date with origin',
          keywords: 'git fetch fast-forward main origin base branch sync stale up to date',
          modified: !project.syncBase,
          render: () => <SyncBaseToggle project={project} />,
        },
      ],
    },
    {
      id: 'pulls',
      title: 'Pull requests',
      description: 'What its agents do with their branches once the work is done.',
      entries: [
        {
          id: 'agent-prs',
          label: 'Agents push and open pull requests',
          keywords: 'push pr github publish branch retire',
          modified: project.agentPRs,
          render: () => <AgentPRsToggle project={project} />,
        },
        {
          id: 'pr-watch',
          label: "Watch agents' pull requests",
          keywords: 'pr watch conflict checks ci fail review changes requested github',
          modified: project.prWatch !== '',
          render: () => <PRWatchPicker project={project} />,
        },
      ],
    },
    {
      id: 'chat',
      title: 'Project chat',
      description: `How much the ${project.name} chat does on its own.`,
      entries: [
        {
          id: 'autonomy',
          label: 'Makes product decisions itself',
          keywords: 'autonomy lead chat decides asks product calls',
          modified: project.autonomy === 'on',
          render: () => <AutonomyToggle project={project} />,
        },
        {
          id: 'finish-notices',
          label: 'When an agent finishes',
          keywords: 'finish notices wake chat lead turn tokens record',
          modified: project.finishNotices !== 'lead',
          render: () => <FinishNoticesPicker project={project} />,
        },
      ],
    },
    {
      id: 'testing',
      title: 'Testing AgentBox itself',
      entries: [
        {
          id: 'nesting',
          label: 'Nesting: agents run their own Incus',
          keywords: 'nesting incus containers testing agentbox itself isolation',
          advanced: true,
          modified: project.nesting,
          render: () => <NestingToggle project={project} />,
        },
      ],
    },
  ];
}

// projectSection is a project's settings as a section of the Settings page.
export function projectSection(project: T.Project): SettingSection {
  return {
    id: `project:${project.name}`,
    title: project.name,
    description: 'What this project gives its agents, and how much its chat does on its own. Every other project keeps its own.',
    scope: 'project',
    groups: projectSettingGroups(project),
  };
}

// ProjectSettings is the same groups, on the project's Overview.
export function ProjectSettings({ project }: { project: T.Project }) {
  return (
    <div className="grid gap-8">
      <SettingGroups groups={projectSettingGroups(project)} />
    </div>
  );
}

// The empty stored value: this project follows the model chosen for new agents
// in Settings, which is what every project does until you change it.
const homeDefault = { value: '', name: 'Same as Settings', description: 'The model new agents start on, in every project' };
// Not a model: the project's chat chooses one for each agent it creates.
const auto = { value: AgentModelAuto, name: 'Auto: the lead picks per task', description: 'The lead never picks Fable unless you ask for it.' };

// AgentModelPicker chooses the model this project's new agents are created on.
// Agents that already exist keep the model they have.
//
// The models are the ones a Claude Code adapter really advertised, remembered
// from the last chat that started (rememberChoices in internal/chat), plus
// AgentBox's own small pinned list (Fable, chief among them — D69 in
// decisions.md), so there's something worth offering even before any chat has
// run.
function AgentModelPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const [query, setQuery] = useState('');
  const save = useMutation({
    mutationFn: (agentModel: string) => api.updateProject(project.name, { agentModel }),
    onSuccess: async (updated) => {
      toast(describeAgentModel(updated));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  // The adapter's own "default" entry is left out: it is the menu's word for
  // "no model of my own", which is what the first item here already says, and
  // the daemon refuses it as a project's model rather than store a sentinel
  // where a model is read (SetProjectAgentModel).
  const choices = (settings.data?.claudeModelChoices ?? []).filter((c) => c.value !== 'default');
  const value = project.agentModel;
  const picked = choices.find((c) => c.value === value);
  // "auto" is a mode rather than a model, so it is never the missing entry.
  const missing = value === AgentModelAuto ? undefined : unavailableValue(choices, value);
  const groups = groupChoices(choices.filter((c) => matchesQuery(c, query)));
  const searchable = choices.length >= searchThreshold;
  const label = !settings.data ? 'Loading…' : value === '' ? homeDefault.name : value === AgentModelAuto ? auto.name : (picked && choiceName(picked)) || value;
  // What Settings currently says, so "same as" isn't a promise you have to
  // leave the page to read.
  const home = settings.data?.defaultClaudeModel || 'AgentBox default (opus)';

  return (
    <SettingRow
      label="Model"
      description={value === AgentModelAuto ? auto.description : 'What a new agent of this project starts on.'}
      details="The agents it already has keep the model they were made with. What you pick as you create an agent wins over this."
      control={
        <Menu onOpenChange={(open) => !open && setQuery('')}>
          <MenuTrigger asChild>
            <button
              data-project-model
              disabled={save.isPending || settings.data === undefined}
              aria-label="Agents' model"
              title={label}
              className={cn(selectTrigger, missing && 'text-amber-300')}
            >
              {missing ? <CircleAlert className="size-4 shrink-0 text-amber-400" /> : <Sparkles className="size-4 shrink-0 text-brand-300" />}
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
            </button>
          </MenuTrigger>
          <MenuContent align="start" className="max-h-96 w-80 overflow-y-auto">
            <MenuLabel>Model for this project's agents</MenuLabel>
            {searchable && (
              <div className="mb-1 flex items-center gap-2 rounded-lg bg-surface px-2.5 py-1.5">
                <Search className="size-3.5 shrink-0 text-subtle" />
                <input
                  autoFocus
                  aria-label="Search models"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={(e) => e.stopPropagation()}
                  placeholder="Search"
                  className="w-full bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none"
                />
              </div>
            )}
            {matchesQuery(homeDefault, query) && (
              <MenuItem onSelect={() => save.mutate('')} hint={value === '' ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                <span className="grid">
                  <span>{homeDefault.name}</span>
                  <span className="text-[11px] text-subtle">Currently {home}</span>
                </span>
              </MenuItem>
            )}
            {matchesQuery(auto, query) && (
              <MenuItem onSelect={() => save.mutate(AgentModelAuto)} hint={value === AgentModelAuto ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                <span className="grid">
                  <span>{auto.name}</span>
                  <span className="text-[11px] text-subtle">{auto.description}</span>
                </span>
              </MenuItem>
            )}
            {/* A model this account has stopped offering keeps its place, named
                and marked, instead of the menu quietly showing something else. */}
            {missing && (
              <>
                <MenuSeparator />
                <MenuItem disabled hint={<Check className="size-3.5 text-amber-300" />}>
                  <span className="grid">
                    <span className="flex items-center gap-1.5 text-amber-200">
                      {missing}
                      <span className="shrink-0 rounded-full bg-amber-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-amber-300">Off the menu</span>
                    </span>
                    <span className="text-[11px] text-subtle">Not on the menu Claude Code last advertised — either a model you named, or one this account has stopped offering.</span>
                  </span>
                </MenuItem>
              </>
            )}
            {groups.map((group, gi) => (
              <div key={group.name || gi}>
                {group.name && (
                  <>
                    <MenuSeparator />
                    <MenuLabel>{group.name}</MenuLabel>
                  </>
                )}
                {group.choices.map((choice) => (
                  <MenuItem key={choice.value} onSelect={() => save.mutate(choice.value)} hint={choice.value === value ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                    <span className="grid">
                      <span className="flex items-center gap-1.5">
                        {choiceName(choice)}
                        {isRecommended(choice) && (
                          <span className="shrink-0 rounded-full bg-brand-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-brand-300">Recommended</span>
                        )}
                      </span>
                      {choice.description && <span className="text-[11px] text-subtle">{choice.description}</span>}
                    </span>
                  </MenuItem>
                ))}
              </div>
            ))}
            {choices.length === 0 && (
              <div className="px-2.5 py-3 text-[12px] leading-relaxed text-subtle">
                No model menu yet. Claude Code sends the models your account may use when a chat starts — open one, and they'll be here.
              </div>
            )}
            <ModelByName onPick={(model) => save.mutate(model)} disabled={save.isPending} />
          </MenuContent>
        </Menu>
      }
    />
  );
}

// describeAgentModel says what was just chosen, in the words the setting means.
function describeAgentModel(project: T.Project): string {
  if (project.agentModel === AgentModelAuto) {
    return `The ${project.name} chat picks a model for each agent it creates`;
  }
  if (project.agentModel === '') {
    return `New agents of ${project.name} use the model chosen in Settings`;
  }
  return `New agents of ${project.name} start on ${project.agentModel}`;
}

// queueOffNote is the one-line pointer shown under a queue control once the
// installation has Agent queue turned off (Settings → Agents): the project's
// own slots and always-queue don't do anything until it is.
function QueueOffNote() {
  return <SettingNote tone="warning">Turn on Agent queue in Settings to change how many of this project's agents run at once.</SettingNote>;
}

// SlotsSetting chooses how many of this project's agents may run at once: 0
// (Auto) shares the VM's memory fairly with every other project, by how
// much memory each one's agents actually use; a fixed number pins it,
// whatever else is running. GET /v1/queue's ProjectSlots says what Auto comes
// to right now, whether the per-agent figure is learned from this project's
// own agents or still the installation's memory limit, and — below it — the
// agents that figure is drawn from, refreshed every few seconds. The control
// itself is disabled, with a pointer, while the installation's Agent queue
// setting is off — the slot maths still runs and is worth seeing, but
// changing it here would do nothing until that's on.
function SlotsSetting({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queue = useQuery({ queryKey: ['queue', project.name], queryFn: () => api.queue(project.name), refetchInterval: 5_000 });
  const mine = queue.data?.projects.find((p) => p.project === project.name);
  const [fixed, setFixed] = useState(String(project.slots || mine?.slots || 1));
  const queueOn = settings.data?.agentQueue ?? false;
  const save = useMutation({
    mutationFn: (slots: number) => api.updateProject(project.name, { slots }),
    onSuccess: async (updated) => {
      toast(updated.slots === 0 ? `${updated.name} shares slots automatically again` : `${updated.name} now runs up to ${updated.slots} agent${updated.slots === 1 ? '' : 's'} at once`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['queue', updated.name] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const auto = project.slots === 0;
  const perAgent = mine ? `~${humanBytes(mine.peak)} per agent${mine.peakLearned ? ', learned' : ', from the memory limit'}` : undefined;
  const disabled = save.isPending || !queueOn;

  return (
    <SettingRow
      label="Agents at once"
      description={auto ? (mine ? `Auto: currently ${mine.slots}${perAgent ? `, ${perAgent}` : ''}.` : 'Auto: split fairly with other projects, by memory.') : `Up to ${project.slots} of this project's agents run at once; the rest queue.`}
      details="Whatever doesn't fit queues instead of starting, and starts as soon as a slot frees up. Auto gives every active project at least one slot and splits what's left of the VM's memory by how much memory each project's agents actually use, least-memory projects first."
      control={
        <Select
          aria-label="Agents at once"
          disabled={disabled}
          value={auto ? 'auto' : 'fixed'}
          onChange={(value) => {
            if (value === 'auto') save.mutate(0);
            else save.mutate(Number(fixed) || 1);
          }}
        >
          <SelectOption value="auto">Auto</SelectOption>
          <SelectOption value="fixed">Fixed number</SelectOption>
        </Select>
      }
    >
      {!queueOn && <QueueOffNote />}
      {!auto && (
        <div className="flex items-center gap-2">
          <Input
            type="number"
            min={1}
            max={64}
            aria-label="Fixed number of agents at once"
            className="w-24 font-mono text-[13px]"
            value={fixed}
            disabled={disabled}
            onChange={(e) => setFixed(e.target.value)}
            onBlur={() => {
              const n = Math.min(64, Math.max(1, Number(fixed) || 1));
              setFixed(String(n));
              if (n !== project.slots) save.mutate(n);
            }}
          />
          {mine && <SettingNote>currently {mine.slots}</SettingNote>}
        </div>
      )}
      {mine && (
        <div className="mt-3 grid gap-2">
          <p className="text-[12.5px] text-secondary">
            Slot size: <span className="font-medium text-primary">{humanBytes(mine.peak)} per agent</span>
            {mine.peakLearned ? ', learned from its latest agents' : ', from the memory limit — nothing measured yet'}
          </p>
          <SlotAgentsTable agents={mine.agents} />
        </div>
      )}
    </SettingRow>
  );
}

// SlotAgentsTable is what the slot maths above is drawn from: each of this
// project's running agents, its memory and CPU right now beside the peak
// each has reached (what "learned" learns from). cpu is a percentage of one
// core (100 = one core busy).
function SlotAgentsTable({ agents }: { agents: T.SlotAgent[] }) {
  if (agents.length === 0) return <p className="text-[12px] text-subtle">No agents running.</p>;
  return (
    <div className="overflow-hidden rounded-lg border border-line-faint">
      <table className="w-full text-[12px]">
        <thead>
          <tr className="text-left text-[10.5px] uppercase tracking-wide text-faint">
            <th className="px-2.5 py-1.5 font-medium">Agent</th>
            <th className="px-2.5 py-1.5 font-medium">Memory now / peak</th>
            <th className="px-2.5 py-1.5 font-medium">CPU now / peak</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line-faint">
          {agents.map((a) => (
            <tr key={a.name}>
              <td className="px-2.5 py-1.5">
                <span className="font-medium text-primary">{a.title || a.name}</span>{' '}
                <span className="font-mono text-[10.5px] text-faint">{a.name}</span>
                <div className="text-[10.5px] text-subtle">{a.state}</div>
              </td>
              <td className="whitespace-nowrap px-2.5 py-1.5 font-mono tabular-nums text-secondary">
                {humanBytes(a.memory)} / {humanBytes(a.memoryPeak)}
              </td>
              <td className="whitespace-nowrap px-2.5 py-1.5 font-mono tabular-nums text-secondary">
                {a.cpu.toFixed(0)}% / {a.cpuPeak.toFixed(0)}%
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// AlwaysQueueToggle is what a new agent of this project does by default: skip
// the queue and start right away (off), or queue like the rest until a slot
// is free (on). Either way, New agent's own Queue switch can override it for
// one agent. Disabled, with the same pointer, while the installation's Agent
// queue setting is off.
function AlwaysQueueToggle({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queueOn = settings.data?.agentQueue ?? false;
  const save = useMutation({
    mutationFn: (alwaysQueue: boolean) => api.updateProject(project.name, { alwaysQueue }),
    onSuccess: async (updated) => {
      toast(updated.alwaysQueue ? `New agents of ${updated.name} queue by default` : `New agents of ${updated.name} start right away by default`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Always queue new agents"
      htmlFor="project-always-queue"
      description={project.alwaysQueue ? 'A new agent queues until a slot is free, unless you turn it off in New agent.' : 'A new agent starts right away, unless you queue it in New agent.'}
      details="Either way, New agent's own Queue switch wins for the one agent you're making."
      control={
        <Switch
          id="project-always-queue"
          data-project-always-queue
          checked={project.alwaysQueue}
          disabled={save.isPending || !queueOn}
          onCheckedChange={(on) => save.mutate(on)}
        />
      }
    >
      {!queueOn && <QueueOffNote />}
    </SettingRow>
  );
}

// AgentSizePicker chooses the size of the agents this project's chat creates:
// what each reserves of the VM's memory, which decides whether it starts now
// or waits in the queue. Auto leaves it to the chat, agent by agent; a size
// here wins over the chat's.
function AgentSizePicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (agentSize: string) => api.updateProject(project.name, { agentSize }),
    onSuccess: async (updated) => {
      const size = projectSizes.find((s) => s.value === updated.agentSize);
      toast(updated.agentSize ? `${updated.name}'s chat creates ${size?.label.toLowerCase()} agents` : `${updated.name}'s chat picks each agent's size`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const picked = projectSizes.find((s) => s.value === project.agentSize) ?? projectSizes[0];

  return (
    <SettingRow
      label="Size"
      htmlFor="project-agent-size"
      description={picked.tip}
      details="What an agent reserves of the VM's memory, shared with every project's agents: one that doesn't fit waits in the queue. Not a cap: an agent may use more while there's memory to spare. What you pick in New agent wins over this."
      control={
        <Select id="project-agent-size" data-project-agent-size value={project.agentSize} disabled={save.isPending} onChange={(value) => save.mutate(value)}>
          {projectSizes.map((size) => (
            <SelectOption key={size.value} value={size.value}>
              {size.label} <span className="text-subtle">· {size.tip}</span>
            </SelectOption>
          ))}
        </Select>
      }
    />
  );
}

const projectSizes = agentSizes(true);

// BranchPrefixField sets what this project's new agents' branches start with,
// before the slug named after its work: agentbox/ unless you change it, which in a
// repository shared with others keeps your agents' branches out of theirs.
// Empty is a real choice (the branch is the slug alone), so nothing here
// turns an empty field back into the default. The daemon checks it against
// git's rules and says what's wrong (CheckBranchPrefix in internal/gitrepo).
function BranchPrefixField({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState(project.branchPrefix);
  const save = useMutation({
    mutationFn: (branchPrefix: string) => api.updateProject(project.name, { branchPrefix }),
    onSuccess: async (updated) => {
      toast(`New agents of ${updated.name} branch as ${updated.branchPrefix}<their work>`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });
  const changed = draft !== project.branchPrefix;

  return (
    <SettingRow
      label="Branch prefix"
      htmlFor="project-branch-prefix"
      description={
        <>
          What its agents' branches start with, like <code className="break-all font-mono text-tertiary">{draft}fix-login-redirect</code>.
        </>
      }
      details="A new agent works on a branch named after its work. In a repository shared with others, a prefix keeps your agents' branches out of theirs. Empty means no prefix. Agents that already exist keep their branches."
    >
      <form
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (changed) save.mutate(draft);
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && changed && !save.isPending) {
            setDraft(project.branchPrefix);
            save.reset();
          }
        }}
      >
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-0 flex-1 sm:max-w-sm">
            <GitBranch className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-brand-300" />
            <Input
              id="project-branch-prefix"
              data-project-branch-prefix
              className="pl-9 font-mono text-[12.5px]"
              placeholder="no prefix"
              spellCheck={false}
              value={draft}
              disabled={save.isPending}
              onChange={(e) => {
                setDraft(e.target.value);
                save.reset();
              }}
            />
          </div>
          {changed && (
            <>
              <Button type="submit" variant="primary" size="sm" disabled={save.isPending}>
                {save.isPending && <LoaderCircle className="animate-spin" />}
                Save
              </Button>
              <Button variant="ghost" size="sm" disabled={save.isPending} onClick={() => (setDraft(project.branchPrefix), save.reset())}>
                Cancel
              </Button>
            </>
          )}
        </div>
        {save.error && <SettingNote tone="error">{errorMessage(save.error)}</SettingNote>}
      </form>
    </SettingRow>
  );
}

// AutonomyToggle is how much a project's chat does without being asked. It was
// reachable only from the command line (agentbox autonomy) until this card,
// and it belongs beside the model: both say what the chat may do for you.
function AutonomyToggle({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const acts = project.autonomy === 'on';
  const save = useMutation({
    mutationFn: (autonomy: string) => api.updateProject(project.name, { autonomy }),
    onSuccess: async (updated) => {
      toast(
        updated.autonomy === 'on'
          ? `The ${updated.name} chat acts on what it decides, and tells you`
          : `The ${updated.name} chat proposes product decisions, and does the routine itself`,
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Makes product decisions itself"
      htmlFor="project-autonomy"
      description={
        acts
          ? 'It makes product calls itself — designs, the next piece of work — and tells you what it did.'
          : 'It asks you about product decisions and anything costly.'
      }
      details="Either way it does the routine itself: it retires merged agents, answers agents and starts the obvious next one."
      control={
        <Switch
          id="project-autonomy"
          data-project-autonomy
          checked={acts}
          disabled={save.isPending}
          onCheckedChange={(on) => save.mutate(on ? 'on' : 'ask')}
        />
      }
    />
  );
}

// AgentPRsToggle lets this project's agents push their own branch and open
// their own pull request when they finish, and tells the lead to stop doing it
// for them. Off by default: a push publishes, with the user's GitHub token.
function AgentPRsToggle({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (agentPRs: boolean) => api.updateProject(project.name, { agentPRs }),
    onSuccess: async (updated) => {
      toast(updated.agentPRs ? `${updated.name}'s agents now open their own pull requests` : `${updated.name}'s agents no longer push`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Agents push and open pull requests"
      htmlFor="project-agent-prs"
      description={
        project.agentPRs
          ? 'An agent pushes its branch and opens a pull request when it finishes.'
          : "Agents commit on their branch and don't push: you or the project's chat push it and open the pull request."
      }
      details={
        project.agentPRs
          ? 'The chat retires it once the pull request is open. An agent never pushes to the base branch, merges or closes anything. A push publishes, with the project\'s GitHub account.'
          : 'On, an agent pushes its branch and opens its own pull request when it finishes, with the project\'s GitHub account, and the chat stops doing it for it. Off by default, since a push publishes.'
      }
      control={
        <Switch
          id="project-agent-prs"
          data-project-agent-prs
          checked={project.agentPRs}
          disabled={save.isPending}
          onCheckedChange={(on) => save.mutate(on)}
        />
      }
    />
  );
}

// SyncBaseToggle keeps the project's base branch (main) up to date with its
// remote: the daemon fetches every few minutes and before it creates an
// agent, and fast-forwards the local branch when it is strictly behind. On by
// default, since an agent made from a stale main starts without what was
// merged since.
function SyncBaseToggle({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (syncBase: boolean) => api.updateProject(project.name, { syncBase }),
    onSuccess: async (updated) => {
      toast(updated.syncBase ? `${updated.name}'s main now follows its remote` : `AgentBox no longer moves ${updated.name}'s main`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Keep main up to date with origin"
      htmlFor="project-sync-base"
      description={
        project.syncBase
          ? "AgentBox fetches every few minutes and fast-forwards main when it's behind origin."
          : "AgentBox doesn't fetch or move main."
      }
      details={
        project.syncBase
          ? 'It also fetches before it creates an agent. It never touches a main with commits of its own, and moves a checked-out main only when nothing in it is changed.'
          : "A new agent still starts from origin's main, as last fetched, when yours is behind it. On by default, since an agent made from a stale main starts without what was merged since."
      }
      control={
        <Switch
          id="project-sync-base"
          data-project-sync-base
          checked={project.syncBase}
          disabled={save.isPending}
          onCheckedChange={(on) => save.mutate(on)}
        />
      }
    />
  );
}

// NestingToggle turns nesting on for this project's agents: a real Incus
// daemon of their own, inside their own container, for testing AgentBox
// features that touch agent machines (image builds, devices, networking) for real.
// Off by default, and only offered once the base image is built with Incus,
// since that's what it needs to nest.
function NestingToggle({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup });
  const hasIncus = setup.data?.image.components.incus === true;
  const save = useMutation({
    mutationFn: (nesting: boolean) => api.updateProject(project.name, { nesting }),
    onSuccess: async (updated) => {
      toast(updated.nesting ? `${updated.name}'s agents now run their own Incus` : `${updated.name}'s agents no longer run their own Incus`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Nesting: agents run their own Incus"
      htmlFor="project-nesting"
      description={
        hasIncus || project.nesting
          ? 'A new agent gets a real Incus daemon of its own, to test AgentBox itself.'
          : 'Build the base image with Incus first: agentbox image build --incus.'
      }
      details="For a project whose agents work on AgentBox: they can test the features that touch agent machines — image builds, devices, networking — for real. It costs isolation: the agent can make and run containers of its own."
      control={
        <Switch
          id="project-nesting"
          data-project-nesting
          checked={project.nesting}
          disabled={save.isPending || (!hasIncus && !project.nesting)}
          onCheckedChange={(on) => save.mutate(on)}
        />
      }
    />
  );
}

// PRWatchPicker overrides Settings' "Watch agents' pull requests" for this
// project, or follows it (the empty value). The daemon says what that comes
// to, so following it can say whether it's on.
function PRWatchPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const pick = useMutation({
    mutationFn: (prWatch: string) => api.updateProject(project.name, { prWatch }),
    onSuccess: async (updated) => {
      toast(updated.prWatching ? `AgentBox watches ${updated.name}'s agents' pull requests` : `AgentBox no longer watches ${updated.name}'s agents' pull requests`);
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Watch agents' pull requests"
      description="Tells an agent when its pull request conflicts, fails its checks or gets changes requested."
      details="Until each is merged or closed. AgentBox tells the agent to fix it, starting it if it was stopped, and tells the chat. One request to GitHub per look, however many pull requests. Left as in Settings, it follows the setting there."
    >
      <Select
        data-project-pr-watch
        aria-label="Watch agents' pull requests"
        disabled={pick.isPending}
        className="sm:max-w-sm"
        value={project.prWatch}
        onChange={(value) => pick.mutate(value)}
      >
        <SelectOption value="">{settings.data ? `As in Settings (${settings.data.prWatch ? 'on' : 'off'})` : 'As in Settings'}</SelectOption>
        <SelectOption value="on">On for this project</SelectOption>
        <SelectOption value="off">Off for this project</SelectOption>
      </Select>
    </SettingRow>
  );
}

// FinishNoticesPicker chooses what a finishing agent does to this project's
// chat. Telling the chat costs it a full turn every time, even when the agent
// needed nothing decided, so a project can ask for the finish to be recorded
// and nothing more — the chat still reads it the next time you write. "lead"
// leaves the choice to the agent that finished, made when it was created (New
// agent's "When it finishes"); one that chose nothing still wakes the chat.
//
// Questions are deliberately not part of this: an agent that asks is blocked
// until somebody answers, so its question always wakes the chat.
function FinishNoticesPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const pick = useMutation({
    mutationFn: (finishNotices: string) => api.updateProject(project.name, { finishNotices }),
    onSuccess: async (updated) => {
      toast(
        {
          off: `An agent of ${updated.name} that finishes is only recorded in its chat`,
          lead: `An agent of ${updated.name} that finishes wakes its chat unless it was told not to when it was created`,
        }[updated.finishNotices] ?? `An agent of ${updated.name} that finishes wakes its chat, which decides what happens next`,
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  return (
    <SettingRow
      label="When an agent finishes"
      description="Whether a finished agent wakes the chat, which costs it a turn."
      details="Telling the chat costs it a turn, even when the agent left nothing to decide. Recorded finishes are still in its history, and it reads them the next time you write. A question from an agent wakes it either way — the agent is blocked on the answer."
    >
      <Select
        data-finish-notices
        aria-label="When an agent finishes"
        disabled={pick.isPending}
        className="sm:max-w-sm"
        value={project.finishNotices}
        onChange={(value) => pick.mutate(value)}
      >
        <SelectOption value="lead">Let the agent that finished decide (the default)</SelectOption>
        <SelectOption value="chat">Tell the project chat, and let it decide</SelectOption>
        <SelectOption value="off">Only record it (no chat turn, no tokens)</SelectOption>
      </Select>
      {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
    </SettingRow>
  );
}
