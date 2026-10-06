import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, LoaderCircle, MessageSquare, SquareTerminal } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import { agentSizes } from '../lib/agentSize';
import { api } from '../lib/api';
import { projectLabel } from '../lib/projectName';
import { branchSlug, branchSlugPattern, maxBranchSlugLen } from '../lib/branch';
import { formatTokens } from '../lib/chat';
import { choiceName } from '../lib/modelChoices';
import { useT, type MessageKey } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
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
  // Start when a slot is free, rather than right away. Defaults to the
  // project's own alwaysQueue until you touch the switch yourself.
  queue: boolean;
  queueTouched: boolean;
  // What it reserves of the VM's memory (lib/agentSize.ts); "" is auto.
  size: string;
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
  queue: false,
  queueTouched: false,
  size: '',
};

// A label or hint that is a brand's name is shown as it is; the rest are keys.
const tools: { ai: string; label?: string; labelKey?: MessageKey; hint?: string; hintKey?: MessageKey }[] = [
  { ai: 'claude', label: 'Claude Code', hint: 'Anthropic' },
  { ai: 'codex', label: 'Codex', hint: 'OpenAI' },
  { ai: 'opencode', label: 'OpenCode', hintKey: 'project.newAgent.tool.openSource' },
  { ai: 'none', labelKey: 'project.newAgent.tool.shellOnly', hintKey: 'project.newAgent.tool.noAI' },
];

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
  const sizes = agentSizes();
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
  // project's, else the installation's default. "Default" is the window new
  // agents start with in Settings when that model has it, else the first, the
  // installation's compact window — what the daemon gives an agent nobody
  // chose a window for (claudeChatDefaults).
  const projectModel = selected?.agentModel && selected.agentModel !== 'auto' ? selected.agentModel : '';
  const runsOn = form.model || projectModel || settings.data?.defaultClaudeModel || 'opus';
  const contextWindows = settings.data?.claudeContextWindows?.[runsOn] ?? [];
  const contextWindow = contextWindows.includes(Number(form.contextWindow)) ? form.contextWindow : '';
  const settingsWindow = Number(settings.data?.defaultAgentContextWindow || 0);
  const defaultWindow = contextWindows.includes(settingsWindow) ? settingsWindow : contextWindows[0];
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
        // Sent either way, like autonomous above: "whatever the project does
        // by default" isn't a request queue can leave out and still mean.
        // Off outright while the installation's Agent queue is off, whatever
        // the switch (disabled, so untouched) still carries from before.
        queue: queueEnabled && form.queue,
        size: form.size || undefined,
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

  // Queue follows the project's own default until you touch the switch.
  useEffect(() => {
    if (!open || selected === undefined) return;
    setForm((f) => (f.queueTouched ? f : { ...f, queue: selected.alwaysQueue }));
  }, [open, selected]);

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
  // The installation's own switch (Settings → Agents → Agent queue): off,
  // this dialog's Queue switch does nothing, so it shows disabled instead of
  // offering a choice the daemon would refuse to act on.
  const queueEnabled = settings.data?.agentQueue ?? false;
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
                {selected?.branchPrefix && <span className="shrink-0 font-mono text-[13px] text-subtle">{selected.branchPrefix}</span>}
                <Input
                  id="agent-branch"
                  className="min-w-0 font-mono text-[13px]"
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
              <Select id="agent-project" value={form.project} onChange={(value) => set('project', value)}>
                {projects.data?.map((p) => (
                  <SelectOption key={p.name} value={p.name}>
                    {projectLabel(p)}
                  </SelectOption>
                ))}
              </Select>
            </Field>

            <SwitchRow
              id="agent-queue"
              label={t('project.newAgent.queueLabel')}
              hint={queueEnabled ? t('project.newAgent.queueHint') : t('project.newAgent.queueOff')}
              checked={queueEnabled && form.queue}
              disabled={!queueEnabled}
              onChange={(value) => setForm((f) => ({ ...f, queue: value, queueTouched: true }))}
            />

            <Field label={t('project.newAgent.sizeLabel')} htmlFor="agent-size" hint={t('project.newAgent.sizeHint')}>
              <Select id="agent-size" value={form.size} onChange={(value) => set('size', value)}>
                {sizes.map((size) => (
                  <SelectOption key={size.value} value={size.value}>
                    {size.label} <span className="text-subtle">· {size.tip}</span>
                  </SelectOption>
                ))}
              </Select>
            </Field>

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
                      <span className="text-[13px] font-medium text-primary">{tool.labelKey ? t(tool.labelKey) : tool.label}</span>
                      <span className="text-[11px] text-subtle">{tool.hintKey ? t(tool.hintKey) : tool.hint}</span>
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
                        <span className="text-[13px] font-medium text-primary">{t(option.label)}</span>
                        <span className="text-[11px] text-subtle">
                          {option.value === 'chat'
                            ? t('project.newAgent.chatHint')
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
              className="-mt-1 flex items-center gap-1.5 justify-self-start text-[13px] text-muted hover:text-primary"
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
                    <Input id="agent-name" className="font-mono text-[13px]" placeholder="agent-NN" value={form.name} onChange={(event) => set('name', event.target.value)} />
                  </Field>
                  <Field label={t('project.newAgent.fromLabel')} htmlFor="agent-from" hint={selected ? t('project.newAgent.fromHint', { branch: selected.branch }) : undefined}>
                    <Input id="agent-from" className="font-mono text-[13px]" placeholder={selected?.branch ?? 'main'} value={form.from} onChange={(event) => set('from', event.target.value)} />
                  </Field>
                </div>
                {form.ai === 'opencode' && (
                  <Field
                    label={t('project.newAgent.modelLabel')}
                    htmlFor="agent-opencode-model"
                    hint={openCodeModels.length ? t('project.newAgent.openCodeModelHint') : t('project.newAgent.openCodeLoginHint')}
                  >
                    <Select id="agent-opencode-model" value={form.model} onChange={(value) => set('model', value)}>
                      <SelectOption value="">{t('project.newAgent.openCodeDefault')}</SelectOption>
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
            {image.data?.ready === false && (
              <Notice tone="warning">
                {t.rich('project.newAgent.noImage', { code: (c) => <Code>{c}</Code> })}
              </Notice>
            )}
            {create.error && <Notice>{errorMessage(create.error)}</Notice>}

            <DialogFooter>
              <Button variant="ghost" onClick={onClose}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={!form.project || create.isPending}>
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
