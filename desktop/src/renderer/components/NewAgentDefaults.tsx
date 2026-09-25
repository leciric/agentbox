import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronDown, CircleAlert, Search, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatTokens } from '../lib/chat';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../lib/modelChoices';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { JobProgress } from './JobProgress';
import { ModelByName } from './ModelByName';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';
import { Panel } from './ui/card';
import { Field, Input } from './ui/input';
import { Select, SelectOption } from './ui/select';
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
    about: "What a new Claude Code agent starts on, in every project. Agents you've already made keep the model they have.",
    windowTitle: 'Context window for new agents',
    windowAbout: "Where a new Claude Code agent's chat compacts. Agents you've already made keep the window they have.",
    empty: { value: '', name: 'AgentBox default', description: 'Opus' },
    emptyModel: 'opus',
    model: (s: T.Settings) => s.defaultClaudeModel,
    window: (s: T.Settings) => s.defaultAgentContextWindow,
    save: (model: string, window?: string): T.UpdateSettingsRequest => ({ defaultClaudeModel: model, defaultAgentContextWindow: window }),
    saveWindow: (window: string): T.UpdateSettingsRequest => ({ defaultAgentContextWindow: window }),
  },
  lead: {
    title: "Model for the lead",
    about: "What each project's chat runs on, unless you pick another in its composer. Leads you already have move to it the next time their chat starts.",
    windowTitle: "Context window for the lead",
    windowAbout: 'Where the lead\'s chat compacts, unless you pick another in its composer. Applies from its next session.',
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
  const label = value === '' ? r.empty.name : (current && choiceName(current)) || value;
  const marker = role === 'agents' ? { 'data-default-model': true } : { 'data-lead-model': true };

  return (
    <Panel className="flex flex-wrap items-center gap-x-4 gap-y-3 p-4">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-primary">{r.title}</div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">{r.about}</p>
      </div>
      <Menu onOpenChange={(open) => !open && setQuery('')}>
        <MenuTrigger asChild>
          <button
            {...marker}
            disabled={save.isPending}
            aria-label={r.title}
            className={cn(
              'flex h-9 min-w-[13rem] items-center gap-2 rounded-xl border border-line-strong bg-surface-faint px-3 text-[13px] transition hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/40 disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0',
              missing ? 'text-amber-300' : 'text-primary',
            )}
          >
            {missing ? <CircleAlert className="text-amber-400" /> : <Sparkles className="text-brand-300" />}
            <span className="min-w-0 flex-1 truncate text-left">{label}</span>
            <ChevronDown className="!size-3.5 text-faint" />
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
    </Panel>
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
    <Panel className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-3 p-4">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-primary">{r.windowTitle}</div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          {r.windowAbout} Past the compact window every step resends the whole conversation, so 1M costs up to five times as much per step late in a long task.
          {settings.data && !hasFull && ' This model has no 1M window.'}
        </p>
      </div>
      <Select
        data-default-window={role}
        aria-label={r.windowTitle}
        disabled={save.isPending || settings.data === undefined}
        className="w-[13rem] flex-none rounded-xl"
        value={value}
        onChange={(next) => save.mutate(next)}
      >
        <SelectOption value="">{standard ? formatTokens(standard) : 'The model\'s whole window'} (default)</SelectOption>
        {(hasFull || value === fullWindow) && <SelectOption value={fullWindow}>1M, the model's whole window</SelectOption>}
      </Select>
    </Panel>
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
    <Panel className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-3 p-4">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-primary">Effort for new agents</div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          How hard a new Claude Code agent thinks. Not every model has effort levels, and one that doesn't simply ignores this.
          {settings.data && !settings.data.claudeMenuKnown && ' The levels are here once a Claude Code chat has started.'}
        </p>
      </div>
      <Select
        data-default-effort
        aria-label="Effort for new agents"
        disabled={save.isPending}
        className="w-[13rem] flex-none rounded-xl"
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
    </Panel>
  );
}

// NewAgentResources caps what a new agent's machine may take from the host.
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
    <Panel className="p-4">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <div className="text-[13px] font-medium text-primary">Resources for new agents</div>
        <div className="text-[12px] text-subtle">
          This host has {cores || '—'} cores and {memory ? humanBytes(memory) : '—'} of memory.
        </div>
      </div>
      <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
        Each limit is per agent, not a pool shared by all of them: three agents at 4 cores on an 8-core host each see 4, and share the host's 8. Empty means
        no limit. Changes apply to new agents only — change an existing one on its Overview tab.
      </p>
      <div className="mt-3.5 grid gap-4 sm:grid-cols-3">
        <ResourceField
          id="default-cpu"
          label="CPU cores"
          placeholder="every core"
          hint={`How many cores the agent sees and can use, even when the host is idle. A new installation starts at 2; empty is every core.${cores ? ` This host has ${cores}.` : ''}`}
          value={settings.data?.defaultCPU ?? ''}
          disabled={save.isPending || settings.isPending}
          onCommit={(defaultCPU) => save.mutate({ defaultCPU })}
        />
        <ResourceField
          id="default-cpu-allowance"
          label="CPU share"
          placeholder="all of it"
          hint="50% only matters when agents compete for the CPU. 25ms/100ms is a hard ceiling, a quarter of one core, even on an idle host."
          value={settings.data?.defaultCPUAllowance ?? ''}
          disabled={save.isPending || settings.isPending}
          onCommit={(defaultCPUAllowance) => save.mutate({ defaultCPUAllowance })}
        />
        <ResourceField
          id="default-memory"
          label="Memory"
          placeholder="all of it"
          hint="A hard ceiling, like 8GiB. Past it, the kernel kills processes inside the agent."
          value={settings.data?.defaultMemory ?? ''}
          disabled={save.isPending || settings.isPending}
          onCommit={(defaultMemory) => save.mutate({ defaultMemory })}
        />
      </div>
    </Panel>
  );
}

// ResourceField is a text box that saves what you typed when you leave it, and
// follows the stored value when that changes underneath.
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
  const [draft, setDraft] = useState(value);
  const [edited, setEdited] = useState(false);
  // While you're typing, the box is yours; otherwise it shows what is stored.
  const shown = edited ? draft : value;
  const commit = () => {
    setEdited(false);
    if (draft.trim() !== value) onCommit(draft.trim());
  };
  return (
    <Field label={label} htmlFor={id} hint={hint}>
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
    </Field>
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
    <Panel className="flex flex-wrap items-center gap-x-4 gap-y-3 p-4">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-primary">Resume after a usage limit</div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          When Claude Code stops an agent mid-turn because the account's usage is spent, wait for the limit to reset and ask it to carry on where it
          left off. Off, the turn stays failed until you send a message. Anything you do to the chat in the meantime — a message, stopping it, stopping
          the agent — calls the wait off.
        </p>
      </div>
      <Switch
        data-resume-after-limit
        aria-label="Resume after a usage limit"
        disabled={save.isPending || settings.data === undefined}
        checked={settings.data?.resumeAfterLimit ?? true}
        onCheckedChange={(next) => save.mutate(next)}
      />
    </Panel>
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
    <Panel className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-3 p-4">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-primary">Compact chats at</div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          How full a Claude Code or Codex chat's context gets before it is summarised and carried on (OpenCode has no such setting). Every step an
          agent takes sends its whole conversation again, so this decides what long work costs per step — smaller spends less, larger forgets
          less. Applies from each chat's next session.
        </p>
      </div>
      <Select
        data-compact-window
        aria-label="Compact chats at"
        disabled={save.isPending || settings.data === undefined}
        className="w-[13rem] flex-none rounded-xl"
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
    </Panel>
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
    <Panel className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-3 p-4">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-primary">Keep a removed agent's media for</div>
        <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
          Screenshots, recordings and reports stay in the project's media view after their agent is destroyed — by you, or on its own once its pull
          request is merged or closed — and are purged when this runs out. An agent that still exists keeps all of its media.
        </p>
      </div>
      <Select
        data-media-retention
        aria-label="Keep a removed agent's media for"
        disabled={save.isPending || settings.data === undefined}
        className="w-[13rem] flex-none rounded-xl"
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
    </Panel>
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
    <Panel className="mt-3 grid gap-3 p-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium text-primary">OpenCode in the base image</div>
          <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
            Adds the OpenCode CLI, which is its own chat adapter, so agents can be created on it. About 120 MB, and turning it on rebuilds the image.
            Agents that already exist are unaffected.
          </p>
        </div>
        <Switch
          data-image-opencode
          aria-label="OpenCode in the base image"
          disabled={build.isPending || job !== null || setup.data === undefined}
          checked={wanted}
          onCheckedChange={(next) => build.mutate(next)}
        />
      </div>
      {wanted && !installed && job === null && (
        <p className="text-[12px] text-amber-300">The image doesn't have OpenCode yet: rebuild it in the Environment tab, or run agentbox image build.</p>
      )}
      {job && <JobProgress jobId={job} onDone={() => void queryClient.invalidateQueries({ queryKey: ['setup'] })} />}
    </Panel>
  );
}
