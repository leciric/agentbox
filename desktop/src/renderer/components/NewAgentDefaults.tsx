import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, CircleAlert, Search, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatTokens } from '../lib/chat';
import { useT } from '../lib/i18n';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../lib/modelChoices';
import { cn, errorMessage, humanBytes, parseBytes } from '../lib/utils';
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
    title: 'defaults.newAgent.agents.title',
    about: 'defaults.newAgent.agents.about',
    more: 'defaults.newAgent.agents.more',
    windowTitle: 'defaults.newAgent.agents.windowTitle',
    windowAbout: 'defaults.newAgent.agents.windowAbout',
    windowMore: 'defaults.newAgent.agents.windowMore',
    emptyName: 'defaults.newAgent.agents.emptyName',
    emptyDescription: 'defaults.newAgent.agents.emptyDescription',
    emptyModel: 'opus',
    model: (s: T.Settings) => s.defaultClaudeModel,
    window: (s: T.Settings) => s.defaultAgentContextWindow,
    save: (model: string, window?: string): T.UpdateSettingsRequest => ({ defaultClaudeModel: model, defaultAgentContextWindow: window }),
    saveWindow: (window: string): T.UpdateSettingsRequest => ({ defaultAgentContextWindow: window }),
  },
  lead: {
    title: 'defaults.newAgent.lead.title',
    about: 'defaults.newAgent.lead.about',
    more: 'defaults.newAgent.lead.more',
    windowTitle: 'defaults.newAgent.lead.windowTitle',
    windowAbout: 'defaults.newAgent.lead.windowAbout',
    windowMore: 'defaults.newAgent.lead.windowMore',
    emptyName: 'defaults.newAgent.lead.emptyName',
    emptyDescription: 'defaults.newAgent.lead.emptyDescription',
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
  const t = useT();
  const r = roles[role];
  const empty = { value: '', name: t(r.emptyName), description: t(r.emptyDescription) };
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
  const label = !settings.data ? t('common.loading') : value === '' ? empty.name : (current && choiceName(current)) || value;
  const marker = role === 'agents' ? { 'data-default-model': true } : { 'data-lead-model': true };

  return (
    <SettingRow
      label={t(r.title)}
      description={t(r.about)}
      details={t(r.more)}
      control={
        <Menu onOpenChange={(open) => !open && setQuery('')}>
          <MenuTrigger asChild>
            <button
              {...marker}
              disabled={save.isPending || settings.data === undefined}
              aria-label={t(r.title)}
              title={label}
              className={cn(selectTrigger, missing && 'text-amber-300')}
            >
              {missing ? <CircleAlert className="size-4 shrink-0 text-amber-400" /> : <Sparkles className="size-4 shrink-0 text-brand-300" />}
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
            </button>
          </MenuTrigger>
          <MenuContent align="end" className="max-h-96 w-80 overflow-y-auto">
            <MenuLabel>{t(r.title)}</MenuLabel>
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
            {matchesQuery(empty, query) && (
              <MenuItem onSelect={() => save.mutate('')} hint={value === '' ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                <span className="grid">
                  <span>{empty.name}</span>
                  <span className="text-[11px] text-subtle">{empty.description}</span>
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
            {settings.data && !settings.data.claudeMenuKnown && (
              <div className="px-2.5 py-3 text-[12px] leading-relaxed text-subtle">
                {t('defaults.newAgent.onlyOwnPicks')}
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
  const t = useT();
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
      label={t(r.windowTitle)}
      description={
        <>
          {t(r.windowAbout)}
          {settings.data && !hasFull && ` ${t('defaults.newAgent.noFullWindow')}`}
        </>
      }
      details={`${t(r.windowMore)} ${t('defaults.newAgent.windowCost')}`}
      control={
        <Select
          data-default-window={role}
          aria-label={t(r.windowTitle)}
          disabled={save.isPending || settings.data === undefined}
          placeholder={t('common.loading')}
          value={value}
          onChange={(next) => save.mutate(next)}
        >
          <SelectOption value="">{t('defaults.newAgent.withDefault', { label: standard ? formatTokens(standard) : t('defaults.newAgent.wholeWindow') })}</SelectOption>
          {(hasFull || value === fullWindow) && <SelectOption value={fullWindow}>{t('defaults.newAgent.fullWindow')}</SelectOption>}
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
  const t = useT();
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
      label={t('defaults.newAgent.effortLabel')}
      description={
        <>
          {t('defaults.newAgent.effortDescription')}
          {settings.data && !settings.data.claudeMenuKnown && ` ${t('defaults.newAgent.effortLevelsLater')}`}
        </>
      }
      details={t('defaults.newAgent.effortDetails')}

      control={
        <Select
          data-default-effort
          aria-label={t('defaults.newAgent.effortLabel')}
          disabled={save.isPending || settings.data === undefined}
          value={value}
          onChange={(next) => save.mutate(next)}
        >
          <SelectOption value="">{t('defaults.newAgent.effortDefault')}</SelectOption>
          {choices.map((choice) => (
            <SelectOption key={choice.value} value={choice.value}>
              {choiceName(choice) || choice.value}
            </SelectOption>
          ))}
          {/* A level this account has stopped offering keeps its place, so the
              control never shows something else as though you had picked it. */}
          {value !== '' && !choices.some((c) => c.value === value) && <SelectOption value={value}>{t('defaults.newAgent.unavailable', { value })}</SelectOption>}
        </Select>
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
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (resumeAfterLimit: boolean) => api.updateSettings({ resumeAfterLimit }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.newAgent.resumeLabel')}
      description={t('defaults.newAgent.resumeDescription')}
      details={t('defaults.newAgent.resumeDetails')}
      control={
        <Switch
          data-resume-after-limit
          aria-label={t('defaults.newAgent.resumeLabel')}
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.resumeAfterLimit ?? true}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}

// DiskFloor is the disk guard's floor: the free space AgentBox keeps on every
// disk it writes to (internal/agent/diskguard.go), the larger of a size and a
// share of the disk. At it, new agents are refused and the agents writing the
// most are paused until there's room; the top bar says when that's near.
export function DiskFloor() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => {
      queryClient.setQueryData(['settings'], next);
      void queryClient.invalidateQueries({ queryKey: ['disk'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const min = settings.data?.diskFloorMin ?? 10 * 1024 ** 3;
  const percent = settings.data?.diskFloorPercent ?? 5;
  const disabled = save.isPending || settings.isPending;
  return (
    <SettingRow
      label={t('defaults.newAgent.diskLabel')}
      description={t('defaults.newAgent.diskDescription')}
      details={t('defaults.newAgent.diskDetails')}
    >
      <div className="grid max-w-md grid-cols-2 gap-3">
        <ResourceField
          id="disk-floor-min"
          label={t('defaults.newAgent.atLeast')}
          placeholder="10GiB"
          hint={t('defaults.newAgent.atLeastHint')}
          value={min % 1024 ** 3 === 0 ? `${min / 1024 ** 3}GiB` : humanBytes(min).replace(' ', '')}
          disabled={disabled}
          onCommit={(value) => {
            if (value.trim() === '') return save.mutate({ diskFloorMin: 0 });
            const bytes = parseBytes(value);
            if (bytes === undefined) {
              toast.error(t('defaults.newAgent.sizeLike', { example: '10GiB' }));
              return;
            }
            save.mutate({ diskFloorMin: bytes });
          }}
        />
        <ResourceField
          id="disk-floor-percent"
          label={t('defaults.newAgent.share')}
          placeholder="5%"
          hint={t('defaults.newAgent.shareHint')}
          value={`${percent}%`}
          disabled={disabled}
          onCommit={(value) => {
            if (value.trim() === '') return save.mutate({ diskFloorPercent: -1 });
            const n = Number(value.replace('%', '').trim());
            if (!Number.isFinite(n) || n < 0) {
              toast.error(t('defaults.newAgent.percentLike', { example: '5%' }));
              return;
            }
            save.mutate({ diskFloorPercent: n });
          }}
        />
      </div>
    </SettingRow>
  );
}

// CacheHolds is what a shared cache holds and the button that empties it,
// laid out like the "Hold at most" field beside it: the size where its label
// is, the button level with its box.
function CacheHolds({
  held,
  disabled,
  onEmpty,
  ...data
}: {
  held: number;
  disabled: boolean;
  onEmpty: () => void;
  [data: `data-${string}`]: boolean;
}) {
  const t = useT();
  return (
    <div className="grid content-start gap-1.5">
      <span className="text-[13px] font-medium text-tertiary" {...data}>
        {t('defaults.newAgent.holds', { size: humanBytes(held) })}
      </span>
      <Button className="h-9" disabled={disabled || held === 0} onClick={onEmpty}>
        {t('defaults.newAgent.emptyIt')}
      </Button>
    </div>
  );
}

// DockerImageCache is the image cache every agent's Docker shares
// (internal/imagecache): Docker Hub's images are downloaded and stored once in
// AgentBox's VM rather than once per agent. Turning it off points agents back
// at Docker Hub alone; the cap and the disk floor bound what it holds.
export function DockerImageCache() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const on = settings.data?.imageCache ?? true;
  const max = settings.data?.imageCacheMaxBytes ?? 20 * 1024 ** 3;
  const held = settings.data?.imageCacheBytes ?? 0;
  const disabled = save.isPending || settings.data === undefined;
  return (
    <SettingRow
      label={t('defaults.newAgent.imageCacheLabel')}
      description={t('defaults.newAgent.imageCacheDescription')}
      details={t('defaults.newAgent.imageCacheDetails')}
      control={
        <Switch
          data-image-cache
          aria-label={t('defaults.newAgent.imageCacheLabel')}
          disabled={disabled}
          checked={on}
          onCheckedChange={(imageCache) => save.mutate({ imageCache })}
        />
      }
    >
      <div className="grid max-w-md grid-cols-2 gap-3">
        <ResourceField
          id="image-cache-max"
          label={t('defaults.newAgent.holdAtMost')}
          placeholder="20GiB"
          hint={t('defaults.newAgent.holdHint')}
          value={max % 1024 ** 3 === 0 ? `${max / 1024 ** 3}GiB` : humanBytes(max).replace(' ', '')}
          disabled={disabled}
          onCommit={(value) => {
            if (value.trim() === '') return save.mutate({ imageCacheMaxBytes: 0 });
            const bytes = parseBytes(value);
            if (bytes === undefined) {
              toast.error(t('defaults.newAgent.sizeLike', { example: '20GiB' }));
              return;
            }
            save.mutate({ imageCacheMaxBytes: bytes });
          }}
        />
        <CacheHolds held={held} disabled={disabled} onEmpty={() => save.mutate({ clearImageCache: true })} data-image-cache-size />
      </div>
    </SettingRow>
  );
}

// SharedPackageCaches are the package managers' caches every agent shares
// (internal/pkgcache): pnpm's store, npm's, Go's, pip's and uv's caches,
// Corepack and Playwright's browsers live in AgentBox's VM and outlive the
// agents, so a dependency is downloaded once. The cap and the disk floor bound
// what they hold.
export function SharedPackageCaches() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const on = settings.data?.packageCache ?? true;
  const max = settings.data?.packageCacheMaxBytes ?? 20 * 1024 ** 3;
  const held = settings.data?.packageCacheBytes ?? 0;
  const disabled = save.isPending || settings.data === undefined;
  return (
    <SettingRow
      label={t('defaults.newAgent.pkgCacheLabel')}
      description={t('defaults.newAgent.pkgCacheDescription')}
      details={t('defaults.newAgent.pkgCacheDetails')}
      control={
        <Switch
          data-package-cache
          aria-label={t('defaults.newAgent.pkgCacheLabel')}
          disabled={disabled}
          checked={on}
          onCheckedChange={(packageCache) => save.mutate({ packageCache })}
        />
      }
    >
      <div className="grid max-w-md grid-cols-2 gap-3">
        <ResourceField
          id="package-cache-max"
          label={t('defaults.newAgent.holdAtMost')}
          placeholder="20GiB"
          hint={t('defaults.newAgent.holdHint')}
          value={max % 1024 ** 3 === 0 ? `${max / 1024 ** 3}GiB` : humanBytes(max).replace(' ', '')}
          disabled={disabled}
          onCommit={(value) => {
            if (value.trim() === '') return save.mutate({ packageCacheMaxBytes: 0 });
            const bytes = parseBytes(value);
            if (bytes === undefined) {
              toast.error(t('defaults.newAgent.sizeLike', { example: '20GiB' }));
              return;
            }
            save.mutate({ packageCacheMaxBytes: bytes });
          }}
        />
        <CacheHolds held={held} disabled={disabled} onEmpty={() => save.mutate({ clearPackageCache: true })} data-package-cache-size />
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
  const t = useT();
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
      label={t('defaults.newAgent.idleLabel')}
      description={t('defaults.newAgent.idleDescription')}
      details={t('defaults.newAgent.idleDetails')}
      control={
        <Switch
          data-auto-stop-idle
          aria-label={t('defaults.newAgent.idleLabel')}
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
            aria-label={t('defaults.newAgent.idleTime')}
            disabled={save.isPending || settings.data === undefined}
            value={String(idleTime)}
            onChange={(next) => save.mutate({ idleTimeSeconds: Number(next) })}
          >
            {idleTimes.map((n) => (
              <SelectOption key={n} value={String(n)}>
                {n === fallback ? t('defaults.newAgent.withDefault', { label: idleTimeWords(n) }) : idleTimeWords(n)}
              </SelectOption>
            ))}
            {!idleTimes.includes(idleTime) && <SelectOption value={String(idleTime)}>{idleTimeWords(idleTime)}</SelectOption>}
          </Select>
        </div>
      )}
    </SettingRow>
  );
}

// DockerPruneOnStop frees an agent's Docker space as it stops, however it's
// stopped (internal/daemon/dockerprune.go): its build cache and every image no
// container uses. Never its volumes, which hold the project's databases. On by
// default: a few stopped agents can otherwise hold tens of gigabytes of images
// nobody will run again.
export function DockerPruneOnStop() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (dockerPruneOnStop: boolean) => api.updateSettings({ dockerPruneOnStop }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.newAgent.pruneLabel')}
      description={t('defaults.newAgent.pruneDescription')}
      details={t('defaults.newAgent.pruneDetails')}
      control={
        <Switch
          data-docker-prune-on-stop
          aria-label={t('defaults.newAgent.pruneLabel')}
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.dockerPruneOnStop ?? true}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}

// AgentQueue turns the per-project running limit on for the installation:
// off, every agent starts right away, the way AgentBox always worked; on, a
// project's own slots (its Overview settings) and New agent's Queue switch
// take effect. Off by default, so nothing changes until you ask for it.
export function AgentQueue() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (agentQueue: boolean) => api.updateSettings({ agentQueue }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.newAgent.queueLabel')}
      description={t('defaults.newAgent.queueDescription')}
      control={
        <Switch
          data-agent-queue
          aria-label={t('defaults.newAgent.queueLabel')}
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.agentQueue ?? false}
          onCheckedChange={(agentQueue) => save.mutate(agentQueue)}
        />
      }
    />
  );
}

// TaskTarget is where a task of the Tasks tab goes when it starts: a new agent
// of its own, the way it always did, or the project's lead, which can split
// it across several agents. A task can choose for itself on its row; until it
// does, it goes where this says.
export function TaskTarget() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (taskTarget: string) => api.updateSettings({ taskTarget }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.newAgent.taskLabel')}
      description={t('defaults.newAgent.taskDescription')}
      control={
        <Select
          data-task-target
          aria-label={t('defaults.newAgent.taskLabel')}
          disabled={save.isPending || settings.data === undefined}
          placeholder={t('common.loading')}
          value={settings.data?.taskTarget ?? ''}
          onChange={(next) => save.mutate(next)}
        >
          <SelectOption value="agent">{t('defaults.newAgent.taskAgent')}</SelectOption>
          <SelectOption value="lead">{t('defaults.newAgent.taskLead')}</SelectOption>
        </Select>
      }
    />
  );
}

// leadRecheckFallback is the minutes it defaults to once you turn it on.
const leadRecheckFallback = 20;

// LeadRecheck wakes each project's chat every so often with a short status —
// agents queued, one sat idle — so it can act on it (retire what's finished,
// notice a queue that isn't moving) without you having to say anything.
// Off by default: it costs a turn only when there's something to act on, but
// it's still a turn nobody asked for until you turn it on.
export function LeadRecheck() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const on = settings.data?.leadRecheck ?? false;
  const minutes = settings.data?.leadRecheckMinutes ?? leadRecheckFallback;
  const [draft, setDraft] = useState(String(minutes));

  return (
    <SettingRow
      label={t('defaults.newAgent.recheckLabel')}
      description={t('defaults.newAgent.recheckDescription')}
      control={
        <Switch
          data-lead-recheck
          aria-label={t('defaults.newAgent.recheckLabel')}
          disabled={save.isPending || settings.data === undefined}
          checked={on}
          onCheckedChange={(leadRecheck) => save.mutate({ leadRecheck })}
        />
      }
    >
      {on && (
        <div className="flex max-w-52 items-center gap-2">
          <Input
            type="number"
            min={5}
            max={1440}
            aria-label={t('defaults.newAgent.recheckMinutes')}
            data-lead-recheck-minutes
            className="font-mono text-[13px]"
            disabled={save.isPending || settings.data === undefined}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => {
              const n = Math.min(1440, Math.max(5, Number(draft) || leadRecheckFallback));
              setDraft(String(n));
              if (n !== minutes) save.mutate({ leadRecheckMinutes: n });
            }}
          />
          <span className="text-[12.5px] text-subtle">{minutes === leadRecheckFallback ? t('defaults.newAgent.minutesDefault') : t('defaults.newAgent.minutes')}</span>
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
  const t = useT();
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
      label={t('defaults.newAgent.compactLabel')}
      description={t('defaults.newAgent.compactDescription')}
      details={t('defaults.newAgent.compactDetails')}
      control={
        <Select
          data-compact-window
          aria-label={t('defaults.newAgent.compactLabel')}
          disabled={save.isPending || settings.data === undefined}
          value={String(value)}
          onChange={(next) => save.mutate(Number(next))}
        >
          {compactWindows.map((n) => (
            <SelectOption key={n} value={String(n)}>
              {n === fallback ? t('defaults.newAgent.withDefault', { label: t('defaults.newAgent.tokens', { count: formatTokens(n) }) }) : t('defaults.newAgent.tokens', { count: formatTokens(n) })}
            </SelectOption>
          ))}
          {/* A value set from the command line keeps its place, so the control
              never shows another one as though you had picked it. */}
          {value !== 0 && !compactWindows.includes(value) && <SelectOption value={String(value)}>{t('defaults.newAgent.tokens', { count: formatTokens(value) })}</SelectOption>}
          <SelectOption value="0">{t('defaults.newAgent.wholeWindow')}</SelectOption>
        </Select>
      }
    />
  );
}

// mediaRetentions are how long a removed agent's media can be kept, in the
// order they are offered. The values are api.MediaRetention's.
const mediaRetentions = [
  { value: 'immediately', label: 'defaults.newAgent.retention.immediately' },
  { value: '1d', label: 'defaults.newAgent.retention.1d' },
  { value: '7d', label: 'defaults.newAgent.retention.7d' },
  { value: '30d', label: 'defaults.newAgent.retention.30d' },
  { value: 'forever', label: 'defaults.newAgent.retention.forever' },
] as const;

// MediaRetention is how long an agent's screenshots, recordings and reports
// outlive it. It belongs to the installation rather than to a project: it is
// about this machine's disk, and every project's removed agents fill it the
// same way — more so now that finished agents are removed on their own.
export function MediaRetention() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (mediaRetention: string) => api.updateSettings({ mediaRetention }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <SettingRow
      label={t('defaults.newAgent.mediaLabel')}
      description={t('defaults.newAgent.mediaDescription')}
      details={t('defaults.newAgent.mediaDetails')}
      control={
        <Select
          data-media-retention
          aria-label={t('defaults.newAgent.mediaLabel')}
          disabled={save.isPending || settings.data === undefined}
          value={settings.data?.mediaRetention ?? '1d'}
          onChange={(next) => save.mutate(next)}
        >
          {mediaRetentions.map((r) => (
            <SelectOption key={r.value} value={r.value}>
              {r.value === '1d' ? t('defaults.newAgent.withDefault', { label: t(r.label) }) : t(r.label)}
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
  const t = useT();
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
      label={t('defaults.newAgent.openCodeLabel')}
      description={t('defaults.newAgent.openCodeDescription')}
      details={t('defaults.newAgent.openCodeDetails')}
      control={
        <Switch
          data-image-opencode
          aria-label={t('defaults.newAgent.openCodeLabel')}
          disabled={build.isPending || job !== null || setup.data === undefined}
          checked={wanted}
          onCheckedChange={(next) => build.mutate(next)}
        />
      }
    >
      {wanted && !installed && job === null && (
        <SettingNote tone="warning">{t('defaults.newAgent.openCodeMissing')}</SettingNote>
      )}
      {job && <JobProgress jobId={job} onDone={() => void queryClient.invalidateQueries({ queryKey: ['setup'] })} />}
    </SettingRow>
  );
}
