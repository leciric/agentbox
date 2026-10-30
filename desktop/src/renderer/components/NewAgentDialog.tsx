import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, LoaderCircle, MessageSquare, Split, SquareTerminal } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { projectLabel } from '../lib/projectName';
import { branchSlug, branchSlugPattern, maxBranchSlugLen } from '../lib/branch';
import { formatTokens } from '../lib/chat';
import { openFanOut } from '../lib/groups';
import { projectAgentDefaults, startingPick } from '../lib/agentPick';
import { choiceName } from '../lib/modelChoices';
import { useT, type MessageKey } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { gitProfileWord, projectGitKey } from './GitProfiles';
import { JobProgress } from './JobProgress';
import { openSettingsSection } from './SettingsPage';
import { AIIcon, aiLabel } from './state';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Label } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { Switch } from './ui/switch';

interface Form {
  project: string;
  title: string;
  name: string;
  // The branch's slug, after the project's prefix. Made from the title until
  // you edit it; after that it is yours.
  branch: string;
  branchTouched: boolean;
  ai: string;
  iface: 'chat' | 'cli'; // how you use the AI tool
  autonomous: boolean;
  // "" is the model and effort new agents start on, not a chosen empty value:
  // the request leaves the field out entirely, so the fallback applies.
  model: string;
  effort: string;
  // Where its chat compacts, in tokens (D91); "" is the installation's compact
  // window. Only offered when the model has more than one.
  contextWindow: string;
  from: string;
  clean: boolean;
  claudeAccount: string; // "" is the project's account
  githubAccount: string; // "" is the project's account
  // The Kubernetes cluster its pod runs in, by name, or "auto" to let the
  // daemon place it where there is room; "" is an Incus machine on this
  // computer, which is what every installation did before clusters.
  cluster: string;
  // Whether the cluster was picked here: until it is, it follows the project's.
  clusterTouched: boolean;
  // Queue it when the cluster it would run in is full, rather than failing
  // the create: it is accepted now and started when there is room.
  queue: boolean;
  // What this agent does to the project's chat when it finishes: "" follows
  // the project's own setting, "chat" wakes it and "off" only records the
  // finish. Only matters when the project's setting is "lead".
  notify: '' | 'chat' | 'off';
}

const emptyForm: Form = {
  project: '',
  title: '',
  name: '',
  branch: '',
  branchTouched: false,
  ai: 'claude',
  iface: 'chat',
  autonomous: true,
  model: '',
  effort: '',
  contextWindow: '',
  from: '',
  clean: false,
  claudeAccount: '',
  githubAccount: '',
  cluster: '',
  clusterTouched: false,
  queue: false,
  notify: '',
};

// A label or hint that is a brand's name is shown as it is; the rest are keys.
const tools: { ai: string; label?: string; labelKey?: MessageKey; hint?: string; hintKey?: MessageKey }[] = [
  { ai: 'claude', label: 'Claude Code', hint: 'Anthropic' },
  { ai: 'codex', label: 'Codex', hint: 'OpenAI' },
  { ai: 'opencode', label: 'OpenCode', hintKey: 'project.newAgent.tool.openSource' },
  { ai: 'cursor', label: 'Cursor', hintKey: 'project.newAgent.tool.cursor' },
  { ai: 'none', labelKey: 'project.newAgent.tool.shellOnly', hintKey: 'project.newAgent.tool.noAI' },
];

// The form as "Open Git settings" left it, so opening the dialog again on the
// same project comes back to the title and prompt rather than an empty form.
let keptForm: Form | null = null;

const interfaces = [
  { value: 'chat', label: 'project.newAgent.interface.chat', icon: MessageSquare },
  { value: 'cli', label: 'project.newAgent.interface.terminal', icon: SquareTerminal },
] as const;

// NewAgentDialog creates an agent and follows the create job. project is the
// preselected project ("" for none); null closes the dialog.
export function NewAgentDialog({
  project,
  onClose,
  onCreated,
}: {
  project: string | null;
  onClose: () => void;
  onCreated: (ref: string) => void;
}) {
  const t = useT();
  const open = project !== null;
  const queryClient = useQueryClient();
  const [form, setForm] = useState<Form>(emptyForm);
  const [advanced, setAdvanced] = useState(false);
  const [job, setJob] = useState<T.Job | null>(null);
  const [finished, setFinished] = useState<T.Job | null>(null);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: open });
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth, enabled: open });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, enabled: open });
  const image = useQuery({ queryKey: ['image'], queryFn: api.image, enabled: open });
  // The base image's installed tools, for the one tool that is optional in it;
  // and where this installation runs agents, which decides whether "this
  // machine" is a place an agent can be created.
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, enabled: open });
  // Exactly ["cluster"]: a daemon with no Incus, where nothing can run but a
  // pod. Both backends keep the machine as an option.
  const backends = setup.data?.backends ?? [];
  const clusterOnly = backends.length === 1 && backends[0] === 'cluster';
  const base = useQuery({ queryKey: ['base', form.project], queryFn: () => api.base(form.project), enabled: open && form.project !== '' });
  // Where a project's cluster agents clone from. Read only once a cluster is
  // chosen, so the Incus path asks for nothing new.
  const clusters = useQuery({ queryKey: ['clusters'], queryFn: api.clusters, enabled: open });
  const git = useQuery({
    queryKey: projectGitKey(form.project),
    queryFn: () => api.projectGit(form.project),
    enabled: open && form.project !== '' && form.cluster !== '',
  });
  const selected = projects.data?.find((p) => p.name === form.project);
  const allowedAccounts = selected?.claudeAccounts ?? [];
  const accounts = (auth.data?.claudeAccounts ?? []).filter((a) => allowedAccounts.length === 0 || allowedAccounts.includes(a.name));
  const defaultAccount = accounts.find((a) => a.default)?.name ?? 'none';
  const githubAccounts = auth.data?.githubAccounts ?? [];
  const defaultGitHubAccount = githubAccounts.find((a) => a.default)?.name ?? 'none';
  // The Claude Code menus, exactly as an adapter advertised them for this
  // account; AgentBox composes neither, so before any chat has run there is
  // only the fallback to offer.
  const modelChoices = settings.data?.claudeModelChoices ?? [];
  const effortChoices = settings.data?.claudeEffortChoices ?? [];
  // OpenCode's models are its own provider/model ids, remembered the same way:
  // from a chat that has run, or from `opencode models` against AgentBox's own
  // login. Its effort levels are remembered too, from the sessions that
  // advertised the option, so the picker exists only once there is a real list.
  const openCodeModels = settings.data?.openCodeModelChoices ?? [];
  const openCodeEfforts = settings.data?.openCodeEffortChoices ?? [];
  const projectEffort = selected?.agentEffort ?? '';
  const fallbackOpenCodeEffort = projectEffort || settingLabel(openCodeEfforts, settings.data?.defaultOpenCodeEffort ?? '', 'medium');
  // Cursor's own model ids, and the effort levels each one takes (most take
  // none). With no model picked, the one Settings names decides the levels.
  const cursorModels = settings.data?.cursorModelChoices ?? [];
  // What the project names for agents on the tool picked, which the daemon
  // applies to whatever is left on its default (Project.agentTool).
  const projectDefaults = selected ? projectAgentDefaults(selected, form.ai) : { model: '', effort: '', window: '' };
  const cursorModel = form.model || projectDefaults.model || settings.data?.defaultCursorModel || '';
  const cursorEfforts = cursorModels.find((c) => c.value === cursorModel)?.efforts ?? [];
  const cursorEffort = cursorEfforts.includes(form.effort) ? form.effort : '';
  // Cursor has no command-line interface to work with: only its chat.
  const iface = form.ai === 'cursor' ? 'chat' : form.iface;
  const fallbackModel = settingLabel(modelChoices, projectDefaults.model || settings.data?.defaultClaudeModel || '', 'opus');
  // The windows the model it will run on has: the one picked, else the
  // project's, else the installation's default. "Default" is the window new
  // agents start with in Settings when that model has it, else the first, the
  // installation's compact window — what the daemon gives an agent nobody
  // chose a window for (claudeChatDefaults).
  const runsOn = form.model || projectDefaults.model || settings.data?.defaultClaudeModel || 'opus';
  const contextWindows = settings.data?.claudeContextWindows?.[runsOn] ?? [];
  const contextWindow = contextWindows.includes(Number(form.contextWindow)) ? form.contextWindow : '';
  const settingsWindow = Number(projectDefaults.window || settings.data?.defaultAgentContextWindow || 0);
  const defaultWindow = contextWindows.includes(settingsWindow) ? settingsWindow : contextWindows[0];
  const fallbackEffort = settingLabel(effortChoices, projectDefaults.effort || settings.data?.defaultClaudeEffort || '', 'medium');

  const create = useMutation({
    mutationFn: () =>
      api.createAgent({
        project: form.project,
        title: form.title.trim() || undefined,
        name: form.name.trim() || undefined,
        branch: form.branch.trim() || undefined,
        ai: form.ai,
        interface: form.ai === 'none' ? undefined : iface,
        // Sent either way: false is a choice, and leaving it out would mean
        // "whatever new agents do", which isn't what the switch was set to.
        autonomous: form.autonomous,
        // Left out when nothing was picked, so the model and effort new agents
        // start on apply. "" would be a choice of no model at all, and is refused.
        model: form.ai === 'claude' || form.ai === 'opencode' || form.ai === 'cursor' ? form.model || undefined : undefined,
        effort:
          form.ai === 'claude'
            ? form.effort || undefined
            : form.ai === 'opencode'
              ? form.effort || undefined
              : form.ai === 'cursor'
                ? cursorEffort || undefined
                : undefined,
        contextWindow: form.ai === 'claude' ? contextWindow || undefined : undefined,
        from: form.from.trim() || undefined,
        clean: form.clean || undefined,
        claudeAccount: form.ai === 'claude' ? form.claudeAccount || undefined : undefined,
        githubAccount: form.githubAccount || undefined,
        // "local" and not an absent field: a project that runs its agents on a
        // cluster would otherwise take an explicit "this machine" as its own
        // choice. A cluster-only daemon has no machine to name, and it places
        // by itself until the picker has filled in.
        cluster: form.cluster || (clusterOnly ? 'auto' : 'local'),
        queue: form.queue || undefined,
        finishNotice: form.notify || undefined,
      }),
    onSuccess: (started) => {
      setJob(started);
      // The next agent starts where this one did, in a project that names
      // no tool of its own (startingPick).
      const model = form.ai === 'none' || form.ai === 'codex' ? '' : form.model;
      const effort = form.ai === 'claude' ? form.effort : form.ai === 'cursor' ? cursorEffort : '';
      void api.updateSettings({ lastAgentPick: { ai: form.ai, model, effort } }).then((updated) => queryClient.setQueryData(['settings'], updated), () => undefined);
    },
  });
  const cancel = useMutation({ mutationFn: (id: string) => api.cancelJob(id) });

  const startedFor = useRef('');
  useEffect(() => {
    if (!open) return;
    startedFor.current = '';
    const kept = keptForm && (!project || keptForm.project === project) ? keptForm : null;
    keptForm = null;
    setForm(kept ?? { ...emptyForm, project: project ?? '' });
    setAdvanced(false);
    setJob(null);
    setFinished(null);
    create.reset();
    cancel.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, project]);

  // The tool, model and effort the form starts on: the project's tool, else
  // the last pick, else Claude Code. Applied once for each project the form is
  // on, as soon as both are known, so a pick made afterwards is kept. After the
  // reset above, so that it starts over each time the dialog opens.
  const lastPick = settings.data?.lastAgentPick;
  useEffect(() => {
    if (!open || !selected || !settings.isFetched || startedFor.current === selected.name) return;
    startedFor.current = selected.name;
    setForm((f) => ({ ...f, ...startingPick(selected, lastPick) }));
  }, [open, selected, settings.isFetched, lastPick]);

  const firstProject = projects.data?.[0]?.name;
  useEffect(() => {
    if (open && form.project === '' && firstProject) setForm((f) => ({ ...f, project: firstProject }));
  }, [open, form.project, firstProject]);

  // Where this project's agents run unless one is given a cluster of its own:
  // the picker starts on that, and moving to another project moves it too. A
  // cluster-only installation has no machine to start on, so a project that
  // names none starts on the daemon's placement instead.
  const projectCluster = projects.data?.find((p) => p.name === form.project)?.cluster ?? '';
  const startCluster = clusterOnly && projectCluster === '' ? 'auto' : projectCluster;
  useEffect(() => {
    if (!open) return;
    setForm((f) => (f.clusterTouched ? f : { ...f, cluster: startCluster }));
  }, [open, form.project, startCluster]);

  // The picker is on screen whenever where the agent runs is a choice, or the
  // project has made it: while the list loads, when it fails, and when the
  // project names a cluster the list doesn't have. What it can't show is never
  // sent: the create waits, or is refused here with the reason (H1). A
  // draining cluster takes no new agents, so it is listed and not offered (H2).
  const clusterList = clusters.data ?? [];
  const showClusters = clusterOnly || projectCluster !== '' || clusterList.length > 0 || clusters.isError;
  const placeable = new Set(['auto', ...clusterList.filter((c) => !c.draining).map((c) => c.name)]);
  if (!clusterOnly) placeable.add('');
  const draining = clusterList.find((c) => c.name === form.cluster)?.draining === true;
  const clusterProblem = !showClusters
    ? ''
    : clusters.isPending
      ? t('project.newAgent.clustersLoading')
      : placeable.has(form.cluster)
        ? ''
        : clusters.isError
          ? t('project.newAgent.clusterUnchecked', { name: form.cluster })
          : draining
            ? t('project.newAgent.clusterDrainingWhy', { name: form.cluster })
            : t('project.newAgent.clusterUnknown', { name: form.cluster });
  // Until the project is read, where it runs its agents isn't known either.
  const clusterBlocked = clusterProblem !== '' || !selected;

  const onDone = useCallback(
    async (done: T.Job) => {
      setFinished(done);
      if (done.status !== 'succeeded') return;
      const agent = done.result as T.Agent;
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
      onClose();
      onCreated(agent.ref);
    },
    [onClose, onCreated, queryClient],
  );

  const set = <K extends keyof Form>(key: K, value: Form[K]) => setForm((f) => ({ ...f, [key]: value }));
  const needsLogin =
    (form.ai === 'claude' && auth.data?.claude === false) ||
    (form.ai === 'codex' && auth.data?.codex === false) ||
    (form.ai === 'opencode' && auth.data?.opencode === false) ||
    (form.ai === 'cursor' && auth.data?.cursor === false);
  // An AI tool that is optional in the base image is no use until the image
  // has it: saying so here is the same answer the daemon would give, before
  // the form is filled in rather than after it is sent.
  const missingFromImage = form.ai === 'opencode' && setup.data?.image.installed.opencode === false;

  // A pod clones its project's git profile, so a profile that can't clone or
  // push is refused here, before the request: the daemon refuses it too, with
  // the command that fixes it (agent.Create, gitprofile.go). The profile the
  // project names is the one a pod gets. The daemon falls back to the other
  // when the named one was removed, and this does not: an agent quietly
  // cloning from the public remote where the project asked for the private one
  // is worse than refusing the create.
  const wanted = git.data?.agentRemote || 'private';
  const agentProfile = (git.data?.profiles ?? []).find((p) => p.profile === wanted);
  const agentProfileName = gitProfileWord(wanted);
  const gitProblem =
    form.cluster === ''
      ? ''
      : git.isError
        ? t('project.newAgent.gitCheckFailed', { error: errorMessage(git.error) })
        : !git.data
          ? ''
          : !agentProfile
        ? t('project.newAgent.gitNoProfile', { profile: agentProfileName })
        : agentProfile.resolveError
          ? t('project.newAgent.gitNoResolve', { profile: agentProfileName, error: agentProfile.resolveError })
          : agentProfile.url.trim() === ''
            ? t('project.newAgent.gitNoURL', { profile: agentProfileName })
            : !agentProfile.hasCredential
              ? t('project.newAgent.gitNoLogin', { profile: agentProfileName, credential: agentProfile.credential || agentProfile.profile })
              : '';
  // The answer is on its way: a submit now would be the request the daemon
  // refuses, so the button waits rather than flashing an error.
  const gitChecking = form.cluster !== '' && form.project !== '' && git.isPending;
  const gitBlocked = gitProblem !== '' || gitChecking;
  // What a disabled Create is waiting for, said beside it rather than left
  // for the reader to guess.
  const waiting = gitChecking ? t('project.newAgent.gitChecking') : '';

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{job ? (form.title.trim() ? t('project.newAgent.creatingTitled', { title: form.title.trim() }) : t('project.newAgent.creating')) : t('project.newAgent.title')}</DialogTitle>
          <DialogDescription>
            {job ? t('project.newAgent.keepsRunning') : t('project.newAgent.description')}
          </DialogDescription>
        </DialogHeader>

        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={(done) => void onDone(done)} />
            {cancel.error && <Notice>{errorMessage(cancel.error)}</Notice>}
            <DialogFooter>
              {finished ? (
                <Button onClick={onClose}>{t('common.close')}</Button>
              ) : (
                <>
                  <Button variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate(job.id)}>
                    {cancel.isPending && <LoaderCircle className="animate-spin" />}
                    {t('project.newAgent.cancelRollback')}
                  </Button>
                  <Button onClick={onClose}>{t('project.newAgent.keepInBackground')}</Button>
                </>
              )}
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-5"
            onSubmit={(event) => {
              event.preventDefault();
              if (!form.project || gitBlocked || clusterBlocked) return;
              create.mutate();
            }}
          >
            <Field label={t('project.newAgent.workLabel')} htmlFor="agent-title" hint={t('project.newAgent.workHint')}>
              <Input
                id="agent-title"
                autoFocus
                maxLength={80}
                placeholder={t('project.newAgent.workPlaceholder')}
                value={form.title}
                onChange={(event) => {
                  const title = event.target.value;
                  setForm((f) => ({ ...f, title, branch: f.branchTouched ? f.branch : branchSlug(title) }));
                }}
              />
            </Field>

            <Field
              label={t('project.newAgent.branchLabel')}
              htmlFor="agent-branch"
              hint={t('project.newAgent.branchHint')}
            >
              <div className="flex min-w-0 items-center gap-1.5">
                {selected?.branchPrefix && <span className="shrink-0 font-mono text-ui text-subtle">{selected.branchPrefix}</span>}
                <Input
                  id="agent-branch"
                  className="min-w-0 font-mono text-ui"
                  maxLength={maxBranchSlugLen}
                  pattern={branchSlugPattern}
                  title={t('project.newAgent.branchPattern')}
                  placeholder="agent-NN"
                  value={form.branch}
                  onChange={(event) => setForm((f) => ({ ...f, branch: event.target.value, branchTouched: true }))}
                />
              </div>
            </Field>

            <Field label={t('project.newAgent.projectLabel')} htmlFor="agent-project">
              <Select id="agent-project" value={form.project} onChange={(value) => setForm((f) => ({ ...f, project: value, clusterTouched: false }))}>
                {projects.data?.map((p) => (
                  <SelectOption key={p.name} value={p.name}>
                    {projectLabel(p)}
                  </SelectOption>
                ))}
              </Select>
            </Field>

            {showClusters && (
              <Field label={t('project.newAgent.clusterLabel')} htmlFor="agent-cluster" hint={t('project.newAgent.clusterHint')}>
                <Select
                  id="agent-cluster"
                  data-new-agent-cluster
                  value={form.cluster}
                  disabled={clusters.isPending}
                  placeholder={clusters.isPending ? t('project.newAgent.clustersLoadingShort') : form.cluster}
                  onChange={(value) => setForm((f) => ({ ...f, cluster: value, clusterTouched: true }))}
                >
                  {/* A cluster-only daemon has no Incus, so "this machine" is
                      not a place an agent can be created. */}
                  {!clusterOnly && <SelectOption value="">{t('project.newAgent.clusterLocal')}</SelectOption>}
                  <SelectOption value="auto">{t('project.newAgent.clusterAuto')}</SelectOption>
                  {clusters.data?.map((cluster) => (
                    <SelectOption key={cluster.name} value={cluster.name} disabled={cluster.draining}>
                      {cluster.draining
                        ? t('project.newAgent.clusterDraining', { name: cluster.name })
                        : cluster.capacity
                          ? t('project.newAgent.clusterOption', { name: cluster.name, agents: cluster.capacity.agents })
                          : cluster.name}
                    </SelectOption>
                  ))}
                </Select>
                {clusters.isError && (
                  <Notice tone="warning">
                    <div className="grid justify-items-start gap-2">
                      <p>{t('project.newAgent.clustersFailed', { error: errorMessage(clusters.error) })}</p>
                      <Button size="sm" variant="secondary" disabled={clusters.isFetching} onClick={() => void clusters.refetch()}>
                        {clusters.isFetching && <LoaderCircle className="animate-spin" />}
                        {t('project.newAgent.clustersRetry')}
                      </Button>
                    </div>
                  </Notice>
                )}
                {clusterProblem !== '' && (
                  <p className={cn('flex items-center gap-1.5 text-xs', clusters.isPending ? 'text-subtle' : 'text-amber-300')} role="status" data-new-agent-cluster-problem>
                    {clusters.isPending && <LoaderCircle className="size-3.5 shrink-0 animate-spin" />}
                    {clusterProblem}
                  </p>
                )}
                {form.cluster !== '' && placeable.has(form.cluster) && (
                  <SwitchRow
                    id="agent-queue"
                    label={t('project.newAgent.queueIfFull')}
                    hint={t('project.newAgent.queueIfFullHint')}
                    checked={form.queue}
                    onChange={(value) => set('queue', value)}
                  />
                )}
              </Field>
            )}

            <div className="grid gap-1.5">
              <Label>{t('project.newAgent.aiTool')}</Label>
              <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label={t('project.newAgent.aiTool')}>
                {tools.map((tool) => (
                  <button
                    key={tool.ai}
                    type="button"
                    role="radio"
                    aria-checked={form.ai === tool.ai}
                    data-ai={tool.ai}
                    onClick={() => set('ai', tool.ai)}
                    className={cn(
                      'flex items-center gap-2.5 rounded-xl border border-line bg-surface-faint px-3 py-2.5 text-left transition hover:border-line-vivid hover:bg-surface',
                      form.ai === tool.ai && 'border-brand-400/50 bg-brand-500/10 shadow-[0_0_0_3px_rgb(139_92_246/0.12)]',
                    )}
                  >
                    <AIIcon ai={tool.ai} className={cn('size-4 text-subtle', form.ai === tool.ai && 'text-brand-300')} />
                    <span className="grid">
                      <span className="text-ui font-medium text-primary">{tool.labelKey ? t(tool.labelKey) : tool.label}</span>
                      <span className="text-caption text-subtle">{tool.hintKey ? t(tool.hintKey) : tool.hint}</span>
                    </span>
                  </button>
                ))}
              </div>
            </div>

            {form.ai !== 'none' && (
              <div className="grid gap-1.5">
                <Label>{t('project.newAgent.workWith')}</Label>
                <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label={t('project.newAgent.interfaceAria')}>
                  {interfaces.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      role="radio"
                      aria-checked={iface === option.value}
                      disabled={form.ai === 'cursor' && option.value === 'cli'}
                      data-interface={option.value}
                      onClick={() => form.ai !== 'cursor' && set('iface', option.value)}
                      className={cn(
                        'flex items-center gap-2.5 rounded-xl border border-line bg-surface-faint px-3 py-2.5 text-left transition hover:border-line-vivid hover:bg-surface',
                        iface === option.value && 'border-brand-400/50 bg-brand-500/10 shadow-[0_0_0_3px_rgb(139_92_246/0.12)]',
                        form.ai === 'cursor' && option.value === 'cli' && 'cursor-not-allowed opacity-50 hover:border-line hover:bg-surface-faint',
                      )}
                    >
                      <option.icon className={cn('size-4 shrink-0 text-subtle', iface === option.value && 'text-brand-300')} />
                      <span className="grid">
                        <span className="text-ui font-medium text-primary">{t(option.label)}</span>
                        <span className="text-caption text-subtle">
                          {option.value === 'chat'
                            ? t('project.newAgent.chatHint')
                            : form.ai === 'cursor'
                              ? t('project.newAgent.cursorNoCli')
                            : t('project.newAgent.cliHint', { tool: aiLabel(form.ai) })}
                        </span>
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            )}

            <button
              type="button"
              className="-mt-1 flex items-center gap-1.5 justify-self-start text-ui text-muted hover:text-primary"
              aria-expanded={advanced}
              onClick={() => setAdvanced((v) => !v)}
            >
              <ChevronRight className={cn('size-3.5 transition-transform', advanced && 'rotate-90')} />
              {t('project.newAgent.moreOptions')}
            </button>
            {advanced && (
              <div className="-mt-2 grid animate-slide-up gap-4 rounded-xl border border-line bg-surface-faint p-4">
                <div className="grid grid-cols-2 gap-4">
                  <Field label={t('project.newAgent.nameLabel')} htmlFor="agent-name" hint={t('project.newAgent.nameHint')}>
                    <Input id="agent-name" className="font-mono text-ui" placeholder="agent-NN" value={form.name} onChange={(event) => set('name', event.target.value)} />
                  </Field>
                  <Field label={t('project.newAgent.fromLabel')} htmlFor="agent-from" hint={selected ? t('project.newAgent.fromHint', { branch: selected.branch }) : undefined}>
                    <Input id="agent-from" className="font-mono text-ui" placeholder={selected?.branch ?? 'main'} value={form.from} onChange={(event) => set('from', event.target.value)} />
                  </Field>
                </div>
                {form.ai === 'opencode' && (
                  <div className={cn('grid gap-4', openCodeEfforts.length > 0 && 'grid-cols-2')}>
                    <Field
                      label={t('project.newAgent.modelLabel')}
                      htmlFor="agent-opencode-model"
                      hint={openCodeModels.length ? t('project.newAgent.openCodeModelHint') : t('project.newAgent.openCodeLoginHint')}
                    >
                      <Select id="agent-opencode-model" value={form.model} onChange={(value) => set('model', value)}>
                        <SelectOption value="">
                          {projectDefaults.model ? t('project.newAgent.defaultOption', { value: settingLabel(openCodeModels, projectDefaults.model, '') }) : t('project.newAgent.openCodeDefault')}
                        </SelectOption>
                        {openCodeModels.map((choice) => (
                          <SelectOption key={choice.value} value={choice.value}>
                            {choiceName(choice) || choice.value}
                          </SelectOption>
                        ))}
                      </Select>
                    </Field>
                    {openCodeEfforts.length > 0 && (
                      <Field label={t('project.newAgent.effortLabel')} htmlFor="agent-opencode-effort" hint={t('project.newAgent.effortHint')}>
                        <Select id="agent-opencode-effort" value={form.effort} onChange={(value) => set('effort', value)}>
                          <SelectOption value="">{t('project.newAgent.defaultOption', { value: fallbackOpenCodeEffort })}</SelectOption>
                          {openCodeEfforts.map((choice) => (
                            <SelectOption key={choice.value} value={choice.value}>
                              {choiceName(choice) || choice.value}
                            </SelectOption>
                          ))}
                        </Select>
                      </Field>
                    )}
                  </div>
                )}
                {form.ai === 'cursor' && (
                  <div className="grid grid-cols-2 gap-4">
                    <Field label={t('project.newAgent.modelLabel')} htmlFor="agent-cursor-model" hint={t('project.newAgent.cursorModelHint')}>
                      <Select id="agent-cursor-model" value={form.model} onChange={(value) => setForm((f) => ({ ...f, model: value, effort: '' }))}>
                        <SelectOption value="">
                          {projectDefaults.model ? t('project.newAgent.defaultOption', { value: settingLabel(cursorModels, projectDefaults.model, '') }) : t('project.newAgent.cursorDefault')}
                        </SelectOption>
                        {cursorModels.map((choice) => (
                          <SelectOption key={choice.value} value={choice.value}>
                            {choiceName(choice) || choice.value}
                          </SelectOption>
                        ))}
                      </Select>
                    </Field>
                    {cursorEfforts.length > 0 && (
                      <Field label={t('project.newAgent.effortLabel')} htmlFor="agent-cursor-effort" hint={t('project.newAgent.effortHint')}>
                        <Select id="agent-cursor-effort" value={cursorEffort} onChange={(value) => set('effort', value)}>
                          <SelectOption value="">{t('project.newAgent.cursorEffortDefault')}</SelectOption>
                          {cursorEfforts.map((level) => (
                            <SelectOption key={level} value={level}>
                              {level}
                            </SelectOption>
                          ))}
                        </Select>
                      </Field>
                    )}
                  </div>
                )}
                {form.ai === 'claude' && (
                  <div className="grid grid-cols-2 gap-4">
                    <Field
                      label={t('project.newAgent.modelLabel')}
                      htmlFor="agent-model"
                      hint={t('project.newAgent.claudeModelHint')}
                    >
                      <Select id="agent-model" value={form.model} onChange={(value) => set('model', value)}>
                        <SelectOption value="">{t('project.newAgent.defaultOption', { value: fallbackModel })}</SelectOption>
                        {modelChoices.map((choice) => (
                          <SelectOption key={choice.value} value={choice.value}>
                            {choiceName(choice) || choice.value}
                          </SelectOption>
                        ))}
                      </Select>
                    </Field>
                    <Field label={t('project.newAgent.effortLabel')} htmlFor="agent-effort" hint={t('project.newAgent.effortHint')}>
                      <Select id="agent-effort" value={form.effort} onChange={(value) => set('effort', value)}>
                        <SelectOption value="">{t('project.newAgent.defaultOption', { value: fallbackEffort })}</SelectOption>
                        {effortChoices.map((choice) => (
                          <SelectOption key={choice.value} value={choice.value}>
                            {choiceName(choice) || choice.value}
                          </SelectOption>
                        ))}
                      </Select>
                    </Field>
                  </div>
                )}
                {form.ai === 'claude' && contextWindows.length > 1 && (
                  <Field
                    label={t('project.newAgent.windowLabel')}
                    htmlFor="agent-context-window"
                    hint={t('project.newAgent.windowHint')}
                  >
                    <Select id="agent-context-window" value={contextWindow} onChange={(value) => set('contextWindow', value)}>
                      <SelectOption value="">{t('project.newAgent.defaultOption', { value: formatTokens(defaultWindow) })}</SelectOption>
                      {contextWindows.slice(1).map((n) => (
                        <SelectOption key={n} value={String(n)}>
                          {t('project.newAgent.windowWhole', { size: formatTokens(n) })}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                )}
                {form.ai === 'claude' && accounts.length > 1 && (
                  <Field
                    label={t('project.newAgent.claudeAccountLabel')}
                    htmlFor="agent-claude-account"
                    hint={
                      selected?.claudeAccount
                        ? t('project.newAgent.accountUses', { project: selected.name, account: selected.claudeAccount })
                        : t('project.newAgent.accountDefault', { account: defaultAccount })
                    }
                  >
                    <Select id="agent-claude-account" value={form.claudeAccount} onChange={(value) => set('claudeAccount', value)}>
                      <SelectOption value="">{selected?.claudeAccount ? t('project.newAgent.projectsAccount', { account: selected.claudeAccount }) : t('project.newAgent.defaultOption', { value: defaultAccount })}</SelectOption>
                      {accounts.map((account) => (
                        <SelectOption key={account.name} value={account.name}>
                          {account.name}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                )}
                {githubAccounts.length > 1 && (
                  <Field
                    label={t('project.newAgent.githubAccountLabel')}
                    htmlFor="agent-github-account"
                    hint={
                      selected?.githubAccount
                        ? t('project.newAgent.accountUses', { project: selected.name, account: selected.githubAccount })
                        : t('project.newAgent.accountDefault', { account: defaultGitHubAccount })
                    }
                  >
                    <Select id="agent-github-account" value={form.githubAccount} onChange={(value) => set('githubAccount', value)}>
                      <SelectOption value="">{selected?.githubAccount ? t('project.newAgent.projectsAccount', { account: selected.githubAccount }) : t('project.newAgent.defaultOption', { value: defaultGitHubAccount })}</SelectOption>
                      {githubAccounts.map((account) => (
                        <SelectOption key={account.name} value={account.name}>
                          {account.name}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                )}
                <Field
                  label={t('project.newAgent.finishLabel')}
                  htmlFor="agent-notify"
                  hint={t('project.newAgent.finishHint')}
                >
                  <Select id="agent-notify" value={form.notify} onChange={(value) => set('notify', value as Form['notify'])}>
                    <SelectOption value="">{t('project.newAgent.finishProject')}</SelectOption>
                    <SelectOption value="chat">{t('project.newAgent.finishWake')}</SelectOption>
                    <SelectOption value="off">{t('project.newAgent.finishRecord')}</SelectOption>
                  </Select>
                </Field>
                <SwitchRow
                  id="agent-autonomous"
                  label={t('project.newAgent.autonomousLabel')}
                  hint={t('project.newAgent.autonomousHint')}
                  checked={form.autonomous}
                  disabled={form.ai === 'none'}
                  onChange={(value) => set('autonomous', value)}
                />
                {base.data && (
                  <SwitchRow
                    id="agent-clean"
                    label={t('project.newAgent.cleanLabel')}
                    hint={t('project.newAgent.cleanHint', { from: base.data.savedFrom })}
                    checked={form.clean}
                    onChange={(value) => set('clean', value)}
                  />
                )}
              </div>
            )}

            {needsLogin && (
              <Notice tone="warning">
                {t.rich('project.newAgent.needsLogin', { tool: aiLabel(form.ai), code: (c) => <Code>{c}</Code>, command: `agentbox auth ${form.ai}` })}
              </Notice>
            )}
            {missingFromImage && (
              <Notice tone="warning">
                {t.rich('project.newAgent.noOpenCode', { code: (c) => <Code>{c}</Code> })}
              </Notice>
            )}
            {gitProblem !== '' && (
              <Notice tone="warning">
                <div className="grid justify-items-start gap-2">
                  <p>{gitProblem}</p>
                  <Button
                    size="sm"
                    variant="secondary"
                    data-new-agent-open-git
                    onClick={() => {
                      // The form is kept for the next time the dialog opens
                      // on this project, and Settings opens at its Git group.
                      keptForm = form;
                      onClose();
                      openSettingsSection(`project:${form.project}`, 'git-flow');
                    }}
                  >
                    {t('project.newAgent.openGitSettings')}
                  </Button>
                </div>
              </Notice>
            )}
            {image.data?.ready === false && (
              <Notice tone="info">
                {image.data.build === 'building' ? t('project.newAgent.imageBuilding') : t('project.newAgent.noImage')}
              </Notice>
            )}
            {create.error && <Notice>{errorMessage(create.error)}</Notice>}
            {waiting && (
              <p className="-mb-2 flex items-center justify-end gap-1.5 text-xs text-subtle" role="status" data-new-agent-waiting>
                <LoaderCircle className="size-3.5 animate-spin" />
                {waiting}
              </p>
            )}

            <DialogFooter>
              {/* The same work on several agents at once, to compare. */}
              <Button
                variant="ghost"
                className="mr-auto px-2 text-brand-200"
                disabled={!form.project || gitBlocked}
                onClick={() => {
                  const seed = { project: form.project, title: form.title.trim() };
                  onClose();
                  openFanOut(seed);
                }}
                data-new-agent-fan-out
              >
                <Split />
                {t('group.newAgent.compare')}
              </Button>
              <Button variant="ghost" onClick={onClose}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={!form.project || create.isPending || gitBlocked || clusterBlocked}>
                {create.isPending && <LoaderCircle className="animate-spin" />}
                {t('project.newAgent.create')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// settingLabel names what an unchosen model or effort falls back to: the one
// set for new agents on the overview, or AgentBox's own default when none is
// set. A value the account no longer offers is still named, rather than shown
// as though nothing were set.
function settingLabel(choices: T.ChatOptionChoice[], value: string, own: string): string {
  if (!value) return own;
  const choice = choices.find((c) => c.value === value);
  return (choice && choiceName(choice)) || value;
}

function SwitchRow({
  id,
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <div className="flex items-start gap-3">
      <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} className="mt-0.5" />
      <div className="grid gap-0.5">
        <Label htmlFor={id}>{label}</Label>
        <p className="text-xs text-subtle">{hint}</p>
      </div>
    </div>
  );
}
