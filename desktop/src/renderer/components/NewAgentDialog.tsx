import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, LoaderCircle, MessageSquare, SquareTerminal } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { branchSlug, branchSlugPattern, maxBranchSlugLen } from '../lib/branch';
import { formatTokens } from '../lib/chat';
import { choiceName } from '../lib/modelChoices';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { JobProgress } from './JobProgress';
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
  // What this agent does to the project's chat when it finishes: "" follows
  // the project's own setting, "chat" wakes it and "off" only records the
  // finish. Only matters when the project's setting is "lead".
  notify: '' | 'chat' | 'off';
  // What this agent's machine may take. "" is not "no limit" here either: an
  // untouched box leaves the field out of the request, so what new agents get
  // applies. Clearing a box that was prefilled is how you ask for no limit,
  // which is why touched is tracked rather than compared against "".
  cpu: string;
  cpuAllowance: string;
  memory: string;
  resourcesTouched: boolean;
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
  notify: '',
  cpu: '',
  cpuAllowance: '',
  memory: '',
  resourcesTouched: false,
};

const tools = [
  { ai: 'claude', label: 'Claude Code', hint: 'Anthropic' },
  { ai: 'codex', label: 'Codex', hint: 'OpenAI' },
  { ai: 'opencode', label: 'OpenCode', hint: 'Open source' },
  { ai: 'none', label: 'Shell only', hint: 'No AI tool' },
];

const interfaces = [
  { value: 'chat', label: 'Chat', icon: MessageSquare },
  { value: 'cli', label: 'Terminal', icon: SquareTerminal },
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
  // Only for the tools that are optional in the base image: an image built
  // without OpenCode can't run an OpenCode agent, whatever logins there are.
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, enabled: open && form.ai === 'opencode' });
  const base = useQuery({ queryKey: ['base', form.project], queryFn: () => api.base(form.project), enabled: open && form.project !== '' });
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
  // login. Its agents have no effort — that is a Claude Code setting.
  const openCodeModels = settings.data?.openCodeModelChoices ?? [];
  const fallbackModel = settingLabel(modelChoices, settings.data?.defaultClaudeModel ?? '', 'opus');
  // The windows the model it will run on has: the one picked, else the
  // project's, else the installation's default. The first is the installation's
  // compact window, which is what "Default" gets.
  const projectModel = selected?.agentModel && selected.agentModel !== 'auto' ? selected.agentModel : '';
  const runsOn = form.model || projectModel || settings.data?.defaultClaudeModel || 'opus';
  const contextWindows = settings.data?.claudeContextWindows?.[runsOn] ?? [];
  const contextWindow = contextWindows.includes(Number(form.contextWindow)) ? form.contextWindow : '';
  const fallbackEffort = settingLabel(effortChoices, settings.data?.defaultClaudeEffort ?? '', 'high');

  const create = useMutation({
    mutationFn: () =>
      api.createAgent({
        project: form.project,
        title: form.title.trim() || undefined,
        name: form.name.trim() || undefined,
        branch: form.branch.trim() || undefined,
        ai: form.ai,
        interface: form.ai === 'none' ? undefined : form.iface,
        // Sent either way: false is a choice, and leaving it out would mean
        // "whatever new agents do", which isn't what the switch was set to.
        autonomous: form.autonomous,
        // Left out when nothing was picked, so the model and effort new agents
        // start on apply. "" would be a choice of no model at all, and is refused.
        model: form.ai === 'claude' || form.ai === 'opencode' ? form.model || undefined : undefined,
        effort: form.ai === 'claude' ? form.effort || undefined : undefined,
        contextWindow: form.ai === 'claude' ? contextWindow || undefined : undefined,
        from: form.from.trim() || undefined,
        clean: form.clean || undefined,
        claudeAccount: form.ai === 'claude' ? form.claudeAccount || undefined : undefined,
        githubAccount: form.githubAccount || undefined,
        finishNotice: form.notify || undefined,
        // Sent only once a box has been touched. An empty string is a real
        // choice — no limit for this agent — so an untouched form must leave
        // all three out rather than send "" and uncap the agent.
        cpu: form.resourcesTouched ? form.cpu.trim() : undefined,
        cpuAllowance: form.resourcesTouched ? form.cpuAllowance.trim() : undefined,
        memory: form.resourcesTouched ? form.memory.trim() : undefined,
      }),
    onSuccess: setJob,
  });
  const cancel = useMutation({ mutationFn: (id: string) => api.cancelJob(id) });

  useEffect(() => {
    if (!open) return;
    setForm({ ...emptyForm, project: project ?? '' });
    setAdvanced(false);
    setJob(null);
    setFinished(null);
    create.reset();
    cancel.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, project]);

  const firstProject = projects.data?.[0]?.name;
  useEffect(() => {
    if (open && form.project === '' && firstProject) setForm((f) => ({ ...f, project: firstProject }));
  }, [open, form.project, firstProject]);

  // Prefilled with what new agents get, so More options shows the real numbers
  // rather than the word "default". Only until you touch them: after that the
  // boxes are yours, and a later settings refetch doesn't overwrite them.
  const { defaultCPU, defaultCPUAllowance, defaultMemory } = settings.data ?? {};
  useEffect(() => {
    if (!open || defaultCPU === undefined) return;
    setForm((f) =>
      f.resourcesTouched ? f : { ...f, cpu: defaultCPU, cpuAllowance: defaultCPUAllowance ?? '', memory: defaultMemory ?? '' },
    );
  }, [open, defaultCPU, defaultCPUAllowance, defaultMemory]);

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
  // Touching any of the three resource boxes stops them following the defaults
  // and starts them being sent, empty or not.
  const setResource = (key: 'cpu' | 'cpuAllowance' | 'memory', value: string) => setForm((f) => ({ ...f, [key]: value, resourcesTouched: true }));
  const hostCores = settings.data?.hostCores ?? 0;
  const hostMemory = settings.data?.hostMemory ?? 0;
  const needsLogin =
    (form.ai === 'claude' && auth.data?.claude === false) ||
    (form.ai === 'codex' && auth.data?.codex === false) ||
    (form.ai === 'opencode' && auth.data?.opencode === false);
  // An AI tool that is optional in the base image is no use until the image
  // has it: saying so here is the same answer the daemon would give, before
  // the form is filled in rather than after it is sent.
  const missingFromImage = form.ai === 'opencode' && setup.data?.image.installed.opencode === false;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{job ? `Creating ${form.title.trim() || 'the agent'}` : 'New agent'}</DialogTitle>
          <DialogDescription>
            {job
              ? 'This keeps running in the daemon if you close this dialog.'
              : 'Its own Linux machine, with a worktree and branch, a chat, a terminal and a browser.'}
          </DialogDescription>
        </DialogHeader>

        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={(done) => void onDone(done)} />
            {cancel.error && <Notice>{errorMessage(cancel.error)}</Notice>}
            <DialogFooter>
              {finished ? (
                <Button onClick={onClose}>Close</Button>
              ) : (
                <>
                  <Button variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate(job.id)}>
                    {cancel.isPending && <LoaderCircle className="animate-spin" />}
                    Cancel and roll back
                  </Button>
                  <Button onClick={onClose}>Keep running in background</Button>
                </>
              )}
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-5"
            onSubmit={(event) => {
              event.preventDefault();
              create.mutate();
            }}
          >
            <Field label="What will it work on?" htmlFor="agent-title" hint="A title for the sidebar. Optional.">
              <Input
                id="agent-title"
                autoFocus
                maxLength={80}
                placeholder="Medication reminders"
                value={form.title}
                onChange={(event) => {
                  const title = event.target.value;
                  setForm((f) => ({ ...f, title, branch: f.branchTouched ? f.branch : branchSlug(title) }));
                }}
              />
            </Field>

            <Field
              label="Branch"
              htmlFor="agent-branch"
              hint="Named after the work, like fix-login-redirect. One that is taken gets -2."
            >
              <div className="flex min-w-0 items-center gap-1.5">
                {selected?.branchPrefix && <span className="shrink-0 font-mono text-[13px] text-subtle">{selected.branchPrefix}</span>}
                <Input
                  id="agent-branch"
                  className="min-w-0 font-mono text-[13px]"
                  maxLength={maxBranchSlugLen}
                  pattern={branchSlugPattern}
                  title="Lowercase letters, digits and single hyphens"
                  placeholder="agent-NN"
                  value={form.branch}
                  onChange={(event) => setForm((f) => ({ ...f, branch: event.target.value, branchTouched: true }))}
                />
              </div>
            </Field>

            <Field label="Project" htmlFor="agent-project">
              <Select id="agent-project" value={form.project} onChange={(value) => set('project', value)}>
                {projects.data?.map((p) => (
                  <SelectOption key={p.name} value={p.name}>
                    {p.name}
                  </SelectOption>
                ))}
              </Select>
            </Field>

            <div className="grid gap-1.5">
              <Label>AI tool</Label>
              <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="AI tool">
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
                      <span className="text-[13px] font-medium text-primary">{tool.label}</span>
                      <span className="text-[11px] text-subtle">{tool.hint}</span>
                    </span>
                  </button>
                ))}
              </div>
            </div>

            {form.ai !== 'none' && (
              <div className="grid gap-1.5">
                <Label>Work with it in</Label>
                <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="Interface">
                  {interfaces.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      role="radio"
                      aria-checked={form.iface === option.value}
                      data-interface={option.value}
                      onClick={() => set('iface', option.value)}
                      className={cn(
                        'flex items-center gap-2.5 rounded-xl border border-line bg-surface-faint px-3 py-2.5 text-left transition hover:border-line-vivid hover:bg-surface',
                        form.iface === option.value && 'border-brand-400/50 bg-brand-500/10 shadow-[0_0_0_3px_rgb(139_92_246/0.12)]',
                      )}
                    >
                      <option.icon className={cn('size-4 shrink-0 text-subtle', form.iface === option.value && 'text-brand-300')} />
                      <span className="grid">
                        <span className="text-[13px] font-medium text-primary">{option.label}</span>
                        <span className="text-[11px] text-subtle">
                          {option.value === 'chat' ? 'Messages and approvals, in the app' : `${tools.find((t) => t.ai === form.ai)?.label}'s own command line`}
                        </span>
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            )}

            <button
              type="button"
              className="-mt-1 flex items-center gap-1.5 justify-self-start text-[13px] text-muted hover:text-primary"
              aria-expanded={advanced}
              onClick={() => setAdvanced((v) => !v)}
            >
              <ChevronRight className={cn('size-3.5 transition-transform', advanced && 'rotate-90')} />
              More options
            </button>
            {advanced && (
              <div className="-mt-2 grid animate-slide-up gap-4 rounded-xl border border-line bg-surface-faint p-4">
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Name" htmlFor="agent-name" hint="Default: agent-01, agent-02, …">
                    <Input id="agent-name" className="font-mono text-[13px]" placeholder="agent-NN" value={form.name} onChange={(event) => set('name', event.target.value)} />
                  </Field>
                  <Field label="Start from" htmlFor="agent-from" hint={selected ? `Default: ${selected.branch}` : undefined}>
                    <Input id="agent-from" className="font-mono text-[13px]" placeholder={selected?.branch ?? 'main'} value={form.from} onChange={(event) => set('from', event.target.value)} />
                  </Field>
                </div>
                {form.ai === 'opencode' && (
                  <Field
                    label="Model"
                    htmlFor="agent-opencode-model"
                    hint={
                      openCodeModels.length
                        ? "OpenCode's own provider/model ids, for the providers you have logged in to."
                        : 'Log in with agentbox auth opencode, and OpenCode names the models its providers can run.'
                    }
                  >
                    <Select id="agent-opencode-model" value={form.model} onChange={(value) => set('model', value)}>
                      <SelectOption value="">Default (what OpenCode's own configuration names)</SelectOption>
                      {openCodeModels.map((choice) => (
                        <SelectOption key={choice.value} value={choice.value}>
                          {choiceName(choice) || choice.value}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                )}
                {form.ai === 'claude' && (
                  <div className="grid grid-cols-2 gap-4">
                    <Field
                      label="Model"
                      htmlFor="agent-model"
                      hint="Claude Code's own models for this account, plus a few AgentBox pins."
                    >
                      <Select id="agent-model" value={form.model} onChange={(value) => set('model', value)}>
                        <SelectOption value="">Default ({fallbackModel})</SelectOption>
                        {modelChoices.map((choice) => (
                          <SelectOption key={choice.value} value={choice.value}>
                            {choiceName(choice) || choice.value}
                          </SelectOption>
                        ))}
                      </Select>
                    </Field>
                    <Field label="Effort" htmlFor="agent-effort" hint="How hard it thinks. Some models have no effort levels.">
                      <Select id="agent-effort" value={form.effort} onChange={(value) => set('effort', value)}>
                        <SelectOption value="">Default ({fallbackEffort})</SelectOption>
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
                    label="Context window"
                    htmlFor="agent-context-window"
                    hint="Where its chat compacts. Past the default, every step resends the whole conversation, so 1M costs up to five times as much per step late in a long task."
                  >
                    <Select id="agent-context-window" value={contextWindow} onChange={(value) => set('contextWindow', value)}>
                      <SelectOption value="">Default ({formatTokens(contextWindows[0])})</SelectOption>
                      {contextWindows.slice(1).map((n) => (
                        <SelectOption key={n} value={String(n)}>
                          {formatTokens(n)}, the model's whole window
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                )}
                {form.ai === 'claude' && accounts.length > 1 && (
                  <Field
                    label="Claude Code account"
                    htmlFor="agent-claude-account"
                    hint={selected?.claudeAccount ? `${selected.name} uses ${selected.claudeAccount}.` : `This machine's default is ${defaultAccount}.`}
                  >
                    <Select id="agent-claude-account" value={form.claudeAccount} onChange={(value) => set('claudeAccount', value)}>
                      <SelectOption value="">{selected?.claudeAccount ? `The project's (${selected.claudeAccount})` : `Default (${defaultAccount})`}</SelectOption>
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
                    label="GitHub account"
                    htmlFor="agent-github-account"
                    hint={selected?.githubAccount ? `${selected.name} uses ${selected.githubAccount}.` : `This machine's default is ${defaultGitHubAccount}.`}
                  >
                    <Select id="agent-github-account" value={form.githubAccount} onChange={(value) => set('githubAccount', value)}>
                      <SelectOption value="">{selected?.githubAccount ? `The project's (${selected.githubAccount})` : `Default (${defaultGitHubAccount})`}</SelectOption>
                      {githubAccounts.map((account) => (
                        <SelectOption key={account.name} value={account.name}>
                          {account.name}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                )}
                <Field
                  label="When it finishes"
                  htmlFor="agent-notify"
                  hint="Only matters if the project's own setting is “let the agent decide”."
                >
                  <Select id="agent-notify" value={form.notify} onChange={(value) => set('notify', value as Form['notify'])}>
                    <SelectOption value="">Project setting</SelectOption>
                    <SelectOption value="chat">Wake the chat</SelectOption>
                    <SelectOption value="off">Only record it</SelectOption>
                  </Select>
                </Field>
                <div className="grid grid-cols-3 gap-4">
                  <Field label="CPU cores" htmlFor="agent-cpu" hint={hostCores ? `${hostCores} on this host` : undefined}>
                    <Input
                      id="agent-cpu"
                      className="font-mono text-[13px]"
                      placeholder="every core"
                      value={form.cpu}
                      onChange={(event) => setResource('cpu', event.target.value)}
                    />
                  </Field>
                  <Field label="CPU share" htmlFor="agent-cpu-allowance" hint="Only when busy">
                    <Input
                      id="agent-cpu-allowance"
                      className="font-mono text-[13px]"
                      placeholder="all of it"
                      value={form.cpuAllowance}
                      onChange={(event) => setResource('cpuAllowance', event.target.value)}
                    />
                  </Field>
                  <Field label="Memory" htmlFor="agent-memory" hint={hostMemory ? `${humanBytes(hostMemory)} on this host` : undefined}>
                    <Input
                      id="agent-memory"
                      className="font-mono text-[13px]"
                      placeholder="all of it"
                      value={form.memory}
                      onChange={(event) => setResource('memory', event.target.value)}
                    />
                  </Field>
                </div>
                <SwitchRow
                  id="agent-autonomous"
                  label="Autonomous"
                  hint="Runs the AI tool without asking for permission. The agent's machine is its sandbox."
                  checked={form.autonomous}
                  disabled={form.ai === 'none'}
                  onChange={(value) => set('autonomous', value)}
                />
                {base.data && (
                  <SwitchRow
                    id="agent-clean"
                    label="Skip the project base"
                    hint={`Start from the plain base image instead of the base saved from ${base.data.savedFrom}.`}
                    checked={form.clean}
                    onChange={(value) => set('clean', value)}
                  />
                )}
              </div>
            )}

            {needsLogin && (
              <Notice tone="warning">
                AgentBox has no {aiLabel(form.ai)} login yet. Add one in Settings, or run <Code>agentbox auth {form.ai}</Code>.
              </Notice>
            )}
            {missingFromImage && (
              <Notice tone="warning">
                The base image was built without OpenCode. Turn it on in Settings, or run <Code>agentbox image build --opencode</Code>.
              </Notice>
            )}
            {image.data?.ready === false && (
              <Notice tone="warning">
                The base image isn't built yet. Build it in Settings, or run <Code>agentbox image build</Code>.
              </Notice>
            )}
            {create.error && <Notice>{errorMessage(create.error)}</Notice>}

            <DialogFooter>
              <Button variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={!form.project || create.isPending}>
                {create.isPending && <LoaderCircle className="animate-spin" />}
                Create agent
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
