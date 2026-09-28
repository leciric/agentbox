import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, CircleAlert, Search, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatTokens } from '../lib/chat';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../lib/modelChoices';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { JobProgress } from './JobProgress';
import { Button } from './ui/button';
import { ModelByName } from './ModelByName';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';
import { Field, Input } from './ui/input';
import { Select, SelectOption, selectTrigger } from './ui/select';
import { SettingNote, SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// Role is a section of Settings with defaults of its own: the agents a project
// creates, or its lead — the project's chat. Each has its own model and context
// window, so a lead that plans and briefs can run on something other than the
// agents doing the work.
export type Role = 'agents' | 'lead';

// roles says, for each, where its defaults are kept and what an empty model
// means: AgentBox's own default for agents, Claude Code's own for the lead,
// which is what every lead ran on before it had a setting.
const roles = {
  agents: {
    title: 'Model for new agents',
    about: 'What a new Claude Code agent starts on, in every project.',
    more: "Agents you've already made keep the model they have. A project can pick its own in its settings, and what you pick as you create an agent wins over both.",
    windowTitle: 'Context window for new agents',
    windowAbout: "Where a new Claude Code agent's chat compacts.",
    windowMore: "Agents you've already made keep the window they have.",
    empty: { value: '', name: 'AgentBox default', description: 'Opus' },
    emptyModel: 'opus',
    model: (s: T.Settings) => s.defaultClaudeModel,
    window: (s: T.Settings) => s.defaultAgentContextWindow,
    save: (model: string, window?: string): T.UpdateSettingsRequest => ({ defaultClaudeModel: model, defaultAgentContextWindow: window }),
    saveWindow: (window: string): T.UpdateSettingsRequest => ({ defaultAgentContextWindow: window }),
  },
  lead: {
    title: 'Model for the lead',
    about: "What each project's chat runs on.",
    more: 'Unless you pick another in its composer, which wins for that project. Leads you already have move to it the next time their chat starts.',
    windowTitle: 'Context window for the lead',
    windowAbout: "Where each project's chat compacts.",
    windowMore: 'Unless you pick another in its composer. Applies from its next session.',
    empty: { value: '', name: "Claude Code's default", description: 'Whatever Claude Code picks for the account' },
    emptyModel: 'default',
    model: (s: T.Settings) => s.defaultLeadModel,
    window: (s: T.Settings) => s.defaultLeadContextWindow,
    save: (model: string, window?: string): T.UpdateSettingsRequest => ({ defaultLeadModel: model, defaultLeadContextWindow: window }),
    saveWindow: (window: string): T.UpdateSettingsRequest => ({ defaultLeadContextWindow: window }),
  },
} as const;

// fullWindow is the 1M window, stored as its count of tokens.
const fullWindow = '1000000';

// windowsFor are the context windows a role's default model has, smallest
// first, from the ones the daemon worked out per model. A model it has no entry
// for — one typed by name — is taken to have only the first, which is the
// daemon's own answer for a model it has never seen run.
function windowsFor(settings: T.Settings | undefined, role: Role): number[] {
  if (!settings) return [];
  const r = roles[role];
  const model = r.model(settings) || r.emptyModel;
  const all = settings.claudeContextWindows ?? {};
  return all[model] ?? all[model.replace(/\[1m\]$/, '')] ?? (all.default ?? []).slice(0, 1);
}

// DefaultModel chooses the model a role's Claude Code chats start on, for
// every project — the setting says what you want an agent to cost and be
// capable of, which doesn't change from one repository to the next, and this
// page spans them all.
//
// The choices are the ones a Claude Code adapter really advertised, remembered
// from the last chat that started (see rememberChoices in internal/chat),
// plus AgentBox's own small pinned list — Fable, chief among them, marked
// with a note since the account gates it (D69).
export function DefaultModel({ role }: { role: Role }) {
  const r = roles[role];
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const [query, setQuery] = useState('');
  const save = useMutation({
    // A model without the 1M window takes the role's window back to the
    // compact one in the same request: the daemon refuses the pair otherwise.
    mutationFn: (model: string) => {
      const all = settings.data?.claudeContextWindows ?? {};
      const windows = all[model || r.emptyModel] ?? (all.default ?? []).slice(0, 1);
      const drop = settings.data && r.window(settings.data) === fullWindow && !windows.includes(Number(fullWindow));
      return api.updateSettings(r.save(model, drop ? '' : undefined));
    },
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const choices = settings.data?.claudeModelChoices ?? [];
  const value = (settings.data && r.model(settings.data)) ?? '';
  const current = choices.find((c) => c.value === value);
  const missing = unavailableValue(choices, value);
  const groups = groupChoices(choices.filter((c) => matchesQuery(c, query)));
  const searchable = choices.length >= searchThreshold;
  const label = !settings.data ? 'Loading…' : value === '' ? r.empty.name : (current && choiceName(current)) || value;
  const marker = role === 'agents' ? { 'data-default-model': true } : { 'data-lead-model': true };

  return (
    <SettingRow
      label={r.title}
      description={r.about}
      details={r.more}
      control={
        <Menu onOpenChange={(open) => !open && setQuery('')}>
          <MenuTrigger asChild>
            <button
              {...marker}
              disabled={save.isPending || settings.data === undefined}
              aria-label={r.title}
              title={label}
              className={cn(selectTrigger, missing && 'text-amber-300')}
            >
              {missing ? <CircleAlert className="size-4 shrink-0 text-amber-400" /> : <Sparkles className="size-4 shrink-0 text-brand-300" />}
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
            </button>
          </MenuTrigger>
          <MenuContent align="end" className="max-h-96 w-80 overflow-y-auto">
            <MenuLabel>{r.title}</MenuLabel>
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
            {matchesQuery(r.empty, query) && (
              <MenuItem onSelect={() => save.mutate('')} hint={value === '' ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                <span className="grid">
                  <span>{r.empty.name}</span>
                  <span className="text-[11px] text-subtle">{r.empty.description}</span>
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
            {settings.data && !settings.data.claudeMenuKnown && (
              <div className="px-2.5 py-3 text-[12px] leading-relaxed text-subtle">
                Only AgentBox's own picks so far. Claude Code sends the models your account may use when a chat starts — open one, and they'll be here.
              </div>
            )}
            <ModelByName onPick={(model) => save.mutate(model)} disabled={save.isPending} />
          </MenuContent>
        </Menu>
      }
    />
  );
}

// DefaultContextWindow chooses where a role's Claude Code chats compact (D91):
// the installation's compact window, set below under "Every agent", or the
// model's whole window. A default model without a 1M window, like Haiku, has
// nothing to choose, and the daemon refuses the pair anyway.
export function DefaultContextWindow({ role }: { role: Role }) {
  const r = roles[role];
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (window: string) => api.updateSettings(r.saveWindow(window)),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const windows = windowsFor(settings.data, role);
  const standard = windows[0] ?? settings.data?.claudeCompactWindow ?? 200_000;
  const hasFull = windows.includes(Number(fullWindow)) && standard !== Number(fullWindow);
  const value = (settings.data && r.window(settings.data)) || '';

  return (
    <SettingRow
      label={r.windowTitle}
      description={
        <>
          {r.windowAbout}
          {settings.data && !hasFull && ' This model has no 1M window.'}
        </>
      }
      details={
        <>
          {r.windowMore} Past the compact window every step resends the whole conversation, so 1M costs up to five times as much per step late in a long
          task.
        </>
      }
      control={
        <Select
          data-default-window={role}
          aria-label={r.windowTitle}
          disabled={save.isPending || settings.data === undefined}
          placeholder="Loading…"
          value={value}
          onChange={(next) => save.mutate(next)}
        >
          <SelectOption value="">{standard ? formatTokens(standard) : 'The model\'s whole window'} (default)</SelectOption>
          {(hasFull || value === fullWindow) && <SelectOption value={fullWindow}>1M, the model's whole window</SelectOption>}
        </Select>
      }
    />
  );
}

// NewAgentEffort chooses how hard new Claude Code agents think, beside the
// model above and for the same reason: it says what you want an agent to cost
// and be capable of, which doesn't change from one repository to the next.
// Either can be overridden for one agent as it is created.
//
// A plain select, not the model's menu: the effort levels are a short
// ungrouped list with nothing to describe or search. They are still only the
// ones an adapter advertised (rememberChoices) — the levels differ per model,
// and some models offer none at all, so an invented one would set nothing.
export function NewAgentEffort() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (defaultClaudeEffort: string) => api.updateSettings({ defaultClaudeEffort }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const choices = settings.data?.claudeEffortChoices ?? [];
  const value = settings.data?.defaultClaudeEffort ?? '';

  return (
    <SettingRow
      label="Effort for new agents"
      description={
        <>
          How hard a new Claude Code agent thinks.
          {settings.data && !settings.data.claudeMenuKnown && ' The levels are here once a Claude Code chat has started.'}
        </>
      }
      details="Not every model has effort levels, and one that doesn't simply ignores this."

      control={
        <Select
          data-default-effort
          aria-label="Effort for new agents"
          disabled={save.isPending || settings.data === undefined}
          value={value}
          onChange={(next) => save.mutate(next)}
        >
          <SelectOption value="">AgentBox default (high)</SelectOption>
          {choices.map((choice) => (
            <SelectOption key={choice.value} value={choice.value}>
              {choiceName(choice) || choice.value}
            </SelectOption>
          ))}
          {/* A level this account has stopped offering keeps its place, so the
              control never shows something else as though you had picked it. */}
          {value !== '' && !choices.some((c) => c.value === value) && <SelectOption value={value}>{value} (unavailable)</SelectOption>}
        </Select>
      }
    />
  );
}

// NewAgentResources caps the cores and memory a new agent's machine may take from the host.
// This is the setting that decides whether an agent running a build or a test
// suite costs you your desktop, so it sits with the other two: like them it
// says what you want an agent to cost, which doesn't change from one
// repository to the next. Agents that already exist keep what they have —
// change one on its Overview tab, which applies while it runs.
//
// Each input is committed on blur rather than on every keystroke: "8GiB" is
// three invalid values on the way to a valid one, and the daemon refuses each
// of them.
export function NewAgentResources() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const cores = settings.data?.hostCores ?? 0;
  const memory = settings.data?.hostMemory ?? 0;

  return (
    <SettingRow
      label="Resources for new agents"
      description={
        <>
          The most CPU and memory each new agent's machine may take.{' '}
          <span className="text-tertiary">
            This host has {cores || '—'} cores and {memory ? humanBytes(memory) : '—'} of memory.
          </span>
        </>
      }
      details={
        <>
          Each limit is per agent, not a pool shared by all of them: three agents at 4 cores on an 8-core host each see 4, and share the host's 8. Empty
          means no limit. Changes apply to new agents only — change an existing one on its Overview tab. Past the memory ceiling the kernel kills processes
          inside the agent, which is kept out of the host's swap so it can't slow the host down first.
        </>
      }
    >
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <ResourceField
          id="default-cpu"
          label="CPU cores"
          placeholder="every core"
          hint="How many cores it sees, even when the host is idle. A new installation starts at 2."
          value={settings.data?.defaultCPU ?? ''}
          disabled={save.isPending || settings.isPending}
          onCommit={(defaultCPU) => save.mutate({ defaultCPU })}
        />
        <ResourceField
          id="default-memory"
          label="Memory"
          placeholder="all of it"
          hint={`A hard ceiling. A new installation starts at 8GiB, or half the host's memory if that's less${settings.data?.seedMemory ? ` — ${settings.data.seedMemory} here` : ''}.`}
          value={settings.data?.defaultMemory ?? ''}
          disabled={save.isPending || settings.isPending}
          onCommit={(defaultMemory) => save.mutate({ defaultMemory })}
        />
      </div>
    </SettingRow>
  );
}

// NewAgentCPUShare is the third of the new agents' limits: the share of the
// CPU a new agent gets (Incus limits.cpu.allowance). Most never change it —
// cores and memory are what decide whether an agent costs you your desktop —
// so Settings keeps it with the advanced ones.
export function NewAgentCPUShare() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (defaultCPUAllowance: string) => api.updateSettings({ defaultCPUAllowance }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  return (
    <SettingRow
      label="CPU share for new agents"
      htmlFor="default-cpu-allowance"
      description="How much of the CPU a new agent gets when agents compete for it."
      details="50% only matters when agents compete for the CPU. 25ms/100ms is a hard ceiling, a quarter of one core, even on an idle host. Empty is all of it."
      control={
        <CommitInput
          id="default-cpu-allowance"
          placeholder="all of it"
          value={settings.data?.defaultCPUAllowance ?? ''}
          disabled={save.isPending || settings.isPending}
          onCommit={(value) => save.mutate(value)}
        />
      }
    />
  );
}

// ResourceField is a labelled CommitInput.
function ResourceField({
  id,
  label,
  placeholder,
  hint,
  value,
  disabled,
  onCommit,
}: {
  id: string;
  label: string;
  placeholder: string;
  hint: string;
  value: string;
  disabled?: boolean;
  onCommit: (value: string) => void;
}) {
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <CommitInput id={id} placeholder={placeholder} value={value} disabled={disabled} onCommit={onCommit} />
    </Field>
  );
}

// CommitInput is a text box that saves what you typed when you leave it, and
// follows the stored value when that changes underneath.
function CommitInput({
  id,
  placeholder,
  value,
  disabled,
  onCommit,
}: {
  id: string;
  placeholder: string;
  value: string;
  disabled?: boolean;
  onCommit: (value: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  const [edited, setEdited] = useState(false);
  // While you're typing, the box is yours; otherwise it shows what is stored.
  const shown = edited ? draft : value;
  const commit = () => {
    setEdited(false);
    if (draft.trim() !== value) onCommit(draft.trim());
  };
  return (
    <Input
      id={id}
      data-setting={id}
      disabled={disabled}
      placeholder={placeholder}
      value={shown}
      onChange={(event) => {
        setEdited(true);
        setDraft(event.target.value);
      }}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter') event.currentTarget.blur();
        if (event.key === 'Escape') {
          setEdited(false);
          event.currentTarget.blur();
        }
      }}
    />
  );
}

// ResumeAfterLimit decides whether a chat carries itself on after a Claude
// usage limit. It sits with the settings above because the limit is the
// account's — every agent on it hits the same one at the same moment — but
// unlike them it applies to the agents you already have, not only to the next
// one: it is about what happens to work that is already running.
export function ResumeAfterLimit() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (resumeAfterLimit: boolean) => api.updateSettings({ resumeAfterLimit }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Resume after a usage limit"
      description="When the Claude account's usage runs out mid-turn, carry on where it left off once the limit resets."
      details="Off, the turn stays failed until you send a message. Anything you do to the chat in the meantime — a message, stopping it, stopping the agent — calls the wait off. It reaches the agents you already have."
      control={
        <Switch
          data-resume-after-limit
          aria-label="Resume after a usage limit"
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.resumeAfterLimit ?? true}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}

// NeverFreezeCPU keeps every running agent's CPU cap adding up to at most
// this host's cores minus what you keep free, recomputed live as agents
// start, stop, are paused, resumed, created or destroyed (D95) — not only
// when this setting itself changes. It sits with the settings that reach
// every agent because that's who it caps: an agent's own limit, set here or
// per agent on its Overview tab, is its ceiling, never raised past — this
// only ever holds the sum of them down further, and puts back what was
// really chosen once there's room, or once it's turned off.
export function NeverFreezeCPU() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const cores = settings.data?.hostCores ?? 0;
  const keepFree = settings.data?.keepFreeCPU ?? 1;
  const on = settings.data?.neverFreezeCPU ?? false;

  return (
    <SettingRow
      label="Never freeze my CPU"
      description={
        <>
          Keeps the running agents' CPU caps adding up to less than this host's cores.
          {cores ? <span className="text-tertiary"> This host has {cores}.</span> : null}
        </>
      }
      details="They sum to at most this host's cores minus the ones you keep free, so the agents can't starve it between them the way an uncapped build on all of them at once would. It's worked out again whenever an agent starts, stops, pauses or resumes. An agent's own limit is never raised, and what you chose comes back once there's room, or once this is off."
      control={
        <Switch
          data-never-freeze-cpu
          aria-label="Never freeze my CPU"
          disabled={save.isPending || settings.data === undefined}
          checked={on}
          onCheckedChange={(neverFreezeCPU) => save.mutate({ neverFreezeCPU })}
        />
      }
    >
      {on && (
        <div className="max-w-40">
          <ResourceField
            id="keep-free-cpu"
            label="Cores to keep free"
            placeholder="1"
            hint={`How many cores stay outside every agent's cap, for this host and everything else on it. Default 1.${cores ? ` This host has ${cores}.` : ''}`}
            value={String(keepFree)}
            disabled={save.isPending || settings.isPending}
            onCommit={(value) => {
              const n = Math.trunc(Number(value));
              if (!Number.isFinite(n) || n < 0 || value.trim() === '') {
                toast.error('Cores to keep free is a whole number, at least 0');
                return;
              }
              save.mutate({ keepFreeCPU: n });
            }}
          />
        </div>
      )}
    </SettingRow>
  );
}

// SharedBudget puts every agent's machine under one cgroup with one memory,
// swap and CPU budget between them (internal/agent/budget.go), so an idle
// agent's share goes to a busy one instead of sitting reserved. The daemon
// suggests a size from this host's memory, cores and swap; the fields show
// it until you change them, and "Use suggested" goes back to it. The cgroup
// needs root once, so until it exists the switch stays off and the row says
// what's missing, with a button that asks for your password through pkexec.
// There is no row at all where the budget can't be: in a Mac's VM, in WSL,
// or on a host without cgroup v2.
export function SharedBudget() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const setup = useMutation({
    mutationFn: () => window.agentbox.hostSetup.budget(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['settings'] }),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const b = settings.data?.sharedBudget;
  if (!b || b.unsupported) return null;
  const busy = save.isPending || settings.isPending;
  const suggested = b.suggested;

  return (
    <SettingRow
      label="Shared agent budget"
      description="One memory, swap and CPU budget for all agents, so what an idle agent isn't using goes to a busy one. On the disk, agents give way to your own apps."
      details="The host keeps the rest. Each agent's own limits still hold inside it. Agents already running move in when they restart. The size starts at what AgentBox suggests for this host."
      control={
        <Switch
          data-shared-budget
          aria-label="Shared agent budget"
          disabled={busy || (!b.on && !!b.notReady)}
          checked={b.on}
          onCheckedChange={(sharedBudget) => save.mutate({ sharedBudget })}
        />
      }
    >
      <div className="grid gap-3">
        {b.on && (
          <>
            <div className="grid items-start gap-4 sm:grid-cols-3">
              <ResourceField
                id="shared-budget-memory"
                label="Memory"
                placeholder={suggested.memory}
                hint={`All agents together. Suggested: ${suggested.memory}.`}
                value={b.memory}
                disabled={busy}
                onCommit={(sharedBudgetMemory) => save.mutate({ sharedBudgetMemory })}
              />
              {b.hostSwap > 0 && (
                <ResourceField
                  id="shared-budget-swap"
                  label="Swap"
                  placeholder={suggested.swap}
                  hint={`Never 0: without swap, memory pressure stalls agents. Suggested: ${suggested.swap}.`}
                  value={b.swap}
                  disabled={busy}
                  onCommit={(sharedBudgetSwap) => save.mutate({ sharedBudgetSwap })}
                />
              )}
              <ResourceField
                id="shared-budget-cpu"
                label="CPU cores"
                placeholder={String(suggested.cpu)}
                hint={`Shared by every agent. Suggested: ${suggested.cpu}.`}
                value={String(b.cpu)}
                disabled={busy}
                onCommit={(value) => {
                  const n = value === '' ? 0 : Math.trunc(Number(value));
                  if (!Number.isFinite(n) || n < 0) {
                    toast.error('CPU cores is a whole number');
                    return;
                  }
                  save.mutate({ sharedBudgetCPU: n });
                }}
              />
            </div>
            <div className="grid items-start gap-4 sm:grid-cols-3" data-shared-budget-disk>
              <ResourceField
                id="shared-budget-disk-weight"
                label="Disk weight"
                placeholder={String(suggested.diskWeight)}
                hint={`Against 100 for your own apps, while they need the disk${b.disk ? ` (${b.disk})` : ''}. Suggested: ${suggested.diskWeight}.`}
                value={String(b.diskWeight)}
                disabled={busy}
                onCommit={(value) => {
                  const n = value === '' ? 0 : Math.trunc(Number(value));
                  if (!Number.isFinite(n) || n < 0 || n > 100) {
                    toast.error('Disk weight is a whole number from 1 to 100');
                    return;
                  }
                  save.mutate({ sharedBudgetDiskWeight: n });
                }}
              />
              <ResourceField
                id="shared-budget-disk-write"
                label="Disk writes a second"
                placeholder={suggested.diskWrite}
                hint={`All agents together, or max. Well under what a cheap SSD writes once its cache is full. Suggested: ${suggested.diskWrite}.`}
                value={b.diskWrite}
                disabled={busy}
                onCommit={(sharedBudgetDiskWrite) => save.mutate({ sharedBudgetDiskWrite })}
              />
            </div>
            <SettingNote>
              <span data-shared-budget-why>{b.why}</span>
              {b.chosen && (
                <>
                  {' '}
                  <button
                    type="button"
                    className="text-secondary underline underline-offset-2 hover:text-primary disabled:opacity-50"
                    disabled={busy}
                    onClick={() =>
                      save.mutate({ sharedBudgetMemory: '', sharedBudgetSwap: '', sharedBudgetCPU: 0, sharedBudgetDiskWeight: 0, sharedBudgetDiskWrite: '' })
                    }
                  >
                    Use suggested
                  </button>
                </>
              )}
            </SettingNote>
          </>
        )}
        {b.on && !b.problem && (
          <SettingNote>
            {b.inside} running agent{b.inside === 1 ? ' is' : 's are'} inside it.
            {b.pending > 0 && ` ${b.pending} more move${b.pending === 1 ? 's' : ''} in when ${b.pending === 1 ? 'it restarts' : 'they restart'}.`}
          </SettingNote>
        )}
        {!b.on && b.pending > 0 && (
          <SettingNote>
            {b.pending} running agent{b.pending === 1 ? '' : 's'} leave{b.pending === 1 ? 's' : ''} it when {b.pending === 1 ? 'it restarts' : 'they restart'}.
          </SettingNote>
        )}
        {(b.problem || b.notReady || (b.on && b.diskNotReady)) && (
          <div className="grid gap-2" data-shared-budget-setup>
            <SettingNote tone={b.problem ? 'error' : 'warning'}>
              <CircleAlert className="mr-1 inline size-3.5 align-[-2px]" />
              {b.problem || (b.notReady ? `Before it can be turned on, ${b.notReady}` : b.diskNotReady)}
            </SettingNote>
            <div className="flex flex-wrap items-center gap-3">
              <Button size="sm" disabled={setup.isPending} onClick={() => setup.mutate()}>
                {setup.isPending ? 'Setting up…' : 'Set up'}
              </Button>
              <span className="text-xs text-subtle">
                or in a terminal: <code className="font-mono text-tertiary">{b.setupCommand}</code>
              </span>
            </div>
          </div>
        )}
      </div>
    </SettingRow>
  );
}

// idleTimes are the presets offered for how long an agent may go idle before
// "auto-stop idle agents" stops it.
const idleTimes = [30 * 60, 60 * 60, 2 * 60 * 60, 4 * 60 * 60, 8 * 60 * 60];

function idleTimeWords(seconds: number): string {
  const hours = seconds / 3600;
  if (hours >= 1 && hours === Math.trunc(hours)) return `${hours}h`;
  const minutes = seconds / 60;
  return `${minutes}m`;
}

// AutoStopIdle stops a running or paused agent, keeping its worktree and
// branch, once nothing has happened on it — no chat turn, no job, no waiting
// question or credential request, no terminal input, no recording — for as
// long as the idle time below. Off by default, so an agent left alone
// overnight isn't stopped unless you asked for that.
export function AutoStopIdle() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const on = settings.data?.autoStopIdle ?? false;
  const fallback = 2 * 60 * 60;
  const idleTime = settings.data?.idleTimeSeconds ?? fallback;

  return (
    <SettingRow
      label="Auto-stop idle agents"
      description="Stops an agent once nothing has happened on it for a while. Its worktree and branch stay."
      details="Nothing means no chat turn, no job, no waiting question or credential request, no terminal input and no recording. It's the same as stopping it by hand, and applies to running and paused agents alike."
      control={
        <Switch
          data-auto-stop-idle
          aria-label="Auto-stop idle agents"
          disabled={save.isPending || settings.data === undefined}
          checked={on}
          onCheckedChange={(autoStopIdle) => save.mutate({ autoStopIdle })}
        />
      }
    >
      {on && (
        <div className="max-w-40">
          <Select
            data-idle-time
            aria-label="Idle time"
            disabled={save.isPending || settings.data === undefined}
            value={String(idleTime)}
            onChange={(next) => save.mutate({ idleTimeSeconds: Number(next) })}
          >
            {idleTimes.map((n) => (
              <SelectOption key={n} value={String(n)}>
                {idleTimeWords(n)}
                {n === fallback ? ' (default)' : ''}
              </SelectOption>
            ))}
            {!idleTimes.includes(idleTime) && <SelectOption value={String(idleTime)}>{idleTimeWords(idleTime)}</SelectOption>}
          </Select>
        </div>
      )}
    </SettingRow>
  );
}

// compactWindows are the points a chat can be set to compact at: Claude Code's
// own bounds (100k to 1M) at the steps worth choosing between. Codex shares
// them; OpenCode has no such setting at all (D83).
const compactWindows = [100_000, 150_000, 200_000, 300_000, 500_000, 1_000_000];

// CompactWindow is how full a Claude Code or Codex chat's context may get
// before its tool summarises it and carries on (D83) — OpenCode has nothing
// this reaches, only settings of its own relative to the model's context. It
// sits with the settings that reach every agent because it is the one that
// decides what a long piece of work costs: every step sends the whole
// conversation again, so a session left to fill a 1M window pays up to five
// times per step what one held at 200k does. Like the resume switch it
// applies to agents you already have, from each chat's next session.
export function CompactWindow() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (claudeCompactWindow: number) => api.updateSettings({ claudeCompactWindow }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const fallback = settings.data?.defaultClaudeCompactWindow ?? 200_000;
  const value = settings.data?.claudeCompactWindow ?? fallback;

  return (
    <SettingRow
      label="Compact chats at"
      description="How full a chat's context gets before it's summarised and carried on. Smaller spends less, larger forgets less."
      details="For Claude Code and Codex; OpenCode has no such setting. Every step an agent takes sends its whole conversation again, so this decides what long work costs per step. Applies from each chat's next session, the lead's included."
      control={
        <Select
          data-compact-window
          aria-label="Compact chats at"
          disabled={save.isPending || settings.data === undefined}
          value={String(value)}
          onChange={(next) => save.mutate(Number(next))}
        >
          {compactWindows.map((n) => (
            <SelectOption key={n} value={String(n)}>
              {formatTokens(n)} tokens{n === fallback ? ' (default)' : ''}
            </SelectOption>
          ))}
          {/* A value set from the command line keeps its place, so the control
              never shows another one as though you had picked it. */}
          {value !== 0 && !compactWindows.includes(value) && <SelectOption value={String(value)}>{formatTokens(value)} tokens</SelectOption>}
          <SelectOption value="0">The model's whole window</SelectOption>
        </Select>
      }
    />
  );
}

// mediaRetentions are how long a removed agent's media can be kept, in the
// order they are offered. The values are api.MediaRetention's.
const mediaRetentions = [
  { value: 'immediately', label: 'Delete it with the agent' },
  { value: '1d', label: '1 day' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: 'forever', label: 'Forever' },
];

// MediaRetention is how long an agent's screenshots, recordings and reports
// outlive it. It belongs to the installation rather than to a project: it is
// about this machine's disk, and every project's removed agents fill it the
// same way — more so now that finished agents are removed on their own.
export function MediaRetention() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (mediaRetention: string) => api.updateSettings({ mediaRetention }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label="Keep a removed agent's media for"
      description="How long an agent's screenshots, recordings and reports outlive it."
      details="They stay in the project's Media after the agent is destroyed — by you, or on its own once its pull request is merged or closed — and are purged when this runs out. An agent that still exists keeps all of its media."
      control={
        <Select
          data-media-retention
          aria-label="Keep a removed agent's media for"
          disabled={save.isPending || settings.data === undefined}
          value={settings.data?.mediaRetention ?? '1d'}
          onChange={(next) => save.mutate(next)}
        >
          {mediaRetentions.map((r) => (
            <SelectOption key={r.value} value={r.value}>
              {r.label}
              {r.value === '1d' ? ' (default)' : ''}
            </SelectOption>
          ))}
        </Select>
      }
    />
  );
}

// OpenCodeInImage turns OpenCode on or off in the base image, which is what
// decides whether an agent can be created on it at all. It belongs here rather
// than with the Environment checks for the same reason the model does: it is a
// choice about what your agents can be, made once for every project.
//
// Turning it on is a rebuild, because the image is where the tool lives: the
// switch saves the choice and starts that build, and says so before it does.
// Agents that already exist are copies and are left alone.
export function OpenCodeInImage() {
  const queryClient = useQueryClient();
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup });
  const [job, setJob] = useState<string | null>(null);
  const build = useMutation({
    mutationFn: (opencode: boolean) => api.buildImage({ opencode }),
    onSuccess: (started) => setJob(started.id),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const wanted = setup.data?.image.components.opencode === true;
  const installed = setup.data?.image.installed.opencode === true;

  return (
    <SettingRow
      label="OpenCode in the base image"
      description="Adds OpenCode to the base image, so agents can be created on it."
      details="The OpenCode CLI is its own chat adapter. About 120 MB, and turning it on rebuilds the image. Agents that already exist are unaffected."
      control={
        <Switch
          data-image-opencode
          aria-label="OpenCode in the base image"
          disabled={build.isPending || job !== null || setup.data === undefined}
          checked={wanted}
          onCheckedChange={(next) => build.mutate(next)}
        />
      }
    >
      {wanted && !installed && job === null && (
        <SettingNote tone="warning">The image doesn't have OpenCode yet: rebuild it under Base image above, or run agentbox image build.</SettingNote>
      )}
      {job && <JobProgress jobId={job} onDone={() => void queryClient.invalidateQueries({ queryKey: ['setup'] })} />}
    </SettingRow>
  );
}

// GPUForAgents passes this host's GPU into every agent's container, as an
// Incus gpu device, so its browser, Electron, Playwright and Android emulator
// render on it instead of software (SwiftShader or llvmpipe). Only shown when
// this host actually has one to give — a Mesa DRM render node or an NVIDIA
// device — which Settings reports as gpuAvailable/gpuKind. Off by default,
// and like NeverFreezeCPU it reaches the agents you already have as well as
// the next one, the moment it changes.
export function GPUForAgents() {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (gpuForAgents: boolean) => api.updateSettings({ gpuForAgents }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  if (settings.data && !settings.data.gpuAvailable) return null;

  const nvidia = settings.data?.gpuKind === 'nvidia';
  const kind = nvidia ? 'its NVIDIA GPU' : 'its GPU';
  const restart = nvidia
    ? " Existing agents need a restart to pick up the NVIDIA driver runtime: it's part of how their container starts, not something Incus can add to one already running."
    : '';

  return (
    <SettingRow
      label="GPU for agents"
      description={`Passes this host's ${kind} into every agent, so its browser and emulator render on it instead of software.`}
      details={`Chromium, Electron, Playwright and the Android emulator all render on it, and a recording of an agent's display encodes on it. It reaches the agents you already have the moment it changes.${restart}`}
      control={
        <Switch
          data-gpu-for-agents
          aria-label="GPU for agents"
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.gpuForAgents ?? false}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}
