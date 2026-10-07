import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, CircleAlert, GitBranch, LoaderCircle, Search, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { AgentModelAuto } from '../../shared/api';
import { agentSizes } from '../lib/agentSize';
import { api } from '../lib/api';
import { t, useT, type MessageKey } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../lib/modelChoices';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { ModelByName } from './ModelByName';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';
import { Select, SelectOption, selectTrigger } from './ui/select';
import type { SettingGroup, SettingSection } from '../lib/settingsSearch';
import { ClaudeAccountPicker, ClaudeAccountsPicker, GitHubAccountPicker } from './ProjectAccounts';
import { openSettingsSection, SettingGroups } from './SettingsPage';
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
  const name = projectLabel(project);
  return [
    {
      id: 'name',
      title: t('defaults.project.groupProject'),
      entries: [
        {
          id: 'name',
          label: t('defaults.project.nameLabel'),
          keywords: t('defaults.project.nameKeywords'),
          // Keyed on the saved name, so a save (or another client's) starts the draft over.
          render: () => <ProjectNameField key={name} project={project} />,
        },
      ],
    },
    {
      id: 'agents',
      title: t('defaults.project.groupAgents'),
      description: t('defaults.project.groupAgentsDescription', { name }),
      entries: [
        {
          id: 'model',
          label: t('defaults.project.modelLabel'),
          keywords: t('defaults.project.modelKeywords'),
          modified: project.agentModel !== '',
          render: () => <AgentModelPicker project={project} />,
        },
        {
          id: 'size',
          label: t('defaults.project.sizeLabel'),
          keywords: t('defaults.project.sizeKeywords'),
          modified: project.agentSize !== '',
          render: () => <AgentSizePicker project={project} />,
        },
        {
          id: 'branch-prefix',
          label: t('defaults.project.prefixLabel'),
          keywords: t('defaults.project.prefixKeywords'),
          advanced: true,
          modified: project.branchPrefix !== 'agentbox/',
          // Keyed on the saved value, so a save (or another client's) starts the draft over.
          render: () => <BranchPrefixField key={project.branchPrefix} project={project} />,
        },
      ],
    },
    {
      id: 'queue',
      title: t('defaults.project.groupQueue'),
      description: t('defaults.project.groupQueueDescription', { name }),
      entries: [
        {
          id: 'slots',
          label: t('defaults.project.slotsLabel'),
          keywords: t('defaults.project.slotsKeywords'),
          modified: project.slots !== 0,
          render: () => <SlotsSetting project={project} />,
        },
        {
          id: 'always-queue',
          label: t('defaults.project.alwaysQueueLabel'),
          keywords: t('defaults.project.alwaysQueueKeywords'),
          modified: project.alwaysQueue,
          render: () => <AlwaysQueueToggle project={project} />,
        },
      ],
    },
    {
      id: 'accounts',
      title: t('defaults.project.groupAccounts'),
      description: t('defaults.project.groupAccountsDescription'),
      entries: [
        {
          id: 'claude-account',
          label: t('defaults.project.claudeAccountLabel'),
          keywords: t('defaults.project.claudeAccountKeywords'),
          modified: project.claudeAccount !== '',
          render: () => <ClaudeAccountPicker project={project} />,
        },
        {
          id: 'claude-accounts',
          label: t('defaults.project.allowedLabel'),
          keywords: t('defaults.project.allowedKeywords'),
          modified: project.claudeAccounts.length > 0,
          render: () => <ClaudeAccountsPicker project={project} />,
        },
        {
          id: 'github-account',
          label: t('defaults.project.githubAccountLabel'),
          keywords: t('defaults.project.githubAccountKeywords'),
          modified: project.githubAccount !== '',
          render: () => <GitHubAccountPicker project={project} />,
        },
      ],
    },
    {
      id: 'repository',
      title: t('defaults.project.groupRepo'),
      description: t('defaults.project.groupRepoDescription'),
      entries: [
        {
          id: 'sync-base',
          label: t('defaults.project.syncLabel'),
          keywords: t('defaults.project.syncKeywords'),
          modified: !project.syncBase,
          render: () => <SyncBaseToggle project={project} />,
        },
      ],
    },
    {
      id: 'pulls',
      title: t('defaults.project.groupPulls'),
      description: t('defaults.project.groupPullsDescription'),
      entries: [
        {
          id: 'agent-prs',
          label: t('defaults.project.agentPRsLabel'),
          keywords: t('defaults.project.agentPRsKeywords'),
          modified: project.agentPRs,
          render: () => <AgentPRsToggle project={project} />,
        },
        {
          id: 'pr-watch',
          label: t('defaults.project.prWatchLabel'),
          keywords: t('defaults.project.prWatchKeywords'),
          modified: project.prWatch !== '',
          render: () => <PRWatchPicker project={project} />,
        },
      ],
    },
    {
      id: 'chat',
      title: t('defaults.project.groupChat'),
      description: t('defaults.project.groupChatDescription', { name }),
      entries: [
        {
          id: 'autonomy',
          label: t('defaults.project.autonomyLabel'),
          keywords: t('defaults.project.autonomyKeywords'),
          modified: project.autonomy === 'on',
          render: () => <AutonomyToggle project={project} />,
        },
        {
          id: 'finish-notices',
          label: t('defaults.project.finishLabel'),
          keywords: t('defaults.project.finishKeywords'),
          modified: project.finishNotices !== 'lead',
          render: () => <FinishNoticesPicker project={project} />,
        },
      ],
    },
    {
      id: 'testing',
      title: t('defaults.project.groupTesting'),
      entries: [
        {
          id: 'nesting',
          label: t('defaults.project.nestingLabel'),
          keywords: t('defaults.project.nestingKeywords'),
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
    title: projectLabel(project),
    description: t('defaults.project.sectionDescription'),
    scope: 'project',
    groups: projectSettingGroups(project),
  };
}

// ProjectSettings is the same groups, on the project's Overview.
export function ProjectSettings({ project }: { project: T.Project }) {
  useT();
  return (
    <div className="grid gap-8">
      <SettingGroups groups={projectSettingGroups(project)} />
    </div>
  );
}

// The empty stored value: this project follows the model chosen for new agents
// in Settings, which is what every project does until you change it.
const homeDefaultOf = () => ({ value: '', name: t('defaults.project.sameAsSettings'), description: t('defaults.project.sameAsSettingsDescription') });
// Not a model: the project's chat chooses one for each agent it creates.
const autoModelOf = () => ({ value: AgentModelAuto, name: t('defaults.project.autoName'), description: t('defaults.project.autoDescription') });

// AgentModelPicker chooses the model this project's new agents are created on.
// Agents that already exist keep the model they have.
//
// The models are the ones a Claude Code adapter really advertised, remembered
// from the last chat that started (rememberChoices in internal/chat), plus
// AgentBox's own small pinned list (Fable, chief among them — D69 in
// decisions.md), so there's something worth offering even before any chat has
// run.
function AgentModelPicker({ project }: { project: T.Project }) {
  const t = useT();
  const homeDefault = homeDefaultOf();
  const auto = autoModelOf();
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
  const label = !settings.data ? t('common.loading') : value === '' ? homeDefault.name : value === AgentModelAuto ? auto.name : (picked && choiceName(picked)) || value;
  // What Settings currently says, so "same as" isn't a promise you have to
  // leave the page to read.
  const home = settings.data?.defaultClaudeModel || t('defaults.project.homeFallback');
  // With enforcement on, the lead creates every agent on Settings' model, so
  // this project's own choice has no effect.
  const enforced = settings.data?.enforceAgentDefaults === true;
  const enforcedModel = home;

  return (
    <SettingRow
      label={t('defaults.project.modelLabel')}
      description={value === AgentModelAuto ? auto.description : t('defaults.project.modelDescription')}
      details={t('defaults.project.modelDetails')}
      control={
        <Menu onOpenChange={(open) => !open && setQuery('')}>
          <MenuTrigger asChild>
            <button
              data-project-model
              disabled={save.isPending || settings.data === undefined}
              aria-label={t('defaults.project.modelAria')}
              title={label}
              className={cn(selectTrigger, missing && 'text-amber-300')}
            >
              {missing ? <CircleAlert className="size-4 shrink-0 text-amber-400" /> : <Sparkles className="size-4 shrink-0 text-brand-300" />}
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
            </button>
          </MenuTrigger>
          <MenuContent align="start" className="max-h-96 w-80 overflow-y-auto">
            <MenuLabel>{t('defaults.project.modelMenu')}</MenuLabel>
            {searchable && (
              <div className="mb-1 flex items-center gap-2 rounded-lg bg-surface px-2.5 py-1.5">
                <Search className="size-3.5 shrink-0 text-subtle" />
                <input
                  autoFocus
                  aria-label={t('defaults.newAgent.searchModels')}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={(e) => e.stopPropagation()}
                  placeholder={t('common.search')}
                  className="w-full bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none"
                />
              </div>
            )}
            {matchesQuery(homeDefault, query) && (
              <MenuItem onSelect={() => save.mutate('')} hint={value === '' ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                <span className="grid">
                  <span>{homeDefault.name}</span>
                  <span className="text-[11px] text-subtle">{t('defaults.project.currently', { home })}</span>
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
                      <span className="shrink-0 rounded-full bg-amber-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-amber-300">{t('defaults.newAgent.offMenu')}</span>
                    </span>
                    <span className="text-[11px] text-subtle">{t('defaults.newAgent.offMenuNote')}</span>
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
                          <span className="shrink-0 rounded-full bg-brand-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-brand-300">{t('defaults.newAgent.recommended')}</span>
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
                {t('defaults.project.noMenu')}
              </div>
            )}
            <ModelByName onPick={(model) => save.mutate(model)} disabled={save.isPending} />
          </MenuContent>
        </Menu>
      }
    >
      {enforced && (
        <SettingNote tone="warning">
          {t('defaults.project.modelEnforced', { model: enforcedModel })}{' '}
          <button type="button" data-open-models className="underline underline-offset-2 hover:text-amber-200" onClick={() => openSettingsSection('models')}>
            {t('defaults.project.modelEnforcedLink')}
          </button>
        </SettingNote>
      )}
    </SettingRow>
  );
}

// describeAgentModel says what was just chosen, in the words the setting means.
function describeAgentModel(project: T.Project): string {
  if (project.agentModel === AgentModelAuto) {
    return t('defaults.project.describeAuto', { name: projectLabel(project) });
  }
  if (project.agentModel === '') {
    return t('defaults.project.describeHome', { name: projectLabel(project) });
  }
  return t('defaults.project.describeModel', { name: projectLabel(project), model: project.agentModel });
}

// queueOffNote is the one-line pointer shown under a queue control once the
// installation has Agent queue turned off (Settings → Agents): the project's
// own slots and always-queue don't do anything until it is.
function QueueOffNote() {
  const t = useT();
  return <SettingNote tone="warning">{t('defaults.project.queueOff')}</SettingNote>;
}

// SlotsSetting chooses how many of this project's agents may run at once: 0
// (Auto) shares the VM's memory fairly with every other project, by how
// much memory each one's agents actually use, and its number is only an
// estimate; a fixed number pins it, whatever else is running, as a hard cap
// on new agents however they're made. GET /v1/queue's ProjectSlots says what Auto comes
// to right now, whether the per-agent figure is learned from this project's
// own agents or still the installation's memory limit, and — below it — the
// agents that figure is drawn from, refreshed every few seconds. The control
// itself is disabled, with a pointer, while the installation's Agent queue
// setting is off — the slot maths still runs and is worth seeing, but
// changing it here would do nothing until that's on.
function SlotsSetting({ project }: { project: T.Project }) {
  const t = useT();
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queue = useQuery({ queryKey: ['queue', project.name], queryFn: () => api.queue(project.name), refetchInterval: 5_000 });
  const mine = queue.data?.projects.find((p) => p.project === project.name);
  const [fixed, setFixed] = useState(String(project.slots || mine?.slots || 1));
  const queueOn = settings.data?.agentQueue ?? false;
  const save = useMutation({
    mutationFn: (slots: number) => api.updateProject(project.name, { slots }),
    onSuccess: async (updated) => {
      toast(updated.slots === 0 ? t('defaults.project.slotsAutoToast', { name: updated.name }) : t('defaults.project.slotsFixedToast', { name: updated.name, count: updated.slots }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['queue', updated.name] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const auto = project.slots === 0;
  const disabled = save.isPending || !queueOn;

  return (
    <SettingRow
      label={t('defaults.project.slotsLabel')}
      description={
        auto ? (
          <>
            <span className="block">{t('defaults.project.slotsAutoStart')}</span>
            {mine
              ? t('defaults.project.slotsAutoNow', { slots: mine.slots, size: humanBytes(mine.peak), learned: mine.peakLearned ? 'yes' : 'no' })
              : t('defaults.project.slotsAuto')}
          </>
        ) : (
          t('defaults.project.slotsUpTo', { count: project.slots })
        )
      }
      details={t('defaults.project.slotsDetails')}
      control={
        <Select
          aria-label={t('defaults.project.slotsLabel')}
          disabled={disabled}
          value={auto ? 'auto' : 'fixed'}
          onChange={(value) => {
            if (value === 'auto') save.mutate(0);
            else save.mutate(Number(fixed) || 1);
          }}
        >
          <SelectOption value="auto">{t('defaults.project.auto')}</SelectOption>
          <SelectOption value="fixed">{t('defaults.project.fixed')}</SelectOption>
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
            aria-label={t('defaults.project.fixedAria')}
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
          {mine && <SettingNote>{t('defaults.project.currentlySlots', { slots: mine.slots })}</SettingNote>}
        </div>
      )}
      {mine && (
        <div className="mt-3 grid gap-2">
          <p className="text-[12.5px] text-secondary">
            {t.rich(mine.peakLearned ? 'defaults.project.slotSizeLearned' : 'defaults.project.slotSizeLimit', {
              strong: (c) => <span className="font-medium text-primary">{c}</span>,
              size: humanBytes(mine.peak),
            })}
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
  const t = useT();
  if (agents.length === 0) return <p className="text-[12px] text-subtle">{t('defaults.project.noAgentsRunning')}</p>;
  return (
    <div className="overflow-hidden rounded-lg border border-line-faint">
      <table className="w-full text-[12px]">
        <thead>
          <tr className="text-left text-[10.5px] uppercase tracking-wide text-faint">
            <th className="px-2.5 py-1.5 font-medium">{t('defaults.project.colAgent')}</th>
            <th className="px-2.5 py-1.5 font-medium">{t('defaults.project.colMemory')}</th>
            <th className="px-2.5 py-1.5 font-medium">{t('defaults.project.colCpu')}</th>
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
  const t = useT();
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queueOn = settings.data?.agentQueue ?? false;
  const save = useMutation({
    mutationFn: (alwaysQueue: boolean) => api.updateProject(project.name, { alwaysQueue }),
    onSuccess: async (updated) => {
      toast(updated.alwaysQueue ? t('defaults.project.alwaysQueueOnToast', { name: updated.name }) : t('defaults.project.alwaysQueueOffToast', { name: updated.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.project.alwaysQueueLabel')}
      htmlFor="project-always-queue"
      description={project.alwaysQueue ? t('defaults.project.alwaysQueueOnDescription') : t('defaults.project.alwaysQueueOffDescription')}
      details={t('defaults.project.alwaysQueueDetails')}
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
  const t = useT();
  const queryClient = useQueryClient();
  const projectSizes = agentSizes(true);
  const save = useMutation({
    mutationFn: (agentSize: string) => api.updateProject(project.name, { agentSize }),
    onSuccess: async (updated) => {
      const size = agentSizes(true).find((s) => s.value === updated.agentSize);
      const name = projectLabel(updated);
      toast(updated.agentSize ? t('defaults.project.sizeToast', { name, size: (size?.label ?? updated.agentSize).toLowerCase() }) : t('defaults.project.sizeAutoToast', { name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const picked = projectSizes.find((s) => s.value === project.agentSize) ?? projectSizes[0];

  return (
    <SettingRow
      label={t('defaults.project.sizeLabel')}
      htmlFor="project-agent-size"
      description={picked.tip}
      details={t('defaults.project.sizeDetails')}
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

// BranchPrefixField sets what this project's new agents' branches start with,
// before the slug named after its work: agentbox/ unless you change it, which in a
// repository shared with others keeps your agents' branches out of theirs.
// Empty is a real choice (the branch is the slug alone), so nothing here
// turns an empty field back into the default. The daemon checks it against
// git's rules and says what's wrong (CheckBranchPrefix in internal/gitrepo).
function BranchPrefixField({ project }: { project: T.Project }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState(project.branchPrefix);
  const save = useMutation({
    mutationFn: (branchPrefix: string) => api.updateProject(project.name, { branchPrefix }),
    onSuccess: async (updated) => {
      toast(t('defaults.project.prefixToast', { name: projectLabel(updated), branch: `${updated.branchPrefix}<${t('defaults.project.theirWork')}>` }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });
  const changed = draft !== project.branchPrefix;

  return (
    <SettingRow
      label={t('defaults.project.prefixLabel')}
      htmlFor="project-branch-prefix"
      description={t.rich('defaults.project.prefixDescription', {
        code: (c) => <code className="break-all font-mono text-tertiary">{c}</code>,
        example: `${draft}fix-login-redirect`,
      })}
      details={t('defaults.project.prefixDetails')}
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
              placeholder={t('defaults.project.noPrefix')}
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
                {t('common.save')}
              </Button>
              <Button variant="ghost" size="sm" disabled={save.isPending} onClick={() => (setDraft(project.branchPrefix), save.reset())}>
                {t('common.cancel')}
              </Button>
            </>
          )}
        </div>
        {save.error && <SettingNote tone="error">{errorMessage(save.error)}</SettingNote>}
      </form>
    </SettingRow>
  );
}

// ProjectNameField renames the project: only what it's called changes. Its id
// (the slug in its agents' branches, machines and URLs) stays what it was made
// with, so nothing on disk moves.
function ProjectNameField({ project }: { project: T.Project }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState(projectLabel(project));
  const save = useMutation({
    mutationFn: (displayName: string) => api.updateProject(project.name, { displayName }),
    onSuccess: async (updated) => {
      toast(t('defaults.project.renamedToast', { name: projectLabel(updated) }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });
  const changed = draft.trim() !== projectLabel(project);

  return (
    <SettingRow
      label={t('defaults.project.nameLabel')}
      htmlFor="project-name"
      description={t.rich('defaults.project.nameDescription', { code: (c) => <code className="break-all font-mono text-tertiary">{c}</code>, id: project.name })}
    >
      <form
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (changed && draft.trim() !== '') save.mutate(draft.trim());
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && changed && !save.isPending) {
            setDraft(projectLabel(project));
            save.reset();
          }
        }}
      >
        <div className="flex flex-wrap items-center gap-2">
          <Input
            id="project-name"
            data-project-name
            className="min-w-0 flex-1 sm:max-w-sm"
            value={draft}
            disabled={save.isPending}
            onChange={(e) => {
              setDraft(e.target.value);
              save.reset();
            }}
          />
          {changed && (
            <>
              <Button type="submit" variant="primary" size="sm" disabled={save.isPending || draft.trim() === ''}>
                {save.isPending && <LoaderCircle className="animate-spin" />}
                {t('common.save')}
              </Button>
              <Button variant="ghost" size="sm" disabled={save.isPending} onClick={() => (setDraft(projectLabel(project)), save.reset())}>
                {t('common.cancel')}
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
  const t = useT();
  const queryClient = useQueryClient();
  const acts = project.autonomy === 'on';
  const save = useMutation({
    mutationFn: (autonomy: string) => api.updateProject(project.name, { autonomy }),
    onSuccess: async (updated) => {
      toast(
        updated.autonomy === 'on'
          ? t('defaults.project.autonomyOnToast', { name: updated.name })
          : t('defaults.project.autonomyOffToast', { name: updated.name }),
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.project.autonomyLabel')}
      htmlFor="project-autonomy"
      description={acts ? t('defaults.project.autonomyOnDescription') : t('defaults.project.autonomyOffDescription')}
      details={t('defaults.project.autonomyDetails')}
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
  const t = useT();
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (agentPRs: boolean) => api.updateProject(project.name, { agentPRs }),
    onSuccess: async (updated) => {
      toast(updated.agentPRs ? t('defaults.project.agentPRsOnToast', { name: updated.name }) : t('defaults.project.agentPRsOffToast', { name: updated.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.project.agentPRsLabel')}
      htmlFor="project-agent-prs"
      description={project.agentPRs ? t('defaults.project.agentPRsOnDescription') : t('defaults.project.agentPRsOffDescription')}
      details={project.agentPRs ? t('defaults.project.agentPRsOnDetails') : t('defaults.project.agentPRsOffDetails')}
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
  const t = useT();
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (syncBase: boolean) => api.updateProject(project.name, { syncBase }),
    onSuccess: async (updated) => {
      toast(updated.syncBase ? t('defaults.project.syncOnToast', { name: updated.name }) : t('defaults.project.syncOffToast', { name: updated.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.project.syncLabel')}
      htmlFor="project-sync-base"
      description={project.syncBase ? t('defaults.project.syncOnDescription') : t('defaults.project.syncOffDescription')}
      details={project.syncBase ? t('defaults.project.syncOnDetails') : t('defaults.project.syncOffDetails')}
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
  const t = useT();
  const queryClient = useQueryClient();
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup });
  const hasIncus = setup.data?.image.components.incus === true;
  const save = useMutation({
    mutationFn: (nesting: boolean) => api.updateProject(project.name, { nesting }),
    onSuccess: async (updated) => {
      toast(updated.nesting ? t('defaults.project.nestingOnToast', { name: updated.name }) : t('defaults.project.nestingOffToast', { name: updated.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.project.nestingLabel')}
      htmlFor="project-nesting"
      description={hasIncus || project.nesting ? t('defaults.project.nestingDescription') : t('defaults.project.nestingNeedsIncus')}
      details={t('defaults.project.nestingDetails')}
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
  const t = useT();
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const pick = useMutation({
    mutationFn: (prWatch: string) => api.updateProject(project.name, { prWatch }),
    onSuccess: async (updated) => {
      toast(updated.prWatching ? t('defaults.project.prWatchOnToast', { name: updated.name }) : t('defaults.project.prWatchOffToast', { name: updated.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.project.prWatchLabel')}
      description={t('defaults.project.prWatchDescription')}
      details={t('defaults.project.prWatchDetails')}
    >
      <Select
        data-project-pr-watch
        aria-label={t('defaults.project.prWatchLabel')}
        disabled={pick.isPending}
        className="sm:max-w-sm"
        value={project.prWatch}
        onChange={(value) => pick.mutate(value)}
      >
        <SelectOption value="">{settings.data ? t('defaults.project.asInSettingsState', { on: settings.data.prWatch ? 'yes' : 'no' }) : t('defaults.project.asInSettings')}</SelectOption>
        <SelectOption value="on">{t('defaults.project.onForProject')}</SelectOption>
        <SelectOption value="off">{t('defaults.project.offForProject')}</SelectOption>
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
  const t = useT();
  const queryClient = useQueryClient();
  const pick = useMutation({
    mutationFn: (finishNotices: string) => api.updateProject(project.name, { finishNotices }),
    onSuccess: async (updated) => {
      const toastKeys: Record<string, MessageKey> = {
        off: 'defaults.project.finishOffToast',
        lead: 'defaults.project.finishLeadToast',
      };
      toast(t(toastKeys[updated.finishNotices] ?? 'defaults.project.finishChatToast', { name: updated.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  return (
    <SettingRow
      label={t('defaults.project.finishLabel')}
      description={t('defaults.project.finishDescription')}
      details={t('defaults.project.finishDetails')}
    >
      <Select
        data-finish-notices
        aria-label={t('defaults.project.finishLabel')}
        disabled={pick.isPending}
        className="sm:max-w-sm"
        value={project.finishNotices}
        onChange={(value) => pick.mutate(value)}
      >
        <SelectOption value="lead">{t('defaults.project.finishLead')}</SelectOption>
        <SelectOption value="chat">{t('defaults.project.finishChat')}</SelectOption>
        <SelectOption value="off">{t('defaults.project.finishOff')}</SelectOption>
      </Select>
      {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
    </SettingRow>
  );
}
