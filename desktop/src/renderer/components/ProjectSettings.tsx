import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, CircleAlert, GitBranch, LoaderCircle, Search, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { AgentModelAuto } from '../../shared/api';
import { api } from '../lib/api';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../lib/modelChoices';
import { cn, errorMessage } from '../lib/utils';
import { ModelByName } from './ModelByName';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';
import { Select, SelectOption, selectTrigger } from './ui/select';
import { SettingNote, SettingRow, SettingsGroup } from './ui/settings';
import { Switch } from './ui/switch';

// The settings that belong to one project rather than to the whole
// installation: what its agents run on and are branched as, and how much its
// chat does on its own.
// They live on the project's Overview, beside the accounts its new agents get,
// because that is where every other "this project's new agents…" choice is.
// Laid out like Settings' own groups (ui/settings), so a setting looks the same
// whichever page it is on.
export function ProjectSettings({ project }: { project: T.Project }) {
  return (
    <div className="grid gap-8">
      <SettingsGroup title="New agents" description={`What ${project.name} gives the agents it creates. Agents it already has keep what they were made with.`}>
        <AgentModelPicker project={project} />
        {/* Keyed on the saved value, so a save (or another client's) starts the draft over. */}
        <BranchPrefixField key={project.branchPrefix} project={project} />
      </SettingsGroup>
      <SettingsGroup title="Project chat" description={`How much the ${project.name} chat does on its own.`}>
        <AutonomyToggle project={project} />
        <FinishNoticesPicker project={project} />
      </SettingsGroup>
      <SettingsGroup title="Testing AgentBox itself" description="For a project whose agents work on AgentBox.">
        <NestingToggle project={project} />
      </SettingsGroup>
    </div>
  );
}

// The empty stored value: this project follows the model chosen for new agents
// on the Home page, which is what every project does until you change it.
const homeDefault = { value: '', name: 'Same as Home page default', description: 'The model new agents start on, in every project' };
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
  // What the Home page currently says, so "same as" isn't a promise you have
  // to leave the page to read.
  const home = settings.data?.defaultClaudeModel || 'AgentBox default (opus)';

  return (
    <SettingRow
      label="Model"
      description={value === AgentModelAuto ? auto.description : 'What a new agent starts on. The ones this project already has keep the model they were made with.'}
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
    return `New agents of ${project.name} use the model chosen on the Home page`;
  }
  return `New agents of ${project.name} start on ${project.agentModel}`;
}

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
          A new agent works on a branch named after its work, like{' '}
          <code className="break-all font-mono text-tertiary">{draft}fix-login-redirect</code>. Empty means no prefix. Agents that already exist keep
          their branches.
        </>
      }
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
          ? 'It also makes the product calls itself — designs, the next piece of work — and tells you what it did.'
          : 'It retires merged agents, answers agents and starts the obvious next one itself, and asks you about product decisions and anything costly.'
      }
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

// NestingToggle turns nesting on for this project's agents: a real Incus
// daemon of their own, inside their own container, for testing AgentBox
// features that touch agent machines (limits, GPU, image builds) for real.
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
          ? 'A new agent gets a real Incus daemon of its own, so it can test AgentBox features that touch agent machines. It costs isolation: the agent can make and run containers of its own.'
          : 'Build the base image with Incus first: agentbox image build --incus.'
      }
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
      description="Telling the chat costs it a turn, even when the agent left nothing to decide. Recorded finishes are still in its history, and it reads them the next time you write. A question from an agent wakes it either way — the agent is blocked on the answer."
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
